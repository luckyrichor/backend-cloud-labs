#!/usr/bin/env python3
"""Reproducible S1 controller. Owns only bcl_s1 DB and labelled bcl-s1-* containers.

Run --prepare once, then --phase probe, then --phase matrix. Runtime state and
credentials stay in ignored .local/. The worker keeps Mac's SSH tunnel alive.
"""
import argparse
import collections
import csv
import datetime
import fcntl
import gzip
import hashlib
import io
import json
import math
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import time
import threading
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
os.chdir(ROOT)
STATE = ROOT/'.local/s1-suite'
LABEL = 'bcl.experiment=s1'
DB = 'bcl_s1'
ARMS = {'A':('cache-aside',False),'B':('versioned',False),'C':('strict',False),
        'D':('cache-first',False),'B-off':('versioned',True)}


def command(argv, **kw):
    return subprocess.run(argv,check=True,**kw)


def sql(query):
    # Root password stays inside the existing MySQL container, never in argv or output.
    r = command(['docker','exec','-i','bcl-p0-mysql-1','sh','-c',
                 'export MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"; exec mysql -uroot -N -B'],
                input=query,text=True,capture_output=True)
    return r.stdout.strip()


def clean_commit():
    if subprocess.check_output(['git','status','--porcelain']).strip():
        raise SystemExit('clean worktree required')
    return subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()


def save(path,obj):
    path.parent.mkdir(parents=True,exist_ok=True)
    tmp = path.with_suffix(path.suffix+'.tmp')
    tmp.write_text(json.dumps(obj,indent=2)+'\n')
    tmp.replace(path)


def event(kind,**fields):
    row = dict(at=datetime.datetime.now(datetime.timezone.utc).isoformat(),event=kind,**fields)
    with (STATE/'events.jsonl').open('a') as f:
        f.write(json.dumps(row)+'\n')
    print(json.dumps(row),flush=True)


def owned_container(name):
    p = subprocess.run(['docker','inspect','--type','container','--format','{{index .Config.Labels "bcl.experiment"}}',name],capture_output=True,text=True)
    if p.returncode:
        return False
    if p.stdout.strip() != 's1':
        raise RuntimeError('container name belongs to another workload')
    return True


def stop_e1():
    if owned_container('bcl-s1-e1'):
        command(['docker','stop','--time','10','bcl-s1-e1'],capture_output=True)
        command(['docker','rm','bcl-s1-e1'],capture_output=True)


def prepare(manifest):
    if sql("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='bcl_s1';"):
        raise SystemExit('bcl_s1 exists; refusing to overwrite; use existing prepared state')
    expected = manifest['stats']['items']
    want = [2000000,2000000,expected['sum_price_cent'],expected['sum_stock'],1,1]
    got = [int(x) for x in sql('SELECT COUNT(*),MAX(id),SUM(price_cent),SUM(stock),MIN(version),MAX(version) FROM bcl.items;').split('\t')]
    # Shared table may contain prior development writes. Preserve it and import
    # the immutable P1 catalog rather than silently treating that table as pristine.
    event('prepare-start',shared_fingerprint=got,expected_fingerprint=want)
    sql("CREATE DATABASE bcl_s1; GRANT ALL ON bcl_s1.* TO 'bcl'@'%'; "
        'CREATE TABLE bcl_s1.items LIKE bcl.items;')
    client=json.loads((ROOT/'.local/deploy/client-env.json').read_text())
    dsn=client['LOADGEN_MYSQL_DSN'].replace('/bcl?','/bcl_s1?')
    command(['go','-C','tools/datapipe','run','./cmd/itemload',
             '--catalog','/home/ubuntu/datasets/backend-cloud-labs/p1/catalog',
             '--output',str(STATE/'import.json')],env={**os.environ,'LOADGEN_MYSQL_DSN':dsn})
    sql('CREATE TABLE bcl_s1.items_base LIKE bcl_s1.items; '
        'INSERT INTO bcl_s1.items_base SELECT * FROM bcl_s1.items;')
    actual=[int(x) for x in sql('SELECT COUNT(*),MAX(id),SUM(price_cent),SUM(stock),MIN(version),MAX(version) FROM bcl_s1.items;').split('\t')]
    if actual!=want:raise RuntimeError('isolated baseline fingerprint mismatch')
    save(STATE/'prepared.json',dict(data_version=manifest['data_version'],source_fingerprint=want,shared_fingerprint_before=got,
                                   database=DB,created_at=datetime.datetime.now(datetime.timezone.utc).isoformat()))
    event('prepare-complete',rows=2000000)


