#!/usr/bin/env python3
"""S1 single-branch supplement: B-gate-only and B-write-only, five rounds each.

This controller is implemented for stage 1 of
.agent-tasks/2026-11-03-s1-single-branch-and-p3.md. It must not be executed for
the formal measurement before Codex validates the code and freezes the source.

The two arms reuse the frozen S1 machinery: same Mac four-stream topology,
private Redis, isolated bcl_s1 source, 50 ms read-refill delay, 95:5 reads at
400 req/s, 10 s warmup and 32 s window. Only the E1 cache-branch switches differ:

  B-gate-only   cache-aside reads, gated refills, delete on write
  B-write-only  versioned reads, plain refills, plain SET on write (= S1 B-off)
"""
import argparse,fcntl,gzip,hashlib,importlib.util,json,os,shutil,signal,subprocess,time,urllib.request
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
STATE=ROOT/'.local/s1-single'
STORY='S1-v2-single-branch'
RATIO=.95;RATE=400;WARMUP=10;WINDOW=32;DELAY='50ms';REPEATS=5;SEED_BASE=20261026
DATA_VERSION='catalog/b0e5aba50106/seed-20261023/8025233af339'
ARMS=('B-gate-only','B-write-only')
CONFIG={
 'B-gate-only':dict(mode='cache-aside',gated_aside=True,ungated_fills=False,read_fill_gated=True,write_path='delete'),
 'B-write-only':dict(mode='versioned',gated_aside=False,ungated_fills=True,read_fill_gated=False,write_path='plain-fill'),
}

spec=importlib.util.spec_from_file_location('legacy',ROOT/'scripts/s1-suite.py')
legacy=importlib.util.module_from_spec(spec);spec.loader.exec_module(legacy)
from audit_truth import calculate
legacy.STATE=STATE


def save_new(path,obj):
 path=Path(path);path.parent.mkdir(parents=True,exist_ok=True)
 if path.exists():raise RuntimeError('refuse to overwrite evidence: '+str(path))
 data=json.dumps(obj,sort_keys=True,separators=(',',':')).encode()
 path.write_bytes(gzip.compress(data,mtime=0) if path.suffix=='.gz' else data)
def save(path,obj):
 path=Path(path);path.parent.mkdir(parents=True,exist_ok=True)
 path.write_text(json.dumps(obj,indent=2,sort_keys=True)+'\n')
def event(kind,**fields):
 row=dict(at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),event=kind,**fields)
 with (STATE/'events.jsonl').open('a') as f:f.write(json.dumps(row,sort_keys=True)+'\n')
 print(json.dumps(row,sort_keys=True),flush=True)


def stem(arm,repeat):return f'{STORY}-{arm}-p{int(RATIO*100)}-q{RATE}-r{repeat}'
def arm_order(repeat):return ARMS if repeat%2 else tuple(reversed(ARMS))
def fingerprint():
 text=legacy.sql('SELECT COUNT(*),MAX(id),SUM(price_cent),SUM(stock),MIN(version),MAX(version) FROM bcl_s1.items;')
 return [int(x) for x in text.split('\t')]
def check_baseline(label):
 want=json.loads((ROOT/'.local/s1-suite/prepared.json').read_text())['source_fingerprint']
 got=fingerprint()
 if got!=want:raise RuntimeError(f'{label}: bcl_s1 fingerprint {got} != {want}')
 return got


def build(commit,manifest):
 legacy.build(commit,manifest)
 path=STATE/'provenance.json';prov=json.loads(path.read_text())
 for name in ['loadgen','summary']:
  binary=STATE/name
  prov['binaries'][str(binary.relative_to(ROOT))]=hashlib.sha256(binary.read_bytes()).hexdigest()
 save(path,prov)
def require_provenance(commit):
 path=STATE/'provenance.json'
 if not path.exists():raise RuntimeError('missing binary provenance; build first')
 prov=json.loads(path.read_text())
 if prov.get('source_commit')!=commit:raise RuntimeError('provenance belongs to another source commit')
 for rel,digest in prov['binaries'].items():
  binary=ROOT/rel
  if not binary.exists() or hashlib.sha256(binary.read_bytes()).hexdigest()!=digest:
   raise RuntimeError('binary hash mismatch: '+rel)
 image_id=subprocess.check_output(['docker','image','inspect','--format','{{.Id}}','bcl-s1-e1'],text=True).strip()
 revision=subprocess.check_output(['docker','image','inspect','--format','{{index .Config.Labels "org.opencontainers.image.revision"}}','bcl-s1-e1'],text=True).strip()
 if image_id!=prov['image_id'] or revision!=commit:
  raise RuntimeError('E1 image does not match provenance')
 return prov


