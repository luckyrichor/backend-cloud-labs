#!/usr/bin/env python3
"""S1 supplemental round: immutable first round, all-key A.2 truth and explicit faults."""
import argparse,collections,datetime,fcntl,gzip,hashlib,importlib.util,json,math,os,shutil,subprocess,time,urllib.request,signal
from pathlib import Path
# Installed as scripts/p2/s1_v2.py after S2 source has finished its frozen run.
ROOT=Path(__file__).resolve().parents[2]
if not (ROOT/'scripts/s1-suite.py').exists():ROOT=Path.cwd()
spec=importlib.util.spec_from_file_location('legacy',ROOT/'scripts/s1-suite.py');legacy=importlib.util.module_from_spec(spec);spec.loader.exec_module(legacy)
from audit_truth import calculate
STATE=ROOT/'.local/p2-s1';legacy.STATE=STATE
legacy.ARMS['CV']=('strict-version',False)
ACTIVE_DELAY='0s';ACTIVE_STORY='';LAST_TRACE=None

def save(path,obj):
 path=Path(path);path.parent.mkdir(exist_ok=True,parents=True)
 if path.exists():raise RuntimeError('refuse to overwrite evidence')
 data=json.dumps(obj,separators=(',',':')).encode()
 path.write_bytes(gzip.compress(data,mtime=0) if path.suffix=='.gz' else data)
def start(arm,stem,profile=False,trace_dir=None):
 global LAST_TRACE
 mode,off=legacy.ARMS[arm];trace=Path(trace_dir);trace.mkdir(parents=True,exist_ok=True);trace.chmod(0o777);LAST_TRACE=trace
 envfile=STATE/'service.env';envfile.write_text((ROOT/'.local/deploy/e1-s1.env').read_text().replace('/bcl?','/bcl_s1?'));envfile.chmod(0o600)
 hot=int.from_bytes((STATE/'popularity.u32').read_bytes()[:4],'little')
 legacy.command(['docker','exec','bcl-s1-redis','redis-cli','FLUSHDB'],capture_output=True)
 args=['docker','run','-d','--name','bcl-s1-e1','--label',legacy.LABEL,'--network','bcl-p0_default','--cpuset-cpus','2','--memory','192m','--env-file',str(envfile),'-e','GUIDE_SOURCE=mysql','-e','GUIDE_ADDRESS=0.0.0.0:8086','-e','ADMIN_ADDRESS=0.0.0.0:9086','-e','REDIS_ADDR=bcl-s1-redis:6379','-e','CACHE_READ_MODE='+mode,'-e','GUIDE_UNGATED_FILLS='+str(int(off)),'-e','GUIDE_FILL_DELAY='+ACTIVE_DELAY,'-e','GUIDE_AUDIT_FILE=/trace/audit.jsonl','-e','GUIDE_INSTANCE='+stem,'-e',f'GUIDE_TRACE_ID={hot}','-e','GUIDE_TRACE_FILE=/trace/events.jsonl','-v',str(trace)+':/trace','-p','127.0.0.1:18096:8086','-p','127.0.0.1:19096:9086','bcl-s1-e1']
 legacy.command(args,capture_output=True)
 for _ in range(100):
  try:
   with urllib.request.urlopen('http://127.0.0.1:19096/debug/strategy',timeout=1) as response:d=json.load(response)
   with urllib.request.urlopen('http://127.0.0.1:19096/debug/audit',timeout=1) as response:a=json.load(response)
   if d['mode']==mode and d['source']=='mysql' and d['fill_delay']==ACTIVE_DELAY and a['all_keys'] and not a['failed']:
    with urllib.request.urlopen('http://127.0.0.1:18096/items/'+str(hot),timeout=2) as response:item=json.load(response)['item']
    if item['version']!=1:raise RuntimeError('source baseline not restored')
    return
  except (OSError,ValueError):time.sleep(.1)
 raise RuntimeError('all-key service not ready')
old_archive=legacy.archive_trace
def archive(trace,target):
 old_archive(trace,target)
 # The service intentionally owns its audit file mode0600. Read through root,
 # never broaden permissions or expose a credential file.
 raw=subprocess.check_output(['sudo','-n','cat',str(Path(trace)/'audit.jsonl')])
 destination=Path(target).with_name(Path(target).name.replace('.trace.jsonl.gz','.audit.jsonl.gz'))
 if destination.exists():raise RuntimeError('audit archive already exists')
 destination.write_bytes(gzip.compress(raw,mtime=0))
