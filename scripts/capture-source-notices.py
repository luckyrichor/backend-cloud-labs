#!/usr/bin/env python3
"""Save source notices outside Git and attach their exact URLs and hashes to manifest."""
import concurrent.futures,hashlib,json,subprocess
from pathlib import Path
sources={
'azure':['https://raw.githubusercontent.com/Azure/AzurePublicDataset/master/LICENSE','https://raw.githubusercontent.com/Azure/AzurePublicDataset/master/AzureTracesForPacking2020.md'],
'amazon':['https://amazon-reviews-2023.github.io/'],
'snap':['https://snap.stanford.edu/data/web-Google.html'],
'uci':['https://archive.ics.uci.edu/dataset/502/online+retail+ii'],
'alibaba':['https://raw.githubusercontent.com/alibaba/clusterdata/master/README.md','https://raw.githubusercontent.com/alibaba/clusterdata/master/cluster-trace-microservices-v2021/README.md']}
root=Path('/home/ubuntu/datasets/backend-cloud-labs/p0/notices');root.mkdir(exist_ok=True)
def fetch(entry):
 key,urls=entry;out=[]
 for i,url in enumerate(urls):
  p=root/f'{key}-{i}.txt';r=subprocess.run(['curl','-q','-fLsS','--proxy','http://127.0.0.1:7890','--max-time','60','--max-filesize','2097152',url,'-o',str(p)],capture_output=True)
  out.append({'url':url,'exit_code':r.returncode,'bytes':p.stat().st_size if p.exists() else 0,'sha256':hashlib.sha256(p.read_bytes()).hexdigest() if p.exists() else None})
 return key,out
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:notices=dict(pool.map(fetch,sources.items()))
p=Path('datasets/manifest.json');doc=json.loads(p.read_text())
for r in doc['datasets']:
 r['license_original_sources']=notices[r['id'].split('-')[0]]
 if r['id'].startswith('alibaba'):
  r['rows']=sum(n-1 for n in r['member_lines_including_headers'].values());r['header_rows']=len(r['member_lines_including_headers']);r['header_verified']='traceid,timestamp,rpcid,um,rpctype,dm,interface,rt'
p.write_text(json.dumps(doc,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(notices,indent=2))