def round_trace_dir(out,name,stamp=None):
 return STATE/'traces'/Path(out).name/(name+'-'+str(stamp if stamp is not None else time.time_ns()))
def service_args(arm,name,trace,envfile,hot):
 cfg=CONFIG[arm]
 return ['docker','run','-d','--name','bcl-s1-e1','--label',legacy.LABEL,'--network','bcl-p0_default','--cpuset-cpus','2','--memory','192m','--env-file',str(envfile),'-e','GUIDE_SOURCE=mysql','-e','GUIDE_ADDRESS=0.0.0.0:8086','-e','ADMIN_ADDRESS=0.0.0.0:9086','-e','REDIS_ADDR=bcl-s1-redis:6379','-e','CACHE_READ_MODE='+cfg['mode'],'-e','GUIDE_GATED_ASIDE='+str(int(cfg['gated_aside'])),'-e','GUIDE_UNGATED_FILLS='+str(int(cfg['ungated_fills'])),'-e','GUIDE_FILL_DELAY='+DELAY,'-e','GUIDE_AUDIT_FILE=/trace/audit.jsonl','-e','GUIDE_INSTANCE='+name,'-e',f'GUIDE_TRACE_ID={hot}','-e','GUIDE_TRACE_FILE=/trace/events.jsonl','-v',str(trace)+':/trace','-p','127.0.0.1:18096:8086','-p','127.0.0.1:19096:9086','bcl-s1-e1']
def start(arm,name,trace_dir):
 cfg=CONFIG[arm];trace=Path(trace_dir);trace.mkdir(parents=True,exist_ok=True);trace.chmod(0o777)
 envfile=STATE/'service.env';envfile.write_text((ROOT/'.local/deploy/e1-s1.env').read_text().replace('/bcl?','/bcl_s1?'));envfile.chmod(0o600)
 hot=int.from_bytes((STATE/'popularity.u32').read_bytes()[:4],'little')
 legacy.command(['docker','exec','bcl-s1-redis','redis-cli','FLUSHDB'],capture_output=True)
 args=service_args(arm,name,trace,envfile,hot)
 legacy.command(args,capture_output=True)
 for _ in range(100):
  try:
   with urllib.request.urlopen('http://127.0.0.1:19096/debug/strategy',timeout=1) as response:d=json.load(response)
   with urllib.request.urlopen('http://127.0.0.1:19096/debug/audit',timeout=1) as response:a=json.load(response)
   if (d['mode']==cfg['mode'] and d['source']=='mysql' and d['ungated_fills']==cfg['ungated_fills'] and
       d['gated_aside']==cfg['gated_aside'] and d['read_fill_gated']==cfg['read_fill_gated'] and
       d['write_path']==cfg['write_path'] and d['fill_delay']==DELAY and d['ttl']=='1m' and a['all_keys'] and not a['failed']):
    with urllib.request.urlopen('http://127.0.0.1:18096/items/'+str(hot),timeout=2) as response:item=json.load(response)['item']
    if item['version']!=1:raise RuntimeError('source baseline not restored')
    return
  except (OSError,ValueError):time.sleep(.1)
 raise RuntimeError('single-branch service not ready')


def archive_trace(trace,target):
 legacy.archive_trace(trace,target)
 # The service owns its audit file mode0600; read through root without widening it.
 raw=subprocess.check_output(['sudo','-n','cat',str(Path(trace)/'audit.jsonl')])
 destination=Path(target).with_name(Path(target).name.replace('.trace.jsonl.gz','.audit.jsonl.gz'))
 if destination.exists():raise RuntimeError('audit archive already exists')
 destination.write_bytes(gzip.compress(raw,mtime=0))


class Mac(legacy.Mac):
 def ask(self,c):
  if c.get('op')=='run':c=dict(c,timeout='10s')
  return super().ask(c)


