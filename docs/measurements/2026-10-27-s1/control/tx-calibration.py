#!/usr/bin/env python3
"""Separate tx loopback calibration; never aggregate with the Mac primary results."""
import fcntl,hashlib,importlib.util,json,math,os,subprocess,time,urllib.request
from pathlib import Path
ROOT=Path.cwd();STATE=ROOT/'.local/s1-suite';OUT=STATE/'tx-calibration-2026-10-27'
COMMIT='d2ca677db32a17eaed9aff776244262b88d42801'
spec=importlib.util.spec_from_file_location('suite',ROOT/'scripts/s1-suite.py');c=importlib.util.module_from_spec(spec);spec.loader.exec_module(c)
lock=(STATE/'controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
subprocess.run(['git','diff','--exit-code',COMMIT,'--','loadgen','experiments','scripts','support','deploy','datasets'],check=True)
provenance=json.loads((STATE/'provenance.json').read_text());assert provenance['source_commit']==COMMIT
for path,want in provenance['binaries'].items():assert hashlib.sha256((ROOT/path).read_bytes()).hexdigest()==want
assert 3 in os.sched_getaffinity(0)
manifest=json.loads((ROOT/'datasets/stats/catalog.json').read_text());design=json.loads((STATE/'design.json').read_text())
assert design['source_commit']==COMMIT
c.EVIDENCE_ROOT=Path(design['evidence_root'])
OUT.mkdir(parents=True,exist_ok=True)
binary=STATE/'loadgen-tx'
subprocess.run(['go','-C','loadgen','build','-trimpath','-ldflags','-X main.sourceCommit='+COMMIT,'-o',str(binary),'./cmd/loadgen'],check=True,env={**os.environ,'CGO_ENABLED':'0','GOOS':'linux','GOARCH':'amd64'})
secret=json.loads((ROOT/'.local/deploy/client-env.json').read_text())
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
class Local:
    def ask(self,p):
        if p['op']=='rtt':
            values=[]
            for _ in range(30):
                start=time.perf_counter_ns()
                with opener.open('http://127.0.0.1:19096/debug/strategy',timeout=3) as response:response.read()
                values.append((time.perf_counter_ns()-start)/1000)
            return {'round_trip_us':values,'transport':'tx-loopback'}
        stem=p['stem']
        args=['taskset','-c','3',str(binary),'s1','--targets','http://127.0.0.1:18096',
              '--admin','http://127.0.0.1:19096','--popularity',str(STATE/'popularity.u32'),
              '--read-ratio',str(p['ratio']),'--rate',str(p['rate']),'--warmup',str(p['warmup'])+'s',
              '--window',str(p['window'])+'s','--seed',str(p['seed']),'--prime-connections','128',
              '--max-inflight','1536','--connections-per-target','1024','--timeout','3s',
              '--story',p['story'],'--arm',p['arm'],'--repeat',str(p['repeat']),'--data-version',p['data_version'],
              '--output',str(OUT/(stem+'.json')),'--raw',str(OUT/(stem+'.csv.gz'))]
        result=subprocess.run(args,capture_output=True,text=True,timeout=p['warmup']+p['window']+60,
                              env={**os.environ,'LOADGEN_AUTHORIZATION':secret['LOADGEN_AUTHORIZATION']})
        return {'exit_code':result.returncode,'stdout':result.stdout,'stderr':result.stderr,'transport':'tx-loopback','ssh_in_use':False}
    def copy(self,stem,out):
        assert out==OUT
        for suffix in ['.json','.csv.gz']:assert (out/(stem+suffix)).is_file()

c.save(STATE/'tx-calibration-environment.json',dict(source_commit=COMMIT,client_cpu_affinity=[3],ssh_in_use=False,
    business_urls=['http://127.0.0.1:18096'],max_inflight=1536,connections_per_target=1024,
    binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),scope='Separate diagnostic only; client hardware, affinity and transport differ from Mac.'))
c.start_redis();c.stop_e1();c.restore();client=Local()
try:
    for repeat in range(1,6):
        order=['A','B','C','D'] if repeat%2 else ['D','C','B','A']
        for arm in order:
            c.one(client,COMMIT,manifest,OUT,arm,.95,design['rates'][-1],repeat,
                  max(20,math.ceil(12000/(design['rates'][-1]*.95))),10,'S1-tx-calibration')
    c.event('tx-calibration-complete')
finally:c.stop_e1()
