"""Read-only, serial idle HTTP RTT sampling; no load generator, no writes."""
import concurrent.futures,datetime,http.client,json,math,socket,subprocess,time
out={'time_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()}
def command(argv):
 r=subprocess.run(argv,capture_output=True,text=True,timeout=15);return {'command':argv,'exit':r.returncode,'stdout':r.stdout,'stderr':r.stderr}
out['identity']=command(['hostname'])
hosts=['43.142.37.199','10.0.0.15','100.87.80.92','2402:4e00:c032:5f00:6cf0:2914:5a6c:0']
ports=[22,80,443,8082,8085,8086,9082,9085,9086,18082,18083,18085,18086,19082,19083,19085,19086,18096,19096,13306,16379,19190,13100]
out['routes']=[command(['route','-n','get',h]) for h in hosts]
def probe(pair):
 h,p=pair;r={'host':h,'port':p};start=time.perf_counter_ns()
 try:
  s=socket.create_connection((h,p),timeout=2);r['tcp']='connected';r['connect_ms']=(time.perf_counter_ns()-start)/1e6
  s.settimeout(2)
  if p==22:r['banner_ssh']=s.recv(100).startswith(b'SSH-')
  elif p not in [443,13306,16379]:
   s.sendall(b'GET /healthz HTTP/1.1\r\nHost: audit\r\nConnection: close\r\n\r\n')
   data=s.recv(512);r['http_status_line']=data.split(b'\r\n',1)[0].decode(errors='replace')[:100]
  elif p==13306:r['greeting_bytes']=len(s.recv(128))
  s.close()
 except Exception as e:r['error']=type(e).__name__+': '+str(e)
 return r
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:out['probes']=list(pool.map(probe,[(h,p) for h in hosts for p in ports]))
print(json.dumps({'stage':'probes','data':out}),flush=True)
# Independent HTTP connections over one SSH stream, same options as S1. No existing tunnels reused.
with socket.socket() as s:s.bind(('127.0.0.1',0));local=s.getsockname()[1]
tunnel=subprocess.Popen(['ssh','-N','-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-o','ControlMaster=no','-o','ControlPath=none','-o','ServerAliveInterval=10','-L',f'127.0.0.1:{local}:127.0.0.1:18086','-L',f'127.0.0.1:{local+1}:127.0.0.1:80','tx'],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
def sample(name,h,p,path,n=1000,reuse=True):
 rows=[];conn=None
 for i in range(n):
  started=time.perf_counter_ns();row={'i':i}
  try:
   if conn is None:conn=http.client.HTTPConnection(h,p,timeout=3)
   conn.request('GET',path,headers={'Host':'audit'});r=conn.getresponse();data=r.read();row.update(status=r.status,bytes=len(data))
   row['ms']=(time.perf_counter_ns()-started)/1e6
   if not reuse:conn.close();conn=None
  except Exception as e:
   row.update(error=type(e).__name__,ms=(time.perf_counter_ns()-started)/1e6)
   if conn:conn.close();conn=None
  rows.append(row);time.sleep(.02)
 if conn:conn.close()
 values=sorted(x['ms'] for x in rows if 'error' not in x)
 result={'name':name,'host':h,'port':p,'path':path,'reuse':reuse,'samples':rows,'success':len(values),'errors':n-len(values)}
 if values:result['ms']={'p50':values[math.ceil(.5*len(values))-1],'p99':values[math.ceil(.99*len(values))-1],'max':values[-1]}
 print(json.dumps({'stage':'rtt','data':result}),flush=True)
try:
 time.sleep(2)
 for h in ['43.142.37.199','100.87.80.92']:
  q=next(x for x in out['probes'] if x['host']==h and x['port']==80)
  if q.get('http_status_line','').startswith('HTTP/'):
   sample('direct-http80-'+h,h,80,'/healthz')
 sample('ssh-http80-reuse','127.0.0.1',local+1,'/healthz')
 sample('ssh-e1-health-reuse','127.0.0.1',local,'/healthz')
 sample('ssh-e1-health-fresh','127.0.0.1',local,'/healthz',reuse=False)
finally:
 tunnel.terminate()
 try:tunnel.communicate(timeout=5)
 except subprocess.TimeoutExpired:tunnel.kill();tunnel.communicate()
 print(json.dumps({'stage':'cleanup','tunnel_exit':tunnel.returncode}),flush=True)