def path_counters(counters):
 return dict(sets=counters.get('bcl_e1_cache_sets_total',0),deletes=counters.get('bcl_e1_cache_deletes_total',0),
             fills_applied=counters.get('bcl_e1_cache_fills_applied_total',0),fills_rejected=counters.get('bcl_e1_cache_fills_rejected_total',0),
             full_reads=counters.get('bcl_e1_source_full_reads_total',0),writes=counters.get('bcl_e1_source_writes_total',0))
def path_ok(arm,counters):
 c=path_counters(counters)
 if c['writes']<=0:return False
 if arm=='B-gate-only':return c['deletes']==c['writes'] and c['sets']==0
 return c['deletes']==0 and c['sets']>0 and c['fills_applied']==0 and c['fills_rejected']==0


def one(client,commit,manifest,out,arm,repeat):
 name=stem(arm,repeat)
 done=out/(name+'.done.json')
 if done.exists():
  prior=json.loads(done.read_text())
  if prior['source_commit']!=commit:raise RuntimeError('completed output belongs to another source commit')
  if not prior['valid']:raise RuntimeError('previous invalid round is preserved; inspect before resuming')
  return prior['valid']
 partial=list(out.glob(name+'.*'))
 if partial:
  archive=out/'incomplete'/str(time.time_ns());archive.mkdir(parents=True)
  for f in partial:shutil.move(str(f),archive/f.name)
 legacy.stop_e1()
 trace_dir=round_trace_dir(out,name)
 start(arm,name,trace_dir)
 event('run-start',stem=name,arm=arm,repeat=repeat,window=WINDOW,warmup=WARMUP,fill_delay=DELAY)
 rtt=client.ask({'op':'rtt'});save(out/(name+'.network.json'),rtt)
 req=dict(op='run',stem=name,ratio=RATIO,rate=RATE,warmup=WARMUP,window=WINDOW,seed=SEED_BASE+repeat,
          arm=arm,repeat=repeat,data_version=manifest['data_version'],story=STORY,bypass=False)
 response=client.ask(req)
 save(out/(name+'.worker.json'),response)
 if response['exit_code']:
  save(out/(name+'.worker-error.json'),response)
  raise RuntimeError('Mac loadgen failed; see private worker-error diagnostics')
 client.copy(name,out)
 doc=json.loads((out/(name+'.json')).read_text())
 if doc['git_commit']!=commit:raise RuntimeError('client source version mismatch')
 if doc['arm']!=arm or doc['story']!=STORY or doc['repeat']!=repeat:raise RuntimeError('client metadata mismatch')
 expected={'warmup':'10s','window':'32s','timeout':'10s','max_inflight':1536,'connections_per_target':256,'prime_connections':128,'seed':SEED_BASE+repeat,'bypass_writes':False}
 if doc['config']['read_ratio']!=RATIO or doc['config']['rate']!=RATE or any(doc['config'].get(k)!=v for k,v in expected.items()):
  raise RuntimeError('client run parameters differ from the frozen design')
 state=subprocess.check_output(['docker','inspect','--type','container','--format','{{json .State}}','bcl-s1-e1'],text=True)
 save(out/(name+'.container.json'),json.loads(state))
 log=legacy.command(['docker','logs','--tail','50','bcl-s1-e1'],capture_output=True,text=True)
 (out/(name+'.service.log')).write_text(log.stdout+log.stderr)
 legacy.stop_e1()  # flush trace and audit before archiving
 archive_trace(trace_dir,out/(name+'.trace.jsonl.gz'))
 correctness=legacy.verify_writes(out/(name+'.csv.gz'))
 save(out/(name+'.correctness.json'),correctness)
 truth=calculate([out/(name+'.audit.jsonl.gz')],out/(name+'.csv.gz'),name)
 counters=doc['result']['server']['counters_delta'];observed=path_counters(counters);counters_ok=path_ok(arm,counters)
 truth.update(source_commit=commit,story=STORY,arm=arm,repeat=repeat,ratio=RATIO,rate=RATE,fill_delay=DELAY,fault_injection=True,
              client_topology='Mac-four-SSH-business-streams',client_timeout='10s',client_valid=doc['result']['valid'],
              source_audit_ok=correctness['ok'],path_counters_ok=counters_ok,path_counters=observed,write_path=CONFIG[arm]['write_path'])
 save_new(out/(name+'.a2.json.gz'),truth)
 good=doc['result']['valid'] and truth['valid'] and correctness['ok'] and counters_ok
 if not correctness['ok']:
  event('correctness-gate-failed',stem=name,reason=correctness['reason'])
  raise RuntimeError('source correctness gate failed; preserve run and inspect before resuming')
 ids=legacy.dirty_ids(out/(name+'.csv.gz'))
 legacy.restore(ids)  # local per-id verification, no extra full-table scan
 registry=legacy.command(['python3','scripts/with-p0-env.py',str(STATE/'loadgen'),'register','--result',str(out/(name+'.json')),
   '--recorded-path',str(legacy.EVIDENCE_ROOT/out.name/(name+'.json.gz'))],capture_output=True,text=True).stdout.strip()
 save(done,dict(registry=registry,restored_keys=len(ids),source_commit=commit,valid=good,
                stale=truth['stale'],reads=truth['successful_formal_reads'],path_counters=observed))
 event('run-complete',stem=name,valid=good,stale=truth['stale'],reads=truth['successful_formal_reads'],
       counters=observed,restored_keys=len(ids))
 if not good:raise RuntimeError('quality gate failed; preserve run and stop: '+name)
 return good


