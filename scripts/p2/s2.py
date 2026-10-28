#!/usr/bin/env python3
"""Single tx coordinator. Sequential event-loop clock, loopback-only S2 observations."""
import argparse,asyncio,collections,datetime,fcntl,gzip,hashlib,json,math,os,subprocess,time,signal
from pathlib import Path
from runtime import ROOT,STATE,Runtime

def save(path,obj):
 path=Path(path);path.parent.mkdir(parents=True,exist_ok=True)
 data=json.dumps(obj,ensure_ascii=False,separators=(',',':')).encode()
 if path.exists():raise RuntimeError('refuse to overwrite evidence')
 path.write_bytes(gzip.compress(data,mtime=0) if path.suffix=='.gz' else data)
def read_audit(folder):
 rows=[]
 for path in Path(folder).glob('instance-*.jsonl'):
  rows += [json.loads(l) for l in path.read_text().splitlines()]
 boots={r['boot_id'] for r in rows};assert len(boots)==1,'mixed host clock boots'
 return rows
async def matrix_trial(out,arm,rate,repeat,seconds,poll_ms,delay,reverse=False):
 r=Runtime(out,arm,delay=delay);flows=[];failures=[];start=None
 try:
  await r.start()
  if reverse:
   Runtime.stop_process(r.bridge)
  start=time.perf_counter_ns();n=math.ceil(rate*seconds)
  async def flow(i):
   id=1000000+repeat*10000+i;row={'id':str(id),'sequence':i,'planned_ns':start+int(i/rate*1e9),'reads':[]};flows.append(row)
   try:
    row['prime']=await r.get(1,id,f'prime-{i}','prime')
    if row['prime']['version']!=1:raise RuntimeError('baseline version not one')
    row['write']=await r.put(id,f'write-{i}')
    deadline=time.perf_counter_ns()+5_000_000_000
    while time.perf_counter_ns()<deadline:
     read=await r.get(1,id,f'poll-{i}-{len(row["reads"])}');row['reads'].append(read)
     if read['version']>=row['write']['version']:return
     await asyncio.sleep(poll_ms/1000)
    raise RuntimeError('convergence timeout')
   except Exception as e:row['error']=type(e).__name__+': '+str(e);failures.append(row['error'])
  tasks=[]
  for i in range(n):
   planned=start+int(i/rate*1e9);await asyncio.sleep(max(0,(planned-time.perf_counter_ns())/1e9));tasks.append(asyncio.create_task(flow(i)))
  await asyncio.gather(*tasks)
  states={str(i):await r.request(19096+i,'GET','/debug/audit',rid='quality',phase='control') for i in [0,1]}
  if any(v['failed'] for v in states.values()):failures.append('audit write failure')
 finally:r.close()
 audit=read_audit(out);commits={(e['id'],e.get('request')):e for e in audit if e['event']=='commit_return'};observations=[]
 reads={(e['instance'],e.get('request')):e for e in audit if e['event']=='read_return' and e.get('phase')=='window'}
 for row in flows:
  if 'error' in row:continue
  commit=commits.get((row['id'],row['write']['request']))
  if commit is None:failures.append('missing commit log');continue
  if not(row['write']['call_begin_ns']<=commit['t_ns']<=row['write']['call_done_ns']):failures.append('commit outside coordinator bracket')
  if any(('instance-1',read['request']) not in reads for read in row['reads']):failures.append('missing full-key read log')
  fresh=row['reads'][-1];stale=[x for x in row['reads'] if x['version']<row['write']['version']]
  lower=max(0,(stale[-1]['begin_ns']-commit['t_ns'])/1e6) if stale else 0
  upper=max(0,(fresh['done_ns']-commit['t_ns'])/1e6)
  observations.append({'id':row['id'],'commit_return_ns':commit['t_ns'],'last_stale_begin_ns':stale[-1]['begin_ns'] if stale else None,'first_fresh_done_ns':fresh['done_ns'],'lower_ms':lower,'upper_ms':upper,'write_call_ms':(row['write']['call_done_ns']-row['write']['call_begin_ns'])/1e6})
 if len(observations)!=math.ceil(rate*seconds):failures.append('observation coverage incomplete')
 actual_in_window=sum(1 for row in flows if 'write' in row and start<=row['write']['call_begin_ns']<start+int(seconds*1e9))
 if actual_in_window < .98*math.ceil(rate*seconds):failures.append('fewer than 98% scheduled writes started in arrival window')
 # Retain failures verbatim as typed, credential-free classifications; no performance-based rejection.
 doc={'source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'data_version':json.loads((ROOT/'datasets/stats/catalog.json').read_text())['data_version'],'arm':arm,'rate':rate,'repeat':repeat,'seconds':seconds,'poll_ms':poll_ms,'reverse':reverse,'valid':not failures,'invalid_reasons':failures,'observations':observations,'flows':flows,'actual_writes_in_window':actual_in_window,'audit_events':len(audit),'states':states,'clock':'same Linux CLOCK_MONOTONIC boot for server+coordinator','coordinator_pid':os.getpid()}
 save(Path(out)/'result.json.gz',doc)
 print(json.dumps({'event':'trial-complete','path':str(out),'arm':arm,'rate':rate,'repeat':repeat,'valid':doc['valid'],'observations':len(observations)}),flush=True)
 return doc
async def main(a):
 STATE.mkdir(exist_ok=True,parents=True)
 lock=(STATE/'controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 legacy_lock=(ROOT/'.local/s1-suite/controller.lock').open('w');fcntl.flock(legacy_lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 if subprocess.check_output(['git','status','--porcelain'],cwd=ROOT).strip():raise RuntimeError('clean source required for recorded trials')
 provenance=json.loads((STATE/'provenance.json').read_text());assert provenance['source_commit']==subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'binary source mismatch'
 for name,digest in provenance['binaries'].items():assert hashlib.sha256((STATE/name).read_bytes()).hexdigest()==digest,'binary hash mismatch'
 out=Path(a.out).resolve();out.mkdir(exist_ok=True,parents=True)
 design={'rates':[5,20,50],'repeats':5,'seconds':a.seconds,'poll_ms':a.poll_ms,'local_ttl':'500ms','delay':a.delay,'rate_rule':'explicit diagnostic-to-formal write tiers 5/20/50 per second; gate actual delivery/success, do not infer saturation','source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()}
 if not(out/'design.json').exists():save(out/'design.json',design)
 else:assert json.loads((out/'design.json').read_text())==design,'immutable design mismatch'
 if a.phase=='smoke':
  for arm in 'ABC':await matrix_trial(out/f'S2-smoke-{arm}-{time.time_ns()}',arm,5,0,1,a.poll_ms,a.delay)
 else:
  for repeat in range(1,6):
   for rate in design['rates']:
    for arm in ('ABC' if repeat%2 else 'CBA'):
     path=out/f'S2-{arm}-q{rate}-r{repeat}'
     if(path/'result.json.gz').exists():continue
     await matrix_trial(path,arm,rate,repeat,a.seconds,a.poll_ms,a.delay)
  for repeat in range(1,6):
   path=out/f'S2-reverse-C-q20-r{repeat}'
   if not(path/'result.json.gz').exists():await matrix_trial(path,'C',20,repeat,a.seconds,a.poll_ms,a.delay,True)
if __name__=='__main__':
 def interrupt(signum,frame):raise KeyboardInterrupt
 signal.signal(signal.SIGTERM,interrupt)
 p=argparse.ArgumentParser();p.add_argument('--out',required=True);p.add_argument('--phase',choices=['smoke','matrix'],default='smoke');p.add_argument('--seconds',type=float,default=20);p.add_argument('--poll-ms',type=float,default=5);p.add_argument('--delay',default='3ms');asyncio.run(main(p.parse_args()))