def restore(ids=None):
    columns = ['title','category_id','price_cent','stock','version','updated_at']
    sets = ','.join('a.'+k+'=b.'+k for k in columns)
    if ids is None:
        sql('UPDATE bcl_s1.items a JOIN bcl_s1.items_base b USING(id) SET '+sets+' WHERE a.version<>b.version;')
        ids = []
    for offset in range(0,len(ids),500):
        chunk = ','.join(str(int(x)) for x in ids[offset:offset+500])
        sql('UPDATE bcl_s1.items a JOIN bcl_s1.items_base b USING(id) SET '+sets+' WHERE a.id IN ('+chunk+');')
        differs = ' OR '.join('NOT(a.'+k+'<=>b.'+k+')' for k in columns)
        n = sql('SELECT COUNT(*) FROM bcl_s1.items a JOIN bcl_s1.items_base b USING(id) WHERE a.id IN ('+chunk+') AND ('+differs+');')
        if n != '0':
            raise RuntimeError('restored rows differ from baseline')


class Mac:
    def __init__(self,commit):
        self.host = 'luomac'
        self.ssh = ['ssh','-o','BatchMode=yes','-o','ConnectTimeout=10',self.host]
        identity = subprocess.check_output(self.ssh+['uname -s; uname -m'],text=True).splitlines()
        if identity != ['Darwin','arm64']:
            raise RuntimeError('expected the configured real Darwin arm64 Mac')
        self.remote = subprocess.check_output(self.ssh+['mktemp -d /tmp/bcl-s1-suite.XXXXXX'],text=True).strip()
        client = json.loads((ROOT/'.local/deploy/client-env.json').read_text())
        private = STATE/'private-client.json'
        save(private,dict(authorization=client['LOADGEN_AUTHORIZATION'],dsn=client['LOADGEN_MYSQL_DSN'].replace('/bcl?','/bcl_s1?')))
        private.chmod(0o600)
        for local,remote in [(STATE/'loadgen-mac','loadgen'),(STATE/'popularity.u32','popularity.u32'),
                             (private,'private.json'),(ROOT/'scripts/s1/mac-worker.py','worker.py')]:
            command(['scp','-q',str(local),f'{self.host}:{self.remote}/{remote}'])
        self.proc = subprocess.Popen(self.ssh+[shlex.join(['python3','-u',self.remote+'/worker.py',self.remote])],
                                     stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,bufsize=1)
        self.environment = self.read()
        if not self.environment.get('ready'):
            raise RuntimeError('Mac worker failed readiness')
        save(STATE/'mac-environment.json',dict(**self.environment,source_commit=commit))
    def read(self):
        text = self.proc.stdout.readline()
        if not text:
            raise RuntimeError('Mac worker disconnected; preserve partial run, resume after restoring baseline')
        return json.loads(text)
    def ask(self,obj):
        self.proc.stdin.write(json.dumps(obj)+'\n');self.proc.stdin.flush()
        return self.read()
    def copy(self,stem,out):
        for suffix in ['.json','.csv.gz']:
            command(['scp','-q',f'{self.host}:{self.remote}/{stem}{suffix}',str(out/(stem+suffix))])
    def close(self):
        try:
            self.proc.stdin.write('{"op":"bye"}\n');self.proc.stdin.flush()
            self.proc.stdin.close()
            self.proc.wait(timeout=15)
        except (BrokenPipeError,subprocess.TimeoutExpired):
            self.proc.terminate();self.proc.wait(timeout=10)
        (STATE/'private-client.json').unlink(missing_ok=True)


def build(commit,manifest):
    p = '/home/ubuntu/datasets/backend-cloud-labs/p1/catalog/popularity.u32'
    expect = next(x['sha256'] for x in manifest['artifacts'] if x['path']=='popularity.u32')
    if hashlib.sha256(Path(p).read_bytes()).hexdigest() != expect:
        raise RuntimeError('popularity hash mismatch')
    shutil.copyfile(p,STATE/'popularity.u32')
    env = {**os.environ,'CGO_ENABLED':'0'}
    command(['go','-C','loadgen','build','-trimpath','-ldflags',f'-X main.sourceCommit={commit}',
             '-o',str(STATE/'loadgen-mac'),'./cmd/loadgen'],env={**env,'GOOS':'darwin','GOARCH':'arm64'})
    command(['go','-C','loadgen','build','-trimpath','-o',str(STATE/'loadgen'),'./cmd/loadgen'],env=env)
    command(['go','-C','loadgen','build','-trimpath','-o',str(STATE/'summary'),'./cmd/s1summary'],env=env)
    command(['go','-C','experiments/e1-shopping-guide','build','-trimpath','-o',str(ROOT/'.local/bin/e1'),'./cmd/guide'],env=env)
    command(['docker','build','-q','-f','deploy/e1.Dockerfile','--build-arg',f'SOURCE_COMMIT={commit}','-t','bcl-s1-e1','.'],stdout=subprocess.DEVNULL)
    hashes = {str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [STATE/'loadgen-mac',ROOT/'.local/bin/e1']}
    save(STATE/'provenance.json',dict(source_commit=commit,binaries=hashes,data_version=manifest['data_version'],
        image_id=subprocess.check_output(['docker','image','inspect','--format','{{.Id}}','bcl-s1-e1'],text=True).strip(),
        service_revision=subprocess.check_output(['docker','image','inspect','--format','{{index .Config.Labels "org.opencontainers.image.revision"}}','bcl-s1-e1'],text=True).strip()))


