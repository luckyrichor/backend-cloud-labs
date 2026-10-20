#!/usr/bin/env python3
"""Mac-origin TCP/ICMP observations and temporary SSH forwards; no Docker on Mac."""
import argparse,concurrent.futures,datetime,hashlib,json,math,os,platform,socket,subprocess,sys,time,urllib.request
from pathlib import Path

def command(argv,timeout=30):
 try:
  r=subprocess.run(argv,text=True,capture_output=True,timeout=timeout)
  return {'command':argv,'exit_code':r.returncode,'stdout':r.stdout.strip(),'stderr':r.stderr.strip()}
 except subprocess.TimeoutExpired:return {'command':argv,'exit_code':None,'error':'timeout'}

def connect(host,port,n):
 rows=[]
 for _ in range(n):
  start=time.perf_counter()
  try:
   with socket.create_connection((host,port),timeout=1):pass
   error=None
  except OSError as e:error=type(e).__name__
  rows.append({'seconds':time.perf_counter()-start,'success':error is None,'error_type':error});time.sleep(.05)
 good=sorted(r['seconds']*1000 for r in rows if r['success'])
 def percentile(q):return good[max(0,math.ceil(len(good)*q)-1)] if good else None
 return {'host':host,'port':port,'metric':'TCP connect wall time, includes TCP handshake; not HTTP latency','observations':rows,'successes':len(good),'attempts':n,'p50_ms':percentile(.5),'p95_ms':percentile(.95),'p99_ms':percentile(.99)}

def main():
 p=argparse.ArgumentParser();p.add_argument('--ssh-host',default='tx');p.add_argument('--output',type=Path,required=True);a=p.parse_args()
 if sys.platform!='darwin':p.error('must run on actual Mac')
 if a.ssh_host.startswith('-'):p.error('invalid SSH host')
 if subprocess.check_output(['git','status','--porcelain']):p.error('clean worktree required')
 commit=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
 cfg=command(['ssh','-G',a.ssh_host]);assert cfg['exit_code']==0
 fields=dict(line.split(' ',1) for line in cfg['stdout'].splitlines() if ' ' in line)
 host=fields['hostname'];sshport=int(fields['port']);ports=[18086,18082,18083,18085]
 result={'generated_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'platform':platform.platform(),'hostname':socket.gethostname(),'source_commit':commit,'working_tree_modified_before_collection':False,'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'ssh_host_alias':a.ssh_host,'tx_host':host,'ssh_port':sshport,'scope':'P0 reachability only; no S1-S7 throughput conclusion'}
 with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
  direct=[pool.submit(connect,host,port,20) for port in [sshport]+ports]
  ping=pool.submit(command,['ping','-n','-c','20',host],30)
  result['direct']=[f.result() for f in direct];result['icmp']=ping.result()
 listeners=[];localports=[]
 for _ in ports:
  s=socket.socket();s.bind(('127.0.0.1',0));localports.append(s.getsockname()[1]);listeners.append(s)
 args=['ssh','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ConnectTimeout=10','-N']
 for local,remote in zip(localports,ports):args+=['-L',f'127.0.0.1:{local}:127.0.0.1:{remote}']
 args.append(a.ssh_host)
 for s in listeners:s.close()
 proc=subprocess.Popen(args,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,text=True)
 try:
  start=time.monotonic()
  while True:
   if proc.poll() is not None:raise RuntimeError('SSH forward process exited')
   try:
    with socket.create_connection(('127.0.0.1',localports[0]),timeout=.5):break
   except OSError:
    if time.monotonic()-start>12:raise RuntimeError('SSH forward startup timeout')
    time.sleep(.1)
  with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
   result['tunnel']=[f.result() for f in [pool.submit(connect,'127.0.0.1',port,20) for port in localports]]
  # Local socket connect alone does not prove remote forwarding; require actual replies.
  smoke=[]
  for local,remote in zip(localports,ports):
   if remote==18082:
    with socket.create_connection(('127.0.0.1',local),timeout=5) as s:
     s.settimeout(5);s.sendall(b'{"type":"join"}\n');f=s.makefile('rb');assert json.loads(f.readline())['type']=='welcome';assert json.loads(f.readline())['type']=='snapshot'
    smoke.append({'remote_port':remote,'welcome_and_snapshot':True})
   else:
    route={18086:'/items/kettle',18083:'/evaluate',18085:'/state'}[remote]
    body=b'{"expression":"x*2+1","variables":{"x":2}}' if remote==18083 else None
    request=urllib.request.Request(f'http://127.0.0.1:{local}{route}',data=body,headers={'Content-Type':'application/json'})
    t=time.perf_counter()
    with urllib.request.urlopen(request,timeout=5) as response:
     content=response.read(1048576);assert response.status==200
     if remote==18083:assert json.loads(content)['value']==5
     smoke.append({'remote_port':remote,'http_status':response.status,'bytes':len(content),'request_seconds':time.perf_counter()-t})
  result['forward_mapping']=[{'local_port':l,'tx_loopback_port':r} for l,r in zip(localports,ports)]
  result['forward_command']=args;result['forward_replies']=smoke
 finally:
  proc.terminate()
  try:proc.wait(timeout=5)
  except subprocess.TimeoutExpired:proc.kill();proc.wait()
  result['forward_process_terminated']=proc.poll() is not None
  result['local_listeners_closed']=all(connect('127.0.0.1',p,1)['successes']==0 for p in localports)
 a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps({'output':str(a.output),'source_commit':commit,'direct_successes':[x['successes'] for x in result['direct']],'remote_replies':len(result['forward_replies']),'tunnel_closed':result['local_listeners_closed']}))
if __name__=='__main__':main()
