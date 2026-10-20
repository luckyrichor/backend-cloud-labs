#!/usr/bin/env python3
"""P0 dataset downloads and integrity checks; raw data lives outside the repository."""
import argparse, concurrent.futures, csv, datetime, gzip, hashlib, io, json, sqlite3
import subprocess, tarfile, time, zipfile, xml.etree.ElementTree as ET
from pathlib import Path

AZURE_SHA='af967499c4f2c38bb37f8fbb202768115cff6c2e522272a498a414762d85fe16'
SQLITE_SHA='bb7e935b0bea31389b99060035f50371e384bb192baf8ed5e75b863f00b8159e'
SOURCES=[
 ('azure','https://github.com/Azure/AzurePublicDataset/releases/download/dataset-packing-2020/azurevmallocation_dataset2020_AzurePackingTraceV1.zip','azure.zip','https://github.com/Azure/AzurePublicDataset/blob/master/AzureTracesForPacking2020.md','CC BY Attribution; cite Protean OSDI 2020'),
 ('amazon','https://mcauleylab.ucsd.edu/public_datasets/data/amazon_2023/raw/meta_categories/meta_All_Beauty.jsonl.gz','meta_All_Beauty.jsonl.gz','https://amazon-reviews-2023.github.io/','License unspecified on checked page; owner approved portfolio evaluation in supplied plan; no raw redistribution'),
 ('snap','https://snap.stanford.edu/data/web-Google.txt.gz','web-Google.txt.gz','https://snap.stanford.edu/data/web-Google.html','No explicit license on source page; citation requested; shape reference only'),
 ('uci','https://archive.ics.uci.edu/static/public/502/online+retail+ii.zip','online-retail-ii.zip','https://archive.ics.uci.edu/dataset/502/online+retail+ii','CC BY 4.0'),
 ('alibaba-0','https://aliopentrace.oss-cn-beijing.aliyuncs.com/v2021MicroservicesTraces/MSCallGraph/MSCallGraph_0.tar.gz','MSCallGraph_0.tar.gz','https://github.com/alibaba/clusterdata/tree/master/cluster-trace-microservices-v2021','Research/learning only per repository; no explicit standalone data license'),
 ('alibaba-1','https://aliopentrace.oss-cn-beijing.aliyuncs.com/v2021MicroservicesTraces/MSCallGraph/MSCallGraph_1.tar.gz','MSCallGraph_1.tar.gz','https://github.com/alibaba/clusterdata/tree/master/cluster-trace-microservices-v2021','Research/learning only per repository; no explicit standalone data license'),
]

def sha(path):
 h=hashlib.sha256()
 with path.open('rb') as f:
  for b in iter(lambda:f.read(1048576),b''):h.update(b)
 return h.hexdigest()