def start_redis():
    if owned_container('bcl-s1-redis'):
        return
    image = subprocess.check_output(['docker','inspect','--format','{{.Config.Image}}','bcl-p0-redis-1'],text=True).strip()
    command(['docker','run','-d','--name','bcl-s1-redis','--label',LABEL,'--network','bcl-p0_default',
             '--cpuset-cpus','1','--memory','192m',image,'redis-server','--maxmemory','128mb','--maxmemory-policy','allkeys-lru'],capture_output=True)
    for _ in range(100):
        p = subprocess.run(['docker','exec','bcl-s1-redis','redis-cli','ping'],capture_output=True,text=True)
        if p.stdout.strip()=='PONG':return
        time.sleep(.1)
    raise RuntimeError('experiment Redis not ready')


def start_e1(arm,stem,profile=False):
    mode,off = ARMS[arm]
    # Env file never appears in stdout/argv; only its path is exposed.
    text = (ROOT/'.local/deploy/e1-s1.env').read_text().replace('/bcl?','/bcl_s1?')
    envfile = STATE/'service.env';envfile.write_text(text);envfile.chmod(0o600)
    trace = STATE/'traces'/stem
    trace.mkdir(parents=True,exist_ok=True);trace.chmod(0o777)
    pop = (STATE/'popularity.u32').read_bytes()
    hot = int.from_bytes(pop[:4],'little')
    command(['docker','exec','bcl-s1-redis','redis-cli','FLUSHDB'],capture_output=True)
    command(['docker','run','-d','--name','bcl-s1-e1','--label',LABEL,'--network','bcl-p0_default',
             '--cpuset-cpus','2','--memory','192m','--env-file',str(envfile),
             '-e','GUIDE_SOURCE=mysql','-e','GUIDE_ADDRESS=0.0.0.0:8086','-e','ADMIN_ADDRESS=0.0.0.0:9086',
             '-e','REDIS_ADDR=bcl-s1-redis:6379','-e',f'CACHE_READ_MODE={mode}',
             '-e',f'GUIDE_UNGATED_FILLS={int(off)}','-e',f'GUIDE_PROFILE_MUTEX={int(profile)}','-e',f'GUIDE_TRACE_ID={hot}',
             '-e','GUIDE_TRACE_FILE=/trace/events.jsonl','-v',str(trace)+':/trace',
             '-p','127.0.0.1:18096:8086','-p','127.0.0.1:19096:9086','bcl-s1-e1'],capture_output=True)
    for _ in range(100):
        try:
            with urllib.request.urlopen('http://127.0.0.1:19096/debug/strategy',timeout=1) as r:d=json.load(r)
            if d['mode']==mode and d['ungated_fills']==off and d.get('source')=='mysql':
                with urllib.request.urlopen('http://127.0.0.1:18096/items/'+str(hot),timeout=2) as r:
                    item=json.load(r)['item']
                if item['version']!=1:raise RuntimeError('baseline key version is not one')
                return
            raise RuntimeError('unexpected strategy')
        except OSError:time.sleep(.1)
    raise RuntimeError('experiment E1 not ready')


def dirty_ids(raw):
    with gzip.open(raw,'rt') as f:
        return sorted({int(r['id']) for r in csv.DictReader(f) if r['write']=='1'})


