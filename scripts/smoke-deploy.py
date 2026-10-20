#!/usr/bin/env python3
"""Real bounded smoke requests, no credentials or message payloads in evidence."""
import json,socket,subprocess,time,urllib.request,urllib.error
from pathlib import Path
records=[]
def req(port,path,expected=200,body=None):
 data=json.dumps(body).encode() if body else None
 r=urllib.request.Request(f'http://127.0.0.1:{port}{path}',data=data,headers={'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(r,timeout=5) as response:code=response.status;out=response.read(1048576)
 except urllib.error.HTTPError as e:code=e.code;out=e.read()
 assert code==expected,(port,path,code,expected)
 records.append({'port':port,'path':path,'http_status':code,'bytes':len(out)})
 return out
req(18086,'/items/kettle');req(18085,'/state')
assert json.loads(req(18083,'/evaluate',body={'expression':'x*2+1','variables':{'x':2}}))['value']==5
assert json.loads(req(18083,'/evaluate',body={'expression':'0 && (1/0)'}))['value']==0
req(18083,'/evaluate',400,{'expression':'1/0'})
with socket.create_connection(('127.0.0.1',18082),timeout=3) as s:
 s.sendall(b'{"type":"join"}\n');f=s.makefile('rb');assert json.loads(f.readline())['type']=='welcome';assert json.loads(f.readline())['type']=='snapshot'
records.append({'protocol':'tcp','welcome_and_snapshot':True})
for port in (19086,19082,19083,19085):
 metrics=req(port,'/metrics').decode();assert '_bucket{le="+Inf"}' in metrics and '_count' in metrics
 req(port,'/debug/pprof/')
for port in (18086,18083,18085):req(port,'/debug/pprof/',404);req(port,'/metrics',404)
targets=json.loads(req(19190,'/api/v1/targets'))['data']['activeTargets']
assert len(targets)==4 and all(t['health']=='up' for t in targets)
assert json.loads(req(13100,'/api/health'))['database']=='ok'
containers=[]
for name in ('mysql','redis','e1','e2','e3','e5','prometheus','grafana'):
 raw=subprocess.check_output(['docker','inspect','--format','{{json .State}}','bcl-p0-'+name+'-1']);state=json.loads(raw)
 assert state['Running'] and state['Health']['Status']=='healthy',name
 # Export only non-secret resource/image fields.
 raw=subprocess.check_output(['docker','inspect','--format','{{json .HostConfig}}','bcl-p0-'+name+'-1']);host=json.loads(raw)
 containers.append({'service':name,'healthy':True,'cpuset':host['CpusetCpus'],'memory_limit':host['Memory'],'nofile':host['Ulimits'],'ports':host['PortBindings'],'image_id':subprocess.check_output(['docker','inspect','--format','{{.Image}}','bcl-p0-'+name+'-1'],text=True).strip()})
Path('.local/p0/smoke.json').write_text(json.dumps({'requests':records,'containers':containers},indent=2)+'\n')
print('Smoke passed; responses and credentials omitted')
