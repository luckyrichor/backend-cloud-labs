#!/usr/bin/env python3
"""Private-stdin command worker for the S1 controller. Run on the real Mac only."""
import json
import os
from pathlib import Path
import platform
import resource
import socket
import subprocess
import sys
import time
import urllib.request

root = Path(sys.argv[1])
if platform.system() != 'Darwin':
    raise SystemExit('S1 client must run on Darwin')
secret = json.loads((root/'private.json').read_text())
soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
resource.setrlimit(resource.RLIMIT_NOFILE, (min(8192, hard) if hard != resource.RLIM_INFINITY else 8192, hard))
ports = []
while len(ports) < 3:
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        n = s.getsockname()[1]
        if n not in ports:
            ports.append(n)
biz, admin, mysql = ports
args = ['ssh','-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=10']
for local, remote in zip(ports, [18096,19096,13306]):
    args += ['-L',f'127.0.0.1:{local}:127.0.0.1:{remote}']
tunnel = subprocess.Popen(args+['tx'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def send(obj):
    print(json.dumps(obj), flush=True)

try:
    time.sleep(.5)
    if tunnel.poll() is not None:
        raise RuntimeError('SSH tunnel failed')
    dsn = secret['dsn'].replace('127.0.0.1:13306',f'127.0.0.1:{mysql}')
    (root/'bypass.dsn').write_text(dsn)
    (root/'bypass.dsn').chmod(0o600)
    send({'ready':True,'host':socket.gethostname(),'platform':platform.platform(),
          'cpus':os.cpu_count(),'nofile':resource.getrlimit(resource.RLIMIT_NOFILE),
          'machine':platform.machine()})
    for line in sys.stdin:
        c = json.loads(line)
        if c['op'] == 'bye':
            break
        if c['op'] == 'rtt':
            values = []
            for _ in range(30):
                start = time.perf_counter_ns()
                with opener.open(f'http://127.0.0.1:{admin}/debug/strategy',timeout=3) as r:
                    r.read()
                values.append((time.perf_counter_ns()-start)/1000)
            send({'round_trip_us':values})
            continue
        stem = c['stem']
        # Controller owns names; never accept paths supplied by arbitrary messages.
        if not stem.replace('-','').replace('_','').isalnum():
            raise ValueError('invalid run name')
        env = {**os.environ,'LOADGEN_AUTHORIZATION':secret['authorization']}
        argv = [str(root/'loadgen'),'s1','--target',f'http://127.0.0.1:{biz}',
                '--admin',f'http://127.0.0.1:{admin}','--popularity',str(root/'popularity.u32'),
                '--read-ratio',str(c['ratio']),'--rate',str(c['rate']),
                '--warmup',f"{c['warmup']}s",'--window',f"{c['window']}s",'--seed',str(c['seed']),
                '--max-inflight','2048','--timeout','3s','--story',c['story'],
                '--arm',c['arm'],'--repeat',str(c['repeat']),'--data-version',c['data_version'],
                '--output',str(root/(stem+'.json')),'--raw',str(root/(stem+'.csv.gz'))]
        if c.get('bypass'):
            argv += ['--bypass-dsn-file',str(root/'bypass.dsn')]
        r = subprocess.run(argv,env=env,capture_output=True,text=True)
        # Driver errors are intentionally generic; no private configuration is printed.
        send({'exit_code':r.returncode,'stdout':r.stdout,'stderr':r.stderr})
finally:
    tunnel.terminate()
    try:
        tunnel.communicate(timeout=10)
    except subprocess.TimeoutExpired:
        tunnel.kill()
        tunnel.communicate()
    for p in (root/'private.json',root/'bypass.dsn'):
        p.unlink(missing_ok=True)
