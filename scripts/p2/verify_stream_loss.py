#!/usr/bin/env python3
"""Offline verification of the separate Stream-loss fault cohort."""
import argparse,gzip,hashlib,json,statistics
from pathlib import Path
def main():
 p=argparse.ArgumentParser();p.add_argument('archive');p.add_argument('--output',required=True);a=p.parse_args();root=Path(a.archive)
 for e in json.loads((root/'manifest.json').read_text()):
  raw=(root/e['path']).read_bytes();assert hashlib.sha256(raw).hexdigest()==e['sha256']
  assert hashlib.sha256(gzip.decompress(raw)).hexdigest()==e['uncompressed_sha256']
 rows=[]
 for folder in sorted((root/'raw/s2-stream-loss-extra').glob('S2-*')):
  d=json.loads(gzip.decompress((folder/'fault.json.gz').read_bytes()));assert d['valid']
  events=[json.loads(line) for file in folder.glob('*.jsonl.gz') for line in gzip.decompress(file.read_bytes()).splitlines()]
  assert len({e['boot_id'] for e in events})==1
  def find(event,request):
   matches=[e for e in events if e['event']==event and e.get('request')==request];assert len(matches)==1,(event,request);return matches[0]
  commit=find('commit_return','lost-write');stale=find('read_return','lost-stale');fresh=find('read_return',d['ttl_fresh']['request'])
  newer=find('commit_return','post-rebuild-write');visible=find('read_return','post-rebuild-read')
  assert commit['version']==2 and stale['version']==1 and fresh['version']==2 and newer['version']==3 and visible['version']==3
  assert commit['t_ns']<d['delete_ns']<find('read_begin','lost-stale')['t_ns']<fresh['t_ns']<newer['t_ns']<visible['t_ns']
  assert len({e['id'] for e in [commit,stale,fresh,newer,visible]})==1
  assert all(v['applied']==0 for v in d['before'].values())
  assert all(v['applied']==0 for v in d['after_loss'].values())
  assert all(v['applied']>=1 for v in d['rebuild'].values())
  value=(d['ttl_fresh']['done_ns']-d['write']['call_done_ns'])/1000;assert value==d['ttl_fallback_us']
  rows.append(dict(repeat=d['repeat'],valid=True,ttl_fallback_us=value,source_commit=d['source_commit'],server_commit_to_fresh_return_us=(fresh['t_ns']-commit['t_ns'])/1000))
 assert len(rows)==5
 result=dict(valid=True,runs=rows,ttl_fallback_us=dict(median=statistics.median(r['ttl_fallback_us'] for r in rows),min=min(r['ttl_fallback_us'] for r in rows),max=max(r['ttl_fallback_us'] for r in rows)),boundary='Small fault sample; operator recreated empty consumer groups. This does not restore lost historical events or validate full-catalog proactive rebuild.')
 Path(a.output).write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({k:v for k,v in result.items() if k!='runs'}))
if __name__=='__main__':main()
