"""Private experiment runtime: no public listeners, no shared Redis flushes."""
import asyncio,contextlib,fcntl,importlib.util,json,os,signal,subprocess,time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];STATE=ROOT/'.local/p2-suite'

def decode_chunked(raw):
 output=bytearray();offset=0
 while True:
  end=raw.find(b'\r\n',offset)
  if end<0:raise ValueError('truncated chunk size')
  length=int(raw[offset:end].split(b';',1)[0],16);offset=end+2
  if length==0:return bytes(output)
  if offset+length+2>len(raw) or raw[offset+length:offset+length+2]!=b'\r\n':raise ValueError('truncated chunk data')
  output.extend(raw[offset:offset+length]);offset+=length+2

class Runtime:
 def __init__(self,out,arm,local_ttl='500ms',delay='3ms'):
  self.out=Path(out);self.out.mkdir(parents=True,exist_ok=False);self.arm=arm;self.run=self.out.name+"-t"+str(time.time_ns());self.processes=[];self.logs=[];self.peers={};self.bridge=None;self.redis_info={}
  self.client=json.loads((ROOT/'.local/deploy/client-env.json').read_text());self.env=dict(os.environ)
  for line in (ROOT/'.local/deploy/e1-s1.env').read_text().splitlines():
   if '=' in line and not line.startswith('#'):
    k,v=line.split('=',1);self.env[k]=v
  for k in ['P0_MYSQL_ADDRESS','P0_MYSQL_USER','P0_MYSQL_PASSWORD']:self.env[k]=self.client[k]
  self.env.update(GUIDE_MYSQL_DSN=self.client['LOADGEN_MYSQL_DSN'].replace('/bcl?','/bcl_s1?'),GUIDE_SOURCE='mysql',GUIDE_CACHE_TTL='1m',CACHE_READ_MODE='cache-aside',GUIDE_LOCAL_TTL=local_ttl,GUIDE_CACHE_PREFIX=self.run+':cache:',GUIDE_EXPERIMENTAL='1')
  if arm=='B':self.env['GUIDE_DOUBLE_DELETE_DELAY']=delay
  self.changed=set();self.generations={0:0,1:0};self.bridge_generation=0
 def redis(self,*args):
  return subprocess.check_output(['docker','exec','bcl-p2-redis','redis-cli','--raw',*map(str,args)],text=True).strip()
 def prepare(self):
  name='bcl-p2-redis';p=subprocess.run(['docker','inspect','--format','{{index .Config.Labels "bcl.experiment"}}',name],capture_output=True,text=True)
  if p.returncode:
   data=ROOT/'.local/s2/redisdata';data.mkdir(parents=True,exist_ok=True)
   subprocess.run(['docker','run','-d','--name',name,'--label','bcl.experiment=p2','--network','bcl-p0_default','--memory','192m','--cpuset-cpus','1','-v',str(data)+':/data','redis:7-alpine','redis-server','--appendonly','yes','--appendfsync','always','--maxmemory','128mb','--maxmemory-policy','noeviction','--save',''],check=True,capture_output=True)
  elif p.stdout.strip()!='p2':raise RuntimeError('Redis ownership mismatch')
  else:
   running=subprocess.check_output(['docker','inspect','--format','{{.State.Running}}',name],text=True).strip()
   if running!='true':subprocess.run(['docker','start',name],check=True,capture_output=True)
  info=json.loads(subprocess.check_output(['docker','inspect',name]))[0];self.env['REDIS_ADDR']=info['NetworkSettings']['Networks']['bcl-p0_default']['IPAddress']+':6379'
  self.redis_info={'image':info['Image'],'ports':info['NetworkSettings']['Ports'],'aof':self.redis('CONFIG','GET','appendonly','appendfsync','maxmemory-policy'),'persistence':self.redis('INFO','persistence')}
 def spawn(self,args,env,name,cpu):
  log=(self.out/(name+'.log')).open('wb');self.logs.append(log)
  p=subprocess.Popen(['taskset','-c',str(cpu),*map(str,args)],env=env,stdout=log,stderr=log);self.processes.append(p);return p
 def start_bridge(self,initialize=False):
  self.bridge_generation+=1
  args=[STATE/'binlog-stream','--database','bcl_s1','--stream',self.run,'--checkpoint',self.run+':cp','--audit-file',self.out/f'bridge-{self.bridge_generation}.jsonl']
  if initialize:args+=['--initialize']
  self.bridge=self.spawn(args,self.env,'bridge-'+str(self.bridge_generation),3);return self.bridge
 def start_peer(self,i):
  self.generations[i]+=1;name=f'instance-{i}-{self.generations[i]}'
  env=self.env|{'GUIDE_ADDRESS':f'127.0.0.1:{18096+i}','ADMIN_ADDRESS':f'127.0.0.1:{19096+i}','GUIDE_INSTANCE':f'instance-{i}','GUIDE_AUDIT_FILE':str(self.out/(name+'.jsonl'))}
  if self.arm=='C':env['GUIDE_STREAM']=self.run
  p=self.spawn([STATE/'guide'],env,name,2);self.peers[i]=p;return p
 async def start(self):
  self.prepare()
  if self.arm=='C':
   self.start_bridge(True)
   for _ in range(100):
    if self.bridge.poll() is not None:raise RuntimeError('bridge initialization failed')
    if self.redis('EXISTS',self.run+':cp')=='1':break
    await asyncio.sleep(.03)
   else:raise RuntimeError('checkpoint initialization timeout')
  for i in [0,1]:self.start_peer(i)
  await self.ready()
 async def ready(self):
  for _ in range(100):
   try:
    for i,p in self.peers.items():
     if p.poll() is not None:raise RuntimeError('peer exited')
     await self.request(19096+i,'GET','/debug/audit',rid='ready',phase='control')
    return
   except (OSError,asyncio.TimeoutError):await asyncio.sleep(.03)
  raise RuntimeError('peer readiness timeout')
 async def request(self,port,method,path,body=None,rid='',phase='window'):
  headers={'Host':'loopback','Connection':'close','X-BCL-Run':self.run,'X-BCL-Phase':phase,'X-BCL-Request':rid}
  data=b''
  if body is not None:
   data=json.dumps(body,separators=(',',':')).encode();headers.update({'Authorization':self.client['LOADGEN_AUTHORIZATION'],'Content-Type':'application/json','Content-Length':str(len(data))})
  async def exchange():
   reader,writer=await asyncio.open_connection('127.0.0.1',port)
   try:
    wire=f'{method} {path} HTTP/1.1\r\n'+''.join(f'{k}: {v}\r\n' for k,v in headers.items())+'\r\n'
    writer.write(wire.encode()+data);await writer.drain();head=await reader.readuntil(b'\r\n\r\n');status=int(head.split(b' ',2)[1])
    raw=await reader.read()
    if status!=200:raise RuntimeError('HTTP status '+str(status))
    if b'transfer-encoding: chunked' in head.lower():raw=decode_chunked(raw)
    return json.loads(raw)
   finally:writer.close();await writer.wait_closed()
  return await asyncio.wait_for(exchange(),3)
 async def get(self,i,id,rid,phase='window'):
  start=time.perf_counter_ns();result=await self.request(18096+i,'GET','/items/'+str(id),rid=rid,phase=phase)
  return {'begin_ns':start,'done_ns':time.perf_counter_ns(),'version':result['item']['version'],'source':result['source'],'request':rid}
 async def put(self,id,rid,phase='window'):
  self.changed.add(id);start=time.perf_counter_ns();result=await self.request(18096,'PUT','/items/'+str(id),{'title':'p2 experiment','price_cent':1234,'stock':5},rid=rid,phase=phase)
  return {'call_begin_ns':start,'call_done_ns':time.perf_counter_ns(),'version':result['item']['version'],'request':rid}
 @staticmethod
 def stop_process(p,kill=False):
  if p.poll() is None:
   p.send_signal(signal.SIGKILL if kill else signal.SIGTERM)
   try:p.wait(timeout=10)
   except subprocess.TimeoutExpired:p.kill();p.wait()
 def close(self):
  for p in reversed(self.processes):self.stop_process(p)
  for f in self.logs:f.close()
  spec=importlib.util.spec_from_file_location('restore',ROOT/'scripts/s1-suite.py');module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
  module.restore(sorted(self.changed))
  (self.out/'runtime.json').write_text(json.dumps({'arm':self.arm,'local_ttl':self.env['GUIDE_LOCAL_TTL'],'double_delete_delay':self.env.get('GUIDE_DOUBLE_DELETE_DELAY','0s'),'shared_ttl':'1m','stream':self.run,'cache_prefix':self.env['GUIDE_CACHE_PREFIX'],'redis':self.redis_info,'restored_ids':sorted(self.changed),'exit_codes':[p.returncode for p in self.processes],'clock':'host CLOCK_MONOTONIC','cpu_affinity':{'mysql':0,'redis':1,'both_E1_instances':2,'bridge':3}},indent=2))
