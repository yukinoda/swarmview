"""Compare terminal overhead on macOS without counting hosted agents as terminal RAM."""

import argparse
import json
import statistics
import subprocess
import time


def terminal(executable):
    name = executable.lower()
    if "/orca.app/" in name or "/orca/daemon-host/" in name:
        return "orca"
    if "/ghostty.app/" in name:
        return "ghostty"
    if name.rsplit("/", 1)[-1] == "tmux" or name.startswith("tmux:"):
        return "tmux"
    return None


def sample():
    groups = {name: [] for name in ("orca", "ghostty", "tmux")}
    output = subprocess.check_output(["ps", "-axo", "pid=,rss=,comm="], text=True)
    for line in output.splitlines():
        fields = line.strip().split(None, 2)
        if len(fields) == 3 and (group := terminal(fields[2])):
            groups[group].append({"pid": int(fields[0]), "rss_mib": int(fields[1]) / 1024})
    return groups


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--interval", type=float, default=1)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        assert terminal("/Applications/Orca.app/Contents/MacOS/Orca") == "orca"
        assert terminal("/Users/a/Library/Application Support/orca/daemon-host/host") == "orca"
        assert terminal("/Applications/Ghostty.app/Contents/MacOS/ghostty") == "ghostty"
        assert terminal("/opt/homebrew/bin/tmux") == "tmux"
        assert terminal("tmux: server") == "tmux"
        assert terminal("opencode") is None  # Hosted agents are not terminal overhead.
        return
    if args.samples < 1 or args.interval < 0:
        parser.error("samples must be positive and interval must be nonnegative")
    samples = []
    for i in range(args.samples):
        samples.append(sample())
        if i + 1 < args.samples:
            time.sleep(args.interval)
    totals = {name: [sum(p["rss_mib"] for p in s[name]) for s in samples] for name in samples[0]}
    print(json.dumps({
        "sample_count": args.samples,
        "seconds_between": args.interval,
        "summed_rss_mib": {name: {"min": round(min(v), 1), "median": round(statistics.median(v), 1), "max": round(max(v), 1)} for name, v in totals.items()},
        "last_processes": samples[-1],
        "system": subprocess.check_output(["sysctl", "hw.memsize", "kern.memorystatus_vm_pressure_level", "vm.swapusage"], text=True).strip(),
        "note": "Summed RSS counts shared pages per process; it is not the amount quitting would reclaim. Compare equivalent workloads after warm-up.",
    }, indent=2))


if __name__ == "__main__":
    main()