legacy.start_e1=start;legacy.archive_trace=archive
class Mac(legacy.Mac):
 def ask(self,c):
  if c.get('op')=='run':c=dict(c,timeout='10s')
  return super().ask(c)
class TX:
 def __init__(self,commit):
  self.commit=commit;self.secret=json.loads((ROOT/'.local/deploy/client-env.json').read_text());self.environment={'scope':'separate tx bypass consistency cohort','host':'tx','cpu_affinity':[3],'business_streams':1}
  self.private=STATE/'tx-bypass.dsn';self.private.write_text(self.secret['LOADGEN_MYSQL_DSN'].replace('/bcl?','/bcl_s1?'));self.private.chmod(0o600)
 def ask(self,c):
  if c['op']=='rtt':
   values=[]
   for _ in range(30):
    begin=time.perf_counter_ns()
    with urllib.request.urlopen('http://127.0.0.1:19096/debug/strategy',timeout=3) as r:r.read()
    values.append((time.perf_counter_ns()-begin)/1000)
   return {'round_trip_us':values}
  stem=c['stem'];audit_path=STATE/'tx-results'/(stem+'.bypass.audit.jsonl');audit_path.parent.mkdir(exist_ok=True)
  leftovers=list(audit_path.parent.glob(stem+'.*'))
  if leftovers:
   history=audit_path.parent/'incomplete'/(stem+'-'+str(time.time_ns()));history.mkdir(parents=True)
   for p in leftovers:shutil.move(str(p),history/p.name)
  args=['taskset','-c','3',str(STATE/'loadgen'),'s1','--repo',str(ROOT),'--target','http://127.0.0.1:18096','--admin','http://127.0.0.1:19096','--popularity',str(STATE/'popularity.u32'),'--read-ratio',str(c['ratio']),'--rate',str(c['rate']),'--warmup',str(c['warmup'])+'s','--window',str(c['window'])+'s','--seed',str(c['seed']),'--max-inflight','1536','--connections-per-target','1024','--prime-connections','128','--timeout','3s','--story',c['story'],'--arm',c['arm'],'--repeat',str(c['repeat']),'--audit-run',stem,'--data-version',c['data_version'],'--output',str(STATE/'tx-results'/(stem+'.json')),'--raw',str(STATE/'tx-results'/(stem+'.csv.gz')),'--bypass-dsn-file',str(self.private),'--bypass-audit',str(audit_path)]
  result=subprocess.run(args,env=os.environ|{'LOADGEN_AUTHORIZATION':self.secret['LOADGEN_AUTHORIZATION']},capture_output=True,text=True,timeout=c['warmup']+c['window']+60)
  return {'exit_code':result.returncode,'stdout':result.stdout,'stderr':result.stderr,'tunnel_alive':True,'scope':'tx loopback bypass, not Mac primary latency'}
 def copy(self,stem,out):
  for suffix in ['.json','.csv.gz']:shutil.copyfile(STATE/'tx-results'/(stem+suffix),out/(stem+suffix))
  p=STATE/'tx-results'/(stem+'.bypass.audit.jsonl');(out/(p.name+'.gz')).write_bytes(gzip.compress(p.read_bytes(),mtime=0))
 def close(self):self.private.unlink(missing_ok=True)
def one(client,commit,manifest,out,arm,ratio,rate,repeat,story,delay='0s',bypass=False):
 global ACTIVE_DELAY,ACTIVE_STORY
 ACTIVE_DELAY=delay;ACTIVE_STORY=story
 window=max(20,math.ceil(12000/(rate*ratio)))
 doc=legacy.one(client,commit,manifest,out,arm,ratio,rate,repeat,window,10,story,bypass)
 stem=f'{story}-{arm}-p{int(ratio*100)}-q{rate}-r{repeat}'
 path=out/(stem+'.a2.json.gz')
 if path.exists():truth=json.loads(gzip.decompress(path.read_bytes()))
 else:
  audits=[out/(stem+'.audit.jsonl.gz')]
  if bypass:audits.append(out/(stem+'.bypass.audit.jsonl.gz'))
  truth=calculate(audits,out/(stem+'.csv.gz'),stem);truth.update(source_commit=commit,story=story,arm=arm,repeat=repeat,ratio=ratio,rate=rate,fill_delay=delay,fault_injection=delay!='0s',client_topology='tx-loopback' if bypass else 'Mac-four-SSH-business-streams',client_timeout='3s' if bypass else '10s',client_valid=doc['result']['valid'],source_audit_ok=json.loads((out/(stem+'.correctness.json')).read_text())['ok'])
  save(path,truth)
 good=doc['result']['valid'] and truth['valid'] and truth['source_audit_ok']
 print(json.dumps({'event':'a2-complete','story':story,'arm':arm,'ratio':ratio,'rate':rate,'repeat':repeat,'valid':good,'stale':truth['stale'],'reads':truth['successful_formal_reads']}),flush=True)
 return good

