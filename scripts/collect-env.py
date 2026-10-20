#!/usr/bin/env python3
"""Collect actual local evidence; a Linux run cannot stand in for a Mac client."""
import argparse
import datetime
import hashlib
import json
import os
import platform
import resource
import shutil
import socket
import subprocess
import sys
import time
from pathlib import Path


def command(argv):
    try:
        r = subprocess.run(argv, capture_output=True, text=True, timeout=15)
        return {"command": argv, "exit_code": r.returncode, "stdout": r.stdout.strip(),
                "stderr": r.stderr.strip()}
    except (OSError, subprocess.TimeoutExpired) as error:
        return {"command": argv, "exit_code": None, "error_type": type(error).__name__}


def collect(role, target=None, ports=(), samples=20):
    if role == "mac" and sys.platform != "darwin":
        raise ValueError("Mac evidence must be collected on macOS, never on tx")
    if role == "tx" and sys.platform != "linux":
        raise ValueError("tx evidence must be collected on Linux")
    state = command(["git", "status", "--porcelain"])
    if state.get("exit_code") != 0 or state.get("stdout"):
        raise ValueError("clean repository required before environment collection")
    result = {
        "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "role": role, "hostname": socket.gethostname(), "platform": platform.platform(),
        "system": platform.system(), "machine": platform.machine(), "cpu_count": os.cpu_count(),
        "nofile": resource.getrlimit(resource.RLIMIT_NOFILE),
        "timezone": time.tzname, "disk": shutil.disk_usage(Path.cwd())._asdict(),
        "git_commit": command(["git", "rev-parse", "HEAD"])["stdout"],
        "working_tree_modified_before_collection": False,
        "collector_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "go": command(["go", "version"]),
        "docker": command(["docker", "version", "--format", "{{json .}}"]),
        "network": [],
    }
    if sys.platform == "darwin":
        result["cpu"] = command(["sysctl", "-n", "machdep.cpu.brand_string"])
        result["memory"] = command(["sysctl", "hw.memsize", "vm.swapusage"])
        result["kernel"] = command(["sw_vers"])
    else:
        result["cpu"] = command(["lscpu", "--json"])
        result["memory"] = Path("/proc/meminfo").read_text()
        result["swap_activity"] = command(["vmstat", "1", "2"])
        result["kernel"] = command(["uname", "-a"])
        result["affinity"] = sorted(os.sched_getaffinity(0))
    for port in ports if target else ():
        observations = []
        for _ in range(samples):
            start = time.perf_counter()
            try:
                with socket.create_connection((target, port), timeout=2):
                    error = None
            except OSError as exc:
                error = type(exc).__name__
            observations.append({"connect_seconds": time.perf_counter() - start,
                                 "success": error is None, "error_type": error})
            time.sleep(0.05)
        result["network"].append({"target": target, "port": port,
                                  "metric": "TCP connection establishment, not HTTP request latency",
                                  "samples": observations})
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--role", choices=("tx", "mac"), required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--target")
    parser.add_argument("--ports", type=int, nargs="*", default=(18086, 18082, 18085))
    parser.add_argument("--samples", type=int, default=20)
    args = parser.parse_args()
    if args.samples < 1 or args.samples > 1000:
        parser.error("samples must be in [1, 1000]")
    try:
        data = collect(args.role, args.target, args.ports, args.samples)
    except ValueError as exc:
        parser.error(str(exc))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"role": args.role, "output": str(args.output),
                      "git_commit": data["git_commit"]}))