def verify(key,path):
 if key=='azure':
  if sha(path)!=AZURE_SHA:raise ValueError('Azure archive hash does not match supplied plan')
  target=path.parent/'packing_trace_zone_a_v1.sqlite'
  with zipfile.ZipFile(path) as z:
   if z.testzip() is not None:raise ValueError('Azure ZIP CRC failed')
   member=next(x for x in z.infolist() if x.filename.endswith('.sqlite'))
   with z.open(member) as src,target.open('wb') as dst:
    while b:=src.read(1048576):dst.write(b)
  if sha(target)!=SQLITE_SHA:raise ValueError('Azure SQLite hash mismatch')
  with sqlite3.connect(f'file:{target}?mode=ro',uri=True) as db:
   return {'rows':db.execute('select count(*) from vm').fetchone()[0],
           'vm_types':db.execute('select count(*) from vmType').fetchone()[0],
           'priority_counts':dict(db.execute('select priority,count(*) from vm group by priority')),
           'sqlite_bytes':target.stat().st_size,'sqlite_sha256':sha(target),'integrity':db.execute('pragma integrity_check').fetchone()[0]}
 if key=='amazon':
  rows=0;keys=set()
  with gzip.open(path,'rt') as f:
   for line in f:
    item=json.loads(line);keys.update(item);rows+=1
  if not {'title','price','parent_asin'}.issubset(keys):raise ValueError('not product metadata')
  return {'rows':rows,'field_names':sorted(keys),'synthetic':False}
 if key=='snap':
  count=0;nodes=set()
  with gzip.open(path,'rt') as f:
   for line in f:
    if line.startswith('#'):continue
    a,b=map(int,line.split());count+=1;nodes.update((a,b))
  return {'rows':count,'nodes':len(nodes),'synthetic':False,'meaning':'web link graph, not CMDB'}
 if key=='uci':
  counts={}
  with zipfile.ZipFile(path) as outer:
   name=next(n for n in outer.namelist() if n.endswith('.xlsx'))
   with zipfile.ZipFile(io.BytesIO(outer.read(name))) as workbook:
    for name in workbook.namelist():
     if name.startswith('xl/worksheets/sheet') and name.endswith('.xml'):
      rows=0
      with workbook.open(name) as f:
       for event,element in ET.iterparse(f,events=('end',)):
        if element.tag.endswith('}row'):rows+=1;element.clear()
      counts[name]=max(0,rows-1)
  return {'rows':sum(counts.values()),'worksheet_data_rows':counts,'meaning':'transaction rows, not unique products'}
 if key.startswith('alibaba'):
  counts={}
  with tarfile.open(path,mode='r|gz') as archive:
   for member in archive:
    if member.isfile():
     f=archive.extractfile(member)
     if f is not None:
      header=f.readline().decode().strip()
      if header!=',traceid,timestamp,rpcid,um,rpctype,dm,interface,rt':raise ValueError('unexpected Alibaba header')
      counts[member.name]=1+sum(1 for _ in f)
  return {'rows':sum(n-1 for n in counts.values()),'header_rows':len(counts),'member_lines_including_headers':counts,'meaning':'sampled callgraph rows, not CMDB'}
 raise ValueError('unknown dataset')

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--data-dir',type=Path,required=True);p.add_argument('--proxy',required=True);p.add_argument('--only',nargs='*');p.add_argument('--output',type=Path,default=Path('datasets/manifest.json'));a=p.parse_args()
 root=Path.cwd().resolve();data=a.data_dir.resolve()
 if data==root or root in data.parents:p.error('raw datasets must be outside repository')
 data.mkdir(parents=True,exist_ok=True);data.chmod(0o700)
 def one(source):
  key,url,name,page,license=source;path=data/name
  record={'id':key,'url':url,'page':page,'license_statement':license,'raw_data_not_redistributed':True}
  partial=path.with_suffix(path.suffix+'.partial')
  cmd=['curl','-q','--fail','--location','--silent','--show-error','--retry','0','--connect-timeout','20','--max-time','600','--max-filesize',str(512*1024*1024),'--proxy','' if a.proxy=='DIRECT' else a.proxy,'--output',str(partial),'--write-out','%{http_code}']
  start=time.monotonic()
  try:
   if path.exists():record.update(exit_code=0,http_status=None,reused_local_file=True)
   else:
    r=subprocess.run(cmd+[url],capture_output=True,text=True,timeout=610)
    record.update(exit_code=r.returncode,http_status=int(r.stdout) if r.stdout.isdigit() else None,error=r.stderr.strip(),received_bytes=partial.stat().st_size if partial.exists() else 0)
    if r.returncode:raise ValueError('download failed')
    partial.rename(path)
   record.update(bytes=path.stat().st_size,sha256=sha(path),**verify(key,path),verified=True)
  except Exception as error:
   record.update(verified=False,rows=None,sha256=None,error_type=type(error).__name__,validation_error=str(error),fallback_status='pending CP1 owner decision; no silent substitution')
  record['seconds']=time.monotonic()-start
  (data/(key+'-result.json')).write_text(json.dumps(record,indent=2))
  print(json.dumps({'id':key,'verified':record['verified'],'bytes':record.get('bytes'),'rows':record.get('rows'),'error':record.get('error')}),flush=True)
  return record
 selected=[s for s in SOURCES if not a.only or s[0] in a.only]
 with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:results=list(pool.map(one,selected))
 if a.output.exists():
  previous=json.loads(a.output.read_text())['datasets'];old={r['id']:r for r in previous}
  for r in results:
   if r['id'] in old and old[r['id']]!=r:r['previous_attempts']=[old[r['id']]]
  updated={r['id']:r for r in results};results=[updated.pop(r['id'],r) for r in previous]+list(updated.values())
 a.output.parent.mkdir(parents=True,exist_ok=True)
 a.output.write_text(json.dumps({'generated_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'proxy_explicit':a.proxy,'datasets':results,'synthetic_tracks':{'source':'synthetic','status':'P1 not yet generated'}},indent=2)+'\n')
