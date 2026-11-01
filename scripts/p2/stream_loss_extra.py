#!/usr/bin/env python3
"""Owned-stream loss injection. Run serially only after S1 measurement is complete."""
import asyncio,fcntl,hashlib,json,subprocess,sys,time
from pathlib import Path
ROOT=Path('/home/ubuntu/project/WorkPlan/backend-cloud-labs');sys.path.insert(0,str(ROOT/'scripts/p2'))
from runtime import Runtime,STATE
async def wait_for(test,seconds=5):
 end=time.perf_counter()+seconds
 while time.perf_counter()<end:
  value=await test()
  if value:return value
  await asyncio.sleep(.01)
 raise RuntimeError('stream-loss recovery deadline')
async def trial(out,repeat):
 r=Runtime(out,'C');d={'repeat':repeat,'kind':'owned-stream-loss','valid':False,'scope':'fault injection; separate from original S2 performance distributions','source_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'script_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest()};id=700000+repeat
 try:
  await r.start()
  for i in [0,1]:await r.request(19096+i,'POST','/debug/stream/pause?state=true',phase='fault',rid='pause-'+str(i))
  await asyncio.sleep(.25);prime=await r.get(1,id,'prime','prime');assert prime['version']==1
  write=await r.put(id,'lost-write','fault');assert write['version']==2
  async def published():return int(r.redis('XLEN',r.run))>=1
  await wait_for(published)
  before={str(i):await r.request(19096+i,'GET','/debug/stream',phase='control',rid='before-'+str(i)) for i in [0,1]}
  assert all(s['applied']==0 for s in before.values()),'event consumed before loss injection'
  d['delete_ns']=time.perf_counter_ns();assert r.redis('DEL',r.run)=='1';assert r.redis('EXISTS',r.run+':cp')=='1','checkpoint must remain after channel loss'
  stale=await r.get(1,id,'lost-stale','fault');assert stale['version']==1,'did not observe stale L1 with event lost'
  for i in [0,1]:await r.request(19096+i,'POST','/debug/stream/pause?state=false',phase='fault',rid='resume-'+str(i))
  async def fresh():
   v=await r.get(1,id,'ttl-'+str(time.perf_counter_ns()),'fault');return v if v['version']==2 else None
  d['ttl_fresh']=await wait_for(fresh,3)
  after={str(i):await r.request(19096+i,'GET','/debug/stream',phase='control',rid='lost-'+str(i)) for i in [0,1]}
  assert all(s['applied']==0 for s in after.values()),'lost event unexpectedly applied'
  # An operator rebuilds empty consumer groups; this does not recreate the lost event.
  for i in [0,1]:r.redis('XGROUP','CREATE',r.run,r.run+':instance-'+str(i),'0','MKSTREAM')
  newer=await r.put(id,'post-rebuild-write','fault');assert newer['version']==3
  async def applied():
   s={str(i):await r.request(19096+i,'GET','/debug/stream',phase='control',rid='rebuild-'+str(i)+'-'+str(time.perf_counter_ns())) for i in [0,1]}
   return s if all(x['applied']>=1 for x in s.values()) else None
  rebuilt=await wait_for(applied);visible=await r.get(1,id,'post-rebuild-read','fault');assert visible['version']==3
  d.update(valid=True,before=before,after_loss=after,rebuild=rebuilt,stale=stale,write=write,newer=newer,visible=visible,ttl_fallback_us=(d['ttl_fresh']['done_ns']-write['call_done_ns'])/1000)
 except Exception as e:d.update(error=type(e).__name__+': '+str(e))
 finally:r.close()
 (Path(out)/'fault.json').write_text(json.dumps(d,indent=2)+'\n');print(json.dumps({'event':'stream-loss-trial','repeat':repeat,'valid':d['valid'],'error':d.get('error')}),flush=True)
 if not d['valid']:raise RuntimeError('stream-loss assertion failed; preserve evidence and inspect')
async def main():
 locks=[]
 for p in [STATE/'controller.lock',ROOT/'.local/s1-suite/controller.lock']:
  f=p.open('w');fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB);locks.append(f)
 provenance=json.loads((STATE/'provenance.json').read_text());assert provenance['source_commit']==subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
 for name,digest in provenance['binaries'].items():assert hashlib.sha256((STATE/name).read_bytes()).hexdigest()==digest
 out=STATE/'s2-stream-loss-extra';out.mkdir(exist_ok=True)
 for repeat in range(1,6):
  folder=out/f'S2-stream-loss-r{repeat}'
  if (folder/'fault.json').exists():assert json.loads((folder/'fault.json').read_text())['valid'];continue
  await trial(folder,repeat)
 (out/'complete.json').write_text(json.dumps({'valid':True,'runs':5,'source_commit':provenance['source_commit']}))
if __name__=='__main__':asyncio.run(main())