def main(a):
 out=Path(a.out).resolve()
 if ROOT/'.local' not in out.parents:raise SystemExit('results must stay under .local/')
 out.mkdir(parents=True,exist_ok=True);STATE.mkdir(exist_ok=True);STATE.chmod(0o700)
 lock=(ROOT/'.local/s1-suite/controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 p2lock=(ROOT/'.local/p2-suite/controller.lock').open('w');fcntl.flock(p2lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 commit=legacy.clean_commit();manifest=json.loads((ROOT/'datasets/stats/catalog.json').read_text())
 if manifest['data_version']!=DATA_VERSION:raise RuntimeError('catalog data version changed')
 prep=json.loads((ROOT/'.local/s1-suite/prepared.json').read_text())
 if prep['data_version']!=DATA_VERSION:raise RuntimeError('prepared dataset mismatch')
 legacy.EVIDENCE_ROOT=Path(a.recorded_root)
 prov=STATE/'provenance.json'
 if not prov.exists() or json.loads(prov.read_text()).get('source_commit')!=commit:build(commit,manifest)
 provenance=require_provenance(commit)
 if provenance.get('data_version')!=DATA_VERSION:raise RuntimeError('provenance data version mismatch')
 legacy.start_redis();legacy.stop_e1();legacy.restore()
 design=dict(source_commit=commit,story=STORY,arms=list(ARMS),ratio=RATIO,rate=RATE,warmup_seconds=WARMUP,
      window_seconds=WINDOW,fill_delay=DELAY,repeats=REPEATS,seed_rule='20261026+repeat',data_version=DATA_VERSION,
      image_id=provenance['image_id'],binaries=provenance['binaries'],
      mode='sequential single-branch supplement; existing A/B/B-off/C/CV/D groups are read-only references')
 design_path=STATE/'design.json'
 if design_path.exists() and json.loads(design_path.read_text())!=design:
  raise RuntimeError('existing design differs; move the previous state before resuming')
 save(design_path,design)
 start_fingerprint=check_baseline('start')
 save(STATE/'fingerprint-start.json',dict(fingerprint=start_fingerprint,at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
 event('baseline-start',commit=commit)
 client=None
 try:
  client=Mac(commit)
  for repeat in range(1,REPEATS+1):
   for arm in arm_order(repeat):
    try:one(client,commit,manifest,out,arm,repeat)
    except Exception as e:
     save_new(STATE/'failures'/f'{stem(arm,repeat)}-{time.time_ns()}.json',dict(error=type(e).__name__,detail=str(e),arm=arm,repeat=repeat,at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
     raise
 finally:
  legacy.stop_e1()
  try:
   if client is not None:client.close()
  finally:
   legacy.restore()
   save(STATE/'fingerprint-end.json',dict(fingerprint=check_baseline('end'),at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
   event('baseline-end')
 save(STATE/'measurement-finished.json',dict(valid=True,source_commit=commit,rounds=len(ARMS)*REPEATS))


if __name__=='__main__':
 def interrupt(signum,frame):raise KeyboardInterrupt
 signal.signal(signal.SIGTERM,interrupt)
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--out',type=Path,default=ROOT/'.local/s1-single/results')
 p.add_argument('--recorded-root',default='docs/measurements/2026-11-03-s1-single-branch')
 main(p.parse_args())