def verify_writes(raw):
    # Audit source state before restoring it. A valid run has no ambiguous writes.
    by_id=collections.defaultdict(list)
    with gzip.open(raw,'rt') as f:
        for seq,r in enumerate(csv.DictReader(f)):
            if r['write']=='1' and r['sent']=='1' and r['status']=='200' and not r['err']:
                by_id[int(r['id'])].append((int(r['version']),seq))
    expected={}
    for ident,writes in by_id.items():
        versions=sorted(v for v,_ in writes)
        if versions!=list(range(2,len(writes)+2)):
            return dict(ok=False,reason='acknowledged versions not contiguous from baseline',id=ident)
        version,seq=max(writes)
        expected[ident]=(version,100+seq%100000,seq%997)
    keys=sorted(expected)
    for start in range(0,len(keys),500):
        text=sql('SELECT id,version,price_cent,stock FROM bcl_s1.items WHERE id IN ('+
                 ','.join(str(x) for x in keys[start:start+500])+');')
        found={}
        for line in text.splitlines():
            ident,version,price,stock=map(int,line.split('\t'))
            found[ident]=(version,price,stock)
        for ident in keys[start:start+500]:
            if found.get(ident)!=expected[ident]:
                return dict(ok=False,reason='source final value differs from acknowledged writes',id=ident)
    return dict(ok=True,keys=len(keys),acknowledged_writes=sum(len(x) for x in by_id.values()))


