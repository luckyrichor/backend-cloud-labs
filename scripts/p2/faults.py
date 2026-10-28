#!/usr/bin/env python3
"""Separate S2 failure injections, never mixed with natural latency distributions."""
import argparse,asyncio,fcntl,gzip,hashlib,json,subprocess,time
from pathlib import Path
from runtime import ROOT,STATE,Runtime
async def until(test,timeout=5):
 deadline=time.perf_counter()+timeout
 while time.perf_counter()<deadline:
  value=await test()
  if value:return value
  await asyncio.sleep(.01)
 raise RuntimeError('fault recovery timeout')
async def trial(out,kind,repeat):
 r=Runtime(out,'C');result={'kind':kind,'repeat':repeat,'scope':'fault injection, not natural performance','valid':False}
 id=500000+repeat*1000;details={}
 try:
  await r.start()
  if kind in ('consumer-pause','consumer-crash-pending'):
   await r.request(19097,'POST','/debug/stream/pause?state=true',rid='pause',phase='fault');await asyncio.sleep(.25)
  if kind=='bridge-crash':Runtime.stop_process(r.bridge,kill=True)
  await r.get(1,id,'prime','prime');write=await r.put(id,'write');target=write['version'];details['write']=write
  if kind=='consumer-pause':
   await asyncio.sleep(.2);old=await r.get(1,id,'paused-read');assert old['version']<target,'pause did not preserve a stale L1 interval';details['paused_read']=old
   details['resume_ns']=time.perf_counter_ns();await r.request(19097,'POST','/debug/stream/pause?state=false',rid='resume',phase='fault')
  elif kind=='bridge-crash':
   for offset in [1,2]:details['write-'+str(offset)]=await r.put(id+offset,'write-'+str(offset))
   await asyncio.sleep(.2);assert r.redis('XLEN',r.run)=='0','bridge progressed after kill'
   details['restart_ns']=time.perf_counter_ns();r.start_bridge(False)
  elif kind=='consumer-crash-pending':
   async def published():return int(r.redis('XLEN',r.run))>=1
   await until(published)
   delivery=r.redis('XREADGROUP','GROUP',r.run+':instance-1','instance-1','COUNT',1,'STREAMS',r.run,'>')
   assert delivery,'pending delivery missing'
   pending=int(r.redis('XPENDING',r.run,r.run+':instance-1').splitlines()[0]);assert pending>=1,'PEL missing before crash';details['pending_before']=pending
   await r.request(19097,'GET','/debug/audit?flush=1',rid='flush',phase='control')
   Runtime.stop_process(r.peers[1],kill=True);details['restart_ns']=time.perf_counter_ns();r.start_peer(1);await r.ready()
  elif kind=='duplicate-reorder':
   write2=await r.put(id,'write-new');target=write2['version'];details['write_new']=write2
   for version in [target,target-1,target,target-2]:r.redis('XADD',r.run,'*','id',id,'version',version,'observed_ns',time.perf_counter_ns(),'published_ns',time.perf_counter_ns())
  async def fresh():
   read=await r.get(1,id,'recover-'+str(time.perf_counter_ns()),'fault');return read if read['version']>=target else None
  details['fresh']=await until(fresh)
  async def all_applied():
   state=await r.request(19097,'GET','/debug/stream',rid='stream-state',phase='control')
   want=3 if kind=='bridge-crash' else 6 if kind=='duplicate-reorder' else 1
   return state if state['applied']>=want else None
  details['consumer']=await until(all_applied)
  if kind=='consumer-crash-pending':
   assert details['consumer']['replayed']>=1,'restart did not replay PEL'
   assert int(r.redis('XPENDING',r.run,r.run+':instance-1').splitlines()[0])==0,'pending not ACKed'
  if kind=='bridge-crash':
   assert int(r.redis('XLEN',r.run))>=3,'writes while bridge down lost'
   for offset in [1,2]:assert (await r.get(1,id+offset,'verify-'+str(offset),'fault'))['version']==details['write-'+str(offset)]['version']
  if kind=='duplicate-reorder':
   for n in range(20):assert (await r.get(1,id,'stable-'+str(n),'fault'))['version']>=target,'cache regressed after replay'
  result.update(valid=True,details=details)
 except Exception as e:result.update(error=type(e).__name__+': '+str(e),details=details)
 finally:r.close()
 result['source_commit']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip();Path(out,'fault.json').write_text(json.dumps(result,indent=2))
 print(json.dumps({'event':'fault-complete','kind':kind,'repeat':repeat,'valid':result['valid'],'path':str(out)}),flush=True)
 return result
async def main(a):
 lock=(STATE/'controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 legacy_lock=(ROOT/'.local/s1-suite/controller.lock').open('w');fcntl.flock(legacy_lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 if subprocess.check_output(['git','status','--porcelain'],cwd=ROOT).strip():raise RuntimeError('clean source required')
 provenance=json.loads((STATE/'provenance.json').read_text());assert provenance['source_commit']==subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
 for name,digest in provenance['binaries'].items():assert hashlib.sha256((STATE/name).read_bytes()).hexdigest()==digest
 out=Path(a.out).resolve();out.mkdir(parents=True,exist_ok=True)
 for repeat in range(1,a.repeats+1):
  for kind in ['consumer-pause','bridge-crash','consumer-crash-pending','duplicate-reorder']:
   path=out/f'S2-fault-{kind}-r{repeat}'
   if (path/'fault.json').exists():continue
   await trial(path,kind,repeat)
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--out',required=True);p.add_argument('--repeats',type=int,default=5);asyncio.run(main(p.parse_args()))
