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
    parser.add_argument("--no-communication", dest="communication", action="store_false", help="check the opt-out launch; communication is on by default and the TUI plugin is never installed")
    parser.add_argument("--compare-cli", action="store_true", help="compare tmux targets with a CLI on port 4777 using the same default server")
    args = parser.parse_args()
    executable = args.app.resolve() / "Contents/MacOS/Swarmview"
    assert executable.is_file(), "Build Swarmview.app first"
    resources = executable.parent.parent / "Resources"
    source = Path(__file__).resolve().parents[2] / "assets/aider-LICENSE.txt"
    assert (resources / source.name).read_bytes() == source.read_bytes(), "App must include the Aider icon license"
    assert args.seconds >= 0
    bridge = executable.parent.parent / "Resources/integrations/opencode"
    bridge_source = Path(__file__).resolve().parents[2] / "integrations/opencode"
    for name in ("package.json", "tui.ts", "observer.js", "README.md"):
        assert (bridge / name).read_bytes() == (bridge_source / name).read_bytes(), f"App must include matching optional bridge file: {name}"
    hooks = "integrations/claude/README.md"
    hooks_source = Path(__file__).resolve().parents[2] / hooks
    assert (resources / hooks).read_bytes() == hooks_source.read_bytes(), "App must include the Claude hook setup"
    assert not any(Path(name).name == "Swarmview" for _, _, name in processes().values()), "Quit existing Swarmview apps yourself before owned-app checks"
    launch = [str(executable)] + ([] if args.communication else ["--no-communication"])
    live_socket = Path.home() / "Library/Caches/swarmview/live.sock"
    live_hook = live_socket.with_name("live-hook")  # the Claude plugin's private copy
    if args.communication:
        assert not live_socket.exists(), "Existing communication socket: do not replace another viewer's listener"
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
        failed = subprocess.run(launch + ["--smoke-test"], capture_output=True, text=True, timeout=20)
        assert failed.returncode != 0 and "address already in use" in failed.stderr, failed.stderr
        conflict.settimeout(0.2)
        try:
            connection, _ = conflict.accept()
        except socket.timeout:
            pass
        else:
            connection.close()
            raise AssertionError("App connected to unrelated listener")

    if args.communication:
        assert not live_socket.exists(), "Port-conflict startup leaked a bridge socket"
    smoke = subprocess.run(launch + ["--smoke-test"], capture_output=True, text=True, timeout=30)
    assert smoke.returncode == 0 and "SWARMVIEW_SMOKE" in smoke.stdout, smoke.stdout + smoke.stderr
    wait_for(lambda: not serving(), "normal quit closes backend")
    if args.communication:
        wait_for(lambda: not live_socket.exists(), "normal quit closes owned bridge socket")
        wait_for(lambda: not live_hook.exists(), "normal quit removes the hook copy")

    before = processes()
    app = subprocess.Popen(launch, env={**os.environ, "PATH": "/usr/bin:/bin"},
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    samples = []
    try:
        wait_for(serving, "Finder-like launch")
        child = backend_pid(app.pid)
        assert child, "No owned backend"
        def communication_enabled():
            with urllib.request.urlopen("http://127.0.0.1:4778/agents", timeout=5) as response:
                return bool(json.load(response).get("communication"))
        assert communication_enabled() == args.communication, "Native flag did not reach bundled backend"
        if args.communication:
            assert live_hook.exists(), "Communication must publish the hook copy"
        if args.compare_cli:
            def targets(port):
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/agents", timeout=5) as response:
                    return {a["pid"]: (a.get("tmuxTarget"), a["tmuxAttached"])
                            for a in json.load(response)["agents"]}
            cli, desktop = targets(4777), targets(4778)
            common = cli.keys() & desktop.keys()
            assert common, "No shared agents to compare"
            assert all(cli[pid] == desktop[pid] for pid in common), "Native/CLI tmux targets differ"
        # An inverse-mode second launch must not silently reconfigure the singleton.
        second = subprocess.run([str(executable)] + (["--no-communication"] if args.communication else []), capture_output=True, text=True, timeout=10)
        assert second.returncode == 0, second.stderr
        assert app.poll() is None and backend_pid(app.pid) == child, "Second launch replaced backend"
        assert communication_enabled() == args.communication, "Second launch changed existing communication mode"
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
        if args.communication:
            wait_for(lambda: not live_socket.exists(), "force-quit closes owned bridge socket")
            wait_for(lambda: not live_hook.exists(), "force-quit removes the hook copy")
    finally:
        if app.poll() is None:
            app.kill()
            app.wait(timeout=10)

    result = {"checks": "passed", "communication": args.communication, "sampleSeconds": args.seconds, "sampleCount": len(samples),
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