def one(mac,commit,manifest,out,arm,ratio,rate,repeat,window,warmup,story,bypass=False,profile=False):
    stem = f'{story}-{arm}-p{int(ratio*100)}-q{rate}-r{repeat}'
    completed = out/(stem+'.done.json')
    if completed.exists():
        doc=json.loads((out/(stem+'.json')).read_text())
        if doc['git_commit']!=commit:raise RuntimeError('completed output belongs to another source commit')
        return doc
    partial=list(out.glob(stem+'.*'))
    if partial:
        archive=out/'incomplete'/str(time.time_ns());archive.mkdir(parents=True)
        for f in partial:shutil.move(str(f),archive/f.name)
    stop_e1()
    # Last complete run's dirty keys are restored at its end. Full restore is done
    # at controller startup to cover a previously interrupted run.
    start_e1(arm,stem,profile)
    event('run-start',stem=stem,window=window,warmup=warmup)
    # Round-trip baseline collected before each run, not concurrently with traffic.
    rtt = mac.ask({'op':'rtt'})
    save(out/(stem+'.network.json'),rtt)
    req = dict(op='run',stem=stem,ratio=ratio,rate=rate,warmup=warmup,window=window,
               seed=20261026+repeat,arm=arm,repeat=repeat,data_version=manifest['data_version'],story=story,bypass=bypass)
    prof_error=[]
    def profile_worker():
        try:
            time.sleep(warmup+2)
            for suffix,url in [('cpu.pprof','profile?seconds=5'),('mutex.pprof','mutex'),('goroutine.pprof','goroutine')]:
                with urllib.request.urlopen('http://127.0.0.1:19096/debug/pprof/'+url,timeout=15) as r:
                    (out/(stem+'.'+suffix)).write_bytes(r.read())
        except Exception as e:
            prof_error.append(type(e).__name__)
    profiler=None
    if profile:
        profiler=threading.Thread(target=profile_worker);profiler.start()
    response = mac.ask(req)
    if profiler:
        profiler.join()
        if prof_error:raise RuntimeError('profile collection failed: '+str(prof_error))
    save(out/(stem+'.worker.json'),response)
    if response['exit_code']:
        save(out/(stem+'.worker-error.json'),response)
        raise RuntimeError('Mac loadgen failed; see private worker-error diagnostics')
    mac.copy(stem,out)
    doc = json.loads((out/(stem+'.json')).read_text())
    if doc['git_commit']!=commit:
        raise RuntimeError('client source version mismatch')
    state=subprocess.check_output(['docker','inspect','--type','container','--format','{{json .State}}','bcl-s1-e1'],text=True)
    save(out/(stem+'.container.json'),json.loads(state))
    log=command(['docker','logs','--tail','50','bcl-s1-e1'],capture_output=True,text=True)
    (out/(stem+'.service.log')).write_text(log.stdout+log.stderr)
    stop_e1()  # flush trace before archiving
    trace = STATE/'traces'/stem/'events.jsonl'
    if trace.exists():
        (out/(stem+'.trace.jsonl.gz')).write_bytes(gzip.compress(trace.read_bytes(),mtime=0))
    correctness=verify_writes(out/(stem+'.csv.gz'))
    save(out/(stem+'.correctness.json'),correctness)
    if not correctness['ok'] and doc['result']['valid']:
        # Preserve the exact client result; separate gate is authoritative for comparison.
        event('correctness-gate-failed',stem=stem,reason=correctness['reason'])
        raise RuntimeError('source correctness gate failed; preserve run and inspect before resuming')
    ids = dirty_ids(out/(stem+'.csv.gz'))
    restore(ids)
    registry = command(['python3','scripts/with-p0-env.py',str(STATE/'loadgen'),'register',
        '--result',str(out/(stem+'.json')),'--recorded-path',str(Path('docs/measurements/2026-10-26-s1')/out.name/(stem+'.json.gz'))],capture_output=True,text=True).stdout.strip()
    save(completed,dict(registry=registry,restored_keys=len(ids),source_commit=commit))
    event('run-complete',stem=stem,valid=doc['result']['valid'],stale=doc['result']['stale_reads'],
          reads=doc['result']['reads']['Count'],restored_keys=len(ids))
    return doc


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--prepare',action='store_true')
    p.add_argument('--phase',choices=['probe','matrix'],default='probe')
    p.add_argument('--output',type=Path,default=Path('.local/s1-suite/results'))
    a=p.parse_args()
    STATE.mkdir(parents=True,exist_ok=True)
    lock=(STATE/'controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    commit=clean_commit()
    manifest=json.loads(Path('datasets/stats/catalog.json').read_text())
    if a.prepare:
        prepare(manifest);return
    prep=json.loads((STATE/'prepared.json').read_text())
    if prep['data_version']!=manifest['data_version']:
        raise RuntimeError('prepared dataset mismatch')
    out=a.output.resolve();out.mkdir(parents=True,exist_ok=True)
    old=STATE/'provenance.json'
    if not old.exists() or json.loads(old.read_text())['source_commit']!=commit:
        build(commit,manifest)
    start_redis();stop_e1();restore()
    mac=Mac(commit)
    try:
        if a.phase=='probe':
            rows=[]
            for rate in [500,1500,3000]:
                doc=one(mac,commit,manifest,out,'A',.95,rate,0,10,5,'S1-probe')
                rows.append(doc)
            command([str(STATE/'summary'),str(out)],stdout=(STATE/'probe-summary.json').open('w'))
            summaries=json.loads((STATE/'probe-summary.json').read_text())
            good=[x['rate'] for x in summaries if x['valid'] and x['server_get_us']['p99_us']<=50000]
            if not good:raise RuntimeError('no baseline rate satisfies experimental service p99 SLO 50ms')
            capacity=max(good)
            rates=sorted({max(100,int(capacity*f)//50*50) for f in [.25,.5,.8]})
            if len(rates)!=3:raise RuntimeError('rate selection did not produce three tiers')
            save(STATE/'design.json',dict(source_commit=commit,rates=rates,service_p99_slo_us=50000,
                 rate_rule='25%, 50%, 80% of highest valid probed A rate within p99 SLO; floor to 50 req/s',
                 warmup_seconds=10,window_rule='max(20, ceil(12000/(rate*read_ratio))) seconds',
                 repeats=5,read_ratios=[.95,.99],truth='client-acknowledged same-id version lower bound',
                 planned_at=datetime.datetime.now(datetime.timezone.utc).isoformat()))
            event('probe-complete',rates=rates)
        else:
            design=json.loads((STATE/'design.json').read_text())
            if design['source_commit']!=commit:raise RuntimeError('design and source version differ; rerun probes')
            rates=design['rates']
            for repeat in range(1,6):
                for ratio in [.95,.99]:
                    for rate in rates:
                        order=['A','B','C','D'] if repeat%2 else ['D','C','B','A']
                        for arm in order:
                            window=max(20,math.ceil(12000/(rate*ratio)))
                            one(mac,commit,manifest,out,arm,ratio,rate,repeat,window,10,'S1')
            # Dedicated interleaved reverse and direct-write groups, five repetitions.
            for repeat in range(1,6):
                rate=rates[-1];window=max(20,math.ceil(12000/(rate*.95)))
                reverse=['A','B-off','B'] if repeat%2 else ['B','B-off','A']
                for arm in reverse:
                    one(mac,commit,manifest,out,arm,.95,rate,repeat,window,10,'S1-reverse')
                bypass=['A','B','C','D'] if repeat%2 else ['D','C','B','A']
                for arm in bypass:
                    one(mac,commit,manifest,out,arm,.95,rate,repeat,window,10,'S1-bypass',True)
            for arm in ['A','B','C']:
                one(mac,commit,manifest,out,arm,.95,rates[-1],1,20,10,'S1-profile',profile=True)
            event('matrix-complete')
    finally:
        stop_e1();mac.close()


if __name__=='__main__':
    main()