def group(client,commit,manifest,out,arms,ratio,rate,story,delay='0s',bypass=False):
 valid={arm:set() for arm in arms}
 for repeat in range(1,21):
  order=arms if repeat%2 else list(reversed(arms))
  for arm in order:
   if one(client,commit,manifest,out,arm,ratio,rate,repeat,story,delay,bypass):valid[arm].add(repeat)
  common=set.intersection(*valid.values())
  if repeat>=5 and len(common)>=5:return sorted(common)
 raise RuntimeError('20 whole-group attempts did not supply five common valid repeats')
def main(a):
 STATE.mkdir(exist_ok=True,parents=True);STATE.chmod(0o700)
 lock=(ROOT/'.local/s1-suite/controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 p2lock=(ROOT/'.local/p2-suite/controller.lock').open('w');fcntl.flock(p2lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 commit=legacy.clean_commit();manifest=json.loads((ROOT/'datasets/stats/catalog.json').read_text());legacy.EVIDENCE_ROOT=Path('docs/measurements/2026-10-28-s1-v2')
 out=Path(a.out).resolve();out.mkdir(exist_ok=True,parents=True)
 prep=json.loads((ROOT/'.local/s1-suite/prepared.json').read_text());assert prep['data_version']==manifest['data_version']
 shutil.copyfile(ROOT/'.local/s1-suite/prepared.json',STATE/'prepared.json')
 if not(STATE/'provenance.json').exists() or json.loads((STATE/'provenance.json').read_text())['source_commit']!=commit:legacy.build(commit,manifest)
 legacy.start_redis();legacy.stop_e1();legacy.restore()
 client=TX(commit) if a.phase=='bypass' else Mac(commit)
 try:
  if a.phase!='probe':
   qualified=json.loads((STATE/'probe-qualified.json').read_text());assert qualified['source_commit']==commit,'probe source mismatch'
  if a.phase=='probe':
   for repeat in range(10):
    good=[one(client,commit,manifest,out,arm,.95,500,repeat,'S1-v2-probe') for arm in ['A','C','CV']]
    summary=json.loads(subprocess.check_output([str(STATE/'summary'),str(out)],text=True))
    baseline=next(row for row in summary if row['story']=='S1-v2-probe' and row['arm']=='A' and row['repeat']==repeat)
    if all(good) and baseline['server_get_us']['p99_us']<=50000:
     path=STATE/'probe-qualified.json'
     if path.exists():path.rename(STATE/('probe-qualified-history-'+str(time.time_ns())+'.json'))
     save(path,{'source_commit':commit,'baseline_probe_rate':500,'baseline_get_p99_us':baseline['server_get_us']['p99_us'],'rates':[100,250,400],'rate_rule':'retain first-round tiers, reconfirm A500 within experimental 50ms SLO; not a new saturation estimate','repeat':repeat});break
   else:raise RuntimeError('no valid A/C/CV probe within A SLO')
  elif a.phase=='natural':
   qualified=json.loads((STATE/'probe-qualified.json').read_text());assert qualified['source_commit']==commit
   for ratio in [.95,.99]:
    for rate in [100,250,400]:group(client,commit,manifest,out,['A','B','C','CV','D'],ratio,rate,'S1-v2')
  elif a.phase=='race':group(client,commit,manifest,out,['A','B','B-off','C','CV','D'],.95,400,'S1-v2-fault-fill','50ms')
  elif a.phase=='bypass':group(client,commit,manifest,out,['A','B','C','CV','D'],.95,400,'S1-v2-bypass-tx',bypass=True)
 finally:legacy.stop_e1();client.close()
if __name__=='__main__':
 def interrupt(signum,frame):raise KeyboardInterrupt
 signal.signal(signal.SIGTERM,interrupt)
 p=argparse.ArgumentParser();p.add_argument('--out',required=True);p.add_argument('--phase',choices=['probe','natural','race','bypass'],required=True);main(p.parse_args())
