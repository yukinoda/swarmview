"""Check the built macOS app; optionally sample its complete launch footprint."""

import argparse
import ctypes
import json
import os
from pathlib import Path
import socket
import statistics
import subprocess
import time
import urllib.request


def processes():
    rows = {}
    for line in subprocess.check_output(["ps", "-axo", "pid=,ppid=,rss=,comm="], text=True).splitlines():
        fields = line.strip().split(None, 3)
        if len(fields) == 4:
            rows[int(fields[0])] = (int(fields[1]), int(fields[2]), fields[3])
    return rows


def wait_for(predicate, description):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.05)
    raise AssertionError("Timed out: " + description)


def serving():
    try:
        with urllib.request.urlopen("http://127.0.0.1:4778/agents", timeout=0.5) as response:
            return response.status == 200
    except OSError:
        return False


def backend_pid(app_pid):
    return next((pid for pid, (parent, _, name) in processes().items()
                 if parent == app_pid and name.endswith("/swarmview-server")), None)


class Usage(ctypes.Structure):
    # macOS rusage_info_v2: UUID followed by 18 uint64 fields; physical footprint is field 7.
    _fields_ = [("uuid", ctypes.c_uint8 * 16), ("values", ctypes.c_uint64 * 18)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("app", type=Path)
    parser.add_argument("--seconds", type=int, default=0)
    parser.add_argument("--compare-cli", action="store_true", help="compare tmux targets with a CLI on port 4777 using the same default server")
    args = parser.parse_args()
    executable = args.app.resolve() / "Contents/MacOS/Swarmview"
    assert executable.is_file(), "Build Swarmview.app first"
    assert args.seconds >= 0
    subprocess.run([str(executable), "--installer-self-test"], check=True, timeout=10,
                   stdout=subprocess.DEVNULL)
    probe = socket.socket()
    try:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        probe.bind(("127.0.0.1", 4778))
    finally:
        probe.close()

    # Run before a force-quit: a dying WebKit helper may briefly retry an old poll.
    with socket.socket() as conflict:
        conflict.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        conflict.bind(("127.0.0.1", 4778))
        conflict.listen()
        failed = subprocess.run([str(executable), "--smoke-test"], capture_output=True, text=True, timeout=20)
        assert failed.returncode != 0 and "address already in use" in failed.stderr, failed.stderr
        conflict.settimeout(0.2)
        try:
            connection, _ = conflict.accept()
        except socket.timeout:
            pass
        else:
            connection.close()
            raise AssertionError("App connected to unrelated listener")

    smoke = subprocess.run([str(executable), "--smoke-test"], capture_output=True, text=True, timeout=30)
    assert smoke.returncode == 0 and "SWARMVIEW_SMOKE" in smoke.stdout, smoke.stdout + smoke.stderr
    wait_for(lambda: not serving(), "normal quit closes backend")

    before = processes()
    app = subprocess.Popen([str(executable)], env={**os.environ, "PATH": "/usr/bin:/bin"},
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    samples = []
    try:
        wait_for(serving, "Finder-like launch")
        child = backend_pid(app.pid)
        assert child, "No owned backend"
        if args.compare_cli:
            def targets(port):
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/agents", timeout=5) as response:
                    return {a["pid"]: (a.get("tmuxTarget"), a["tmuxAttached"])
                            for a in json.load(response)["agents"]}
            cli, desktop = targets(4777), targets(4778)
            common = cli.keys() & desktop.keys()
            assert common, "No shared agents to compare"
            assert all(cli[pid] == desktop[pid] for pid in common), "Native/CLI tmux targets differ"
        second = subprocess.run([str(executable)], capture_output=True, text=True, timeout=10)
        assert second.returncode == 0, second.stderr
        assert app.poll() is None and backend_pid(app.pid) == child, "Second launch replaced backend"
        lib = ctypes.CDLL("/usr/lib/libproc.dylib")
        lib.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_void_p]
        lib.proc_pid_rusage.restype = ctypes.c_int
        deadline = time.monotonic() + args.seconds
        while time.monotonic() < deadline:
            rows = processes()
            owned = {app.pid, child}
            # WebKit XPC helpers have launchd parents. Include every newly created
            # WebKit helper, not just descendants; other WebKit apps should stay quiet.
            owned.update(pid for pid, (_, _, name) in rows.items()
                         if pid not in before and "/com.apple.WebKit." in name)
            sample = {"app": 0, "backend": 0, "webkit": 0, "rss": 0, "missing": 0}
            for pid in owned:
                if pid not in rows:
                    continue
                usage = Usage()
                if lib.proc_pid_rusage(pid, 2, ctypes.byref(usage)) != 0:
                    sample["missing"] += 1
                    continue
                group = "app" if pid == app.pid else "backend" if pid == child else "webkit"
                sample[group] += usage.values[7] / 1024 ** 2
                sample["rss"] += rows[pid][1] / 1024
            sample["total"] = sum(sample[g] for g in ("app", "backend", "webkit"))
            samples.append(sample)
            time.sleep(2)
        app.kill()  # Force-quit must not orphan the backend.
        app.wait(timeout=10)
        wait_for(lambda: child not in processes(), "force-quit reaps backend")
        wait_for(lambda: not serving(), "force-quit closes listener")
    finally:
        if app.poll() is None:
            app.kill()
            app.wait(timeout=10)

    result = {"checks": "passed", "sampleSeconds": args.seconds, "sampleCount": len(samples),
              "note": "Footprint MiB, including new WebKit XPC helpers. Run without concurrent WebKit app launches; RSS double-counts shared pages."}
    if args.compare_cli:
        result["tmuxParityAgents"] = len(common)
    if samples:
        result["memory"] = {key: {"median": round(statistics.median(s[key] for s in samples), 1),
                                  "peak": round(max(s[key] for s in samples), 1)}
                            for key in ("app", "backend", "webkit", "total", "rss")}
        result["missingFootprints"] = sum(s["missing"] for s in samples)
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
