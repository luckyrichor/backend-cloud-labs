#!/usr/bin/env python3
"""Mac S1 worker: four independent business SSH streams plus an isolated control stream."""
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

root=Path(sys.argv[1])
if platform.system()!='Darwin':raise SystemExit('S1 client must run on Darwin')
awake=subprocess.Popen(['/usr/bin/caffeinate','-i','-w',str(os.getpid())],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
secret=json.loads((root/'private.json').read_text())
soft,hard=resource.getrlimit(resource.RLIMIT_NOFILE)
resource.setrlimit(resource.RLIMIT_NOFILE,(min(8192,hard) if hard!=resource.RLIM_INFINITY else 8192,hard))
ports=[]
while len(ports)<6:
    with socket.socket() as s:
        s.bind(('127.0.0.1',0));n=s.getsockname()[1]
        if n not in ports:ports.append(n)
business=ports[:4];admin,mysql=ports[4:]
specs=[('control',[(admin,19096),(mysql,13306)])]
specs += [(f'business-{i}',[(port,18096)]) for i,port in enumerate(business)]
tunnels=[];logs=[]
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def send(obj):print(json.dumps(obj),flush=True)
def log_tail():
    return '\n'.join(name+':\n'+(root/(name+'.ssh.log')).read_text()[-1500:] for name,_ in specs)

try:
    for name,forwards in specs:
        log=(root/(name+'.ssh.log')).open('ab');logs.append(log)
        args=['ssh','-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes',
              '-o','ControlMaster=no','-o','ControlPath=none','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=3']
        for local,remote in forwards:args+=['-L',f'127.0.0.1:{local}:127.0.0.1:{remote}']
        tunnels.append(subprocess.Popen(args+['tx'],stdout=subprocess.DEVNULL,stderr=log))
    for attempt in range(200):
        if any(p.poll() is not None for p in tunnels):raise RuntimeError('SSH stream failed: '+log_tail())
        ready=True
        for port in ports:
            try:
                with socket.create_connection(('127.0.0.1',port),timeout=.1):pass
            except OSError:ready=False;break
        if ready:break
        time.sleep(.1)
    else:raise RuntimeError('SSH local listeners did not become ready')
    if awake.poll() is not None:raise RuntimeError('Mac idle-sleep inhibitor exited')
    dsn=secret['dsn'].replace('127.0.0.1:13306',f'127.0.0.1:{mysql}')
    (root/'bypass.dsn').write_text(dsn);(root/'bypass.dsn').chmod(0o600)
    send({'ready':True,'host':socket.gethostname(),'platform':platform.platform(),'cpus':os.cpu_count(),
          'nofile':resource.getrlimit(resource.RLIMIT_NOFILE),'machine':platform.machine(),'worker_pid':os.getpid(),
          'idle_sleep_inhibited':True,'business_streams':4,'control_stream_separate':True,
          'max_inflight':1536,'http_connections_per_stream':256,'bypass_pool_limit':64})
    for line in sys.stdin:
        c=json.loads(line)
        if c['op']=='bye':break
        if c['op']=='rtt':
            values=[]
            for _ in range(30):
                start=time.perf_counter_ns()
                with opener.open(f'http://127.0.0.1:{admin}/debug/strategy',timeout=3) as r:r.read()
                values.append((time.perf_counter_ns()-start)/1000)
            send({'round_trip_us':values});continue
        stem=c['stem']
        if not stem.replace('-','').replace('_','').isalnum():raise ValueError('invalid run name')
        env={**os.environ,'LOADGEN_AUTHORIZATION':secret['authorization']}
        targets=','.join(f'http://127.0.0.1:{port}' for port in business)
        argv=[str(root/'loadgen'),'s1','--target',f'http://127.0.0.1:{business[0]}','--targets',targets,
              '--admin',f'http://127.0.0.1:{admin}','--popularity',str(root/'popularity.u32'),
              '--read-ratio',str(c['ratio']),'--rate',str(c['rate']),'--warmup',f"{c['warmup']}s",
              '--window',f"{c['window']}s",'--seed',str(c['seed']),'--prime-connections','128',
              '--max-inflight','1536','--connections-per-target','256','--timeout',c.get('timeout','3s'),'--story',c['story'],
              '--arm',c['arm'],'--audit-run',stem,'--repeat',str(c['repeat']),'--data-version',c['data_version'],
              '--output',str(root/(stem+'.json')),'--raw',str(root/(stem+'.csv.gz'))]
        if c.get('bypass'):argv+=['--bypass-dsn-file',str(root/'bypass.dsn')]
        r=subprocess.run(argv,env=env,capture_output=True,text=True,timeout=c['warmup']+c['window']+60)
        send({'exit_code':r.returncode,'stdout':r.stdout,'stderr':r.stderr,
              'tunnel_alive':all(p.poll() is None for p in tunnels),'tunnel_log_tail':log_tail()})
finally:
    for p in tunnels:
        p.terminate()
        try:p.communicate(timeout=10)
        except subprocess.TimeoutExpired:p.kill();p.communicate()
    for log in logs:log.close()
    awake.terminate();awake.wait(timeout=5)
    for p in (root/'private.json',root/'bypass.dsn'):p.unlink(missing_ok=True)
