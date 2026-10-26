#!/usr/bin/env python3
"""Run read-only S1 calibration on a real Mac via a temporary SSH tunnel.

Run on tx from a clean repository. No table reset, service recreation or cache
flush occurs. These results cannot replace the S1 read/write comparison matrix.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile


def run(args, **kw):
    return subprocess.run(args, check=True, **kw)


# Executed on the Mac, using only the Python standard library and a copied binary.
REMOTE = r'''
import datetime, hashlib, json, os, platform, resource, socket, subprocess, sys, time, urllib.request
from pathlib import Path
c = json.loads(sys.argv[1])
if platform.system() != "Darwin":
    raise SystemExit("calibration client must be a real Mac")
root = Path(c["dir"])
soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
resource.setrlimit(resource.RLIMIT_NOFILE, (min(4096, hard) if hard != resource.RLIM_INFINITY else 4096, hard))
def port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]
biz, adm = port(), port()
while biz == adm:
    adm = port()
tunnel = subprocess.Popen(["ssh", "-N", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes",
    "-o", "ServerAliveInterval=10", "-L", f"127.0.0.1:{biz}:127.0.0.1:18086",
    "-L", f"127.0.0.1:{adm}:127.0.0.1:19086", "tx"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
try:
    url = f"http://127.0.0.1:{adm}/debug/strategy"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for attempt in range(100):
        if tunnel.poll() is not None:
            raise RuntimeError("SSH tunnel exited")
        try:
            with opener.open(url, timeout=1) as r:
                strategy = json.load(r)
            break
        except OSError:
            time.sleep(.1)
    else:
        raise RuntimeError("forwarded admin endpoint did not respond")
    rtts = []
    for _ in range(100):
        start = time.perf_counter_ns()
        with opener.open(url, timeout=2) as r:
            r.read()
        rtts.append((time.perf_counter_ns()-start)/1000)
    env = {"calibration_only": True, "git_commit": c["commit"], "client_host": socket.gethostname(),
        "system": platform.platform(), "machine": platform.machine(), "cpus": os.cpu_count(),
        "nofile_before": [soft, hard], "nofile_effective": resource.getrlimit(resource.RLIMIT_NOFILE),
        "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "strategy": strategy, "admin_http_round_trip_us": rtts,
        "network_scope": "Mac to tx via SSH; HTTP admin round trip includes connection setup and server handling",
        "binary_sha256": hashlib.sha256((root/"loadgen").read_bytes()).hexdigest(),
        "popularity_sha256": hashlib.sha256((root/"popularity.u32").read_bytes()).hexdigest()}
    (root/"environment.json").write_text(json.dumps(env, indent=2)+"\n")
    valid = True
    for rate in c["rates"]:
        stem = f"read-only-{rate}"
        subprocess.run([str(root/"loadgen"), "s1", "--target", f"http://127.0.0.1:{biz}",
            "--admin", f"http://127.0.0.1:{adm}", "--popularity", str(root/"popularity.u32"),
            "--read-ratio", "1", "--rate", str(rate), "--warmup", c["warmup"], "--window", c["window"],
            "--max-inflight", "512", "--story", "S1-calibration", "--arm", strategy["mode"],
            "--data-version", c["data_version"], "--output", str(root/(stem+".json")),
            "--raw", str(root/(stem+".csv.gz"))], check=True)
        doc = json.loads((root/(stem+".json")).read_text())
        valid = valid and doc["result"]["valid"]
    (root/"status.json").write_text(json.dumps({"all_valid":valid,"calibration_only":True})+"\n")
finally:
    tunnel.terminate()
    try:
        tunnel.communicate(timeout=10)
    except subprocess.TimeoutExpired:
        tunnel.kill()
        tunnel.communicate()
'''


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--host', default='luomac')
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--popularity', type=Path, default=Path('/home/ubuntu/datasets/backend-cloud-labs/p1/catalog/popularity.u32'))
    p.add_argument('--rates', type=int, nargs='+', default=[50, 200, 500])
    p.add_argument('--warmup', default='3s')
    p.add_argument('--window', default='10s')
    a = p.parse_args()
    repo = Path(__file__).resolve().parents[1]
    os.chdir(repo)
    if subprocess.check_output(['git', 'status', '--porcelain']).strip():
        p.error('clean worktree required')
    if a.output.exists():
        p.error('output directory already exists; preserve previous evidence')
    if len(set(a.rates)) != len(a.rates) or any(r <= 0 or r > 100000 for r in a.rates):
        p.error('rates must be distinct integers within 1..100000')
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    ssh = ['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', a.host]
    identity = subprocess.check_output(ssh+['uname -s; uname -m'], text=True).splitlines()
    if len(identity) != 2 or identity[0] != 'Darwin' or identity[1] not in ('arm64', 'x86_64'):
        p.error('target must identify as Darwin arm64 or x86_64')
    manifest = json.loads((repo/'datasets/stats/catalog.json').read_text())
    expected = next(x['sha256'] for x in manifest['artifacts'] if x['path'] == 'popularity.u32')
    if hashlib.sha256(a.popularity.read_bytes()).hexdigest() != expected:
        p.error('popularity hash differs from catalog manifest')
    remote = subprocess.check_output(ssh+['mktemp -d /tmp/bcl-s1-calibration.XXXXXX'], text=True).strip()
    config = dict(dir=remote, commit=commit, rates=a.rates, warmup=a.warmup, window=a.window,
                  data_version=manifest['data_version'])
    a.output.mkdir(parents=True)
    with tempfile.TemporaryDirectory(prefix='bcl-s1-build-') as build:
        binary = Path(build)/'loadgen'
        run(['go', '-C', 'loadgen', 'build', '-trimpath', '-ldflags', f'-X main.sourceCommit={commit}',
             '-o', str(binary), './cmd/loadgen'], env={**os.environ, 'GOOS':'darwin',
             'GOARCH':'arm64' if identity[1]=='arm64' else 'amd64', 'CGO_ENABLED':'0'})
        run(['scp', '-q', str(binary), f'{a.host}:{remote}/loadgen'])
        run(['scp', '-q', str(a.popularity), f'{a.host}:{remote}/popularity.u32'])
        try:
            run(ssh+[shlex.join(['python3', '-', json.dumps(config)])], input=REMOTE, text=True)
        finally:
            # Retain diagnostics even when the remote run fails; no credential files copied.
            run(['scp', '-q', f'{a.host}:{remote}/*.json', f'{a.host}:{remote}/*.csv.gz', str(a.output)])
    sums = {x.name:hashlib.sha256(x.read_bytes()).hexdigest() for x in sorted(a.output.iterdir())}
    (a.output/'sha256.json').write_text(json.dumps(sums, indent=2)+'\n')
    print('Calibration evidence:', a.output, flush=True)
    if not json.loads((a.output/'status.json').read_text())['all_valid']:
        raise SystemExit('calibration contains invalid runs; inspect invalid_reasons')


if __name__ == '__main__':
    main()
