#!/usr/bin/env python3
"""Recompute S2 first-fresh bounds against archived all-key server event clocks."""
import argparse,collections,gzip,json,math
from pathlib import Path
def load(path):return json.loads(gzip.decompress(path.read_bytes()))
def verify(folder):
 d=load(folder/'result.json.gz');assert d['valid'];events=[]
 for p in folder.glob('*.jsonl.gz'):events.extend(json.loads(x) for x in gzip.decompress(p.read_bytes()).splitlines())
 assert len({e['boot_id'] for e in events})==1
 assert all(e.get('session') for e in events)
 audit={};by_version=collections.defaultdict(dict)
 for e in events:
  if e['event'] in ['binlog_observed','stream_publish_begin','stream_publish_return','stream_applied'] and e['instance'] in ['binlog-bridge','instance-1']:
   by_version[(e['id'],e.get('version'))][e['event']]=e
  if e['event'] in ['read_begin','read_return','commit_call_begin','commit_return']:
   k=(e['instance'],e['event'],e.get('request'))
   assert k not in audit,('duplicate',k);audit[k]=e
 rows=[];samples=0;regressions=0;fresh_before=0;max_poll_gap=0;max_late=0;max_width=0;stages=[]
 for f in d['flows']:
  assert 'error' not in f
  target=f['write']['version'];id=f['id'];req=f['write']['request']
  call=audit[('instance-0','commit_call_begin',req)];commit=audit[('instance-0','commit_return',req)]
  assert call['id']==commit['id']==id and commit['version']==target
  assert f['write']['call_begin_ns']<=call['t_ns']<=commit['t_ns']<=f['write']['call_done_ns']
  assert len(f['reads'])>=math.ceil(600/d['poll_ms'])
  first=next(i for i,r in enumerate(f['reads']) if r['version']>=target)
  assert f['first_fresh_index']==first
  fresh=f['reads'][first];stale=[r for r in f['reads'][:first] if r['version']<target]
  lower=max(0,(stale[-1]['begin_ns']-commit['t_ns'])/1e6) if stale else 0
  upper=max(0,(fresh['done_ns']-commit['t_ns'])/1e6)
  for r in f['reads']:
   begin=audit[('instance-1','read_begin',r['request'])];end=audit[('instance-1','read_return',r['request'])]
   assert begin['id']==end['id']==id and end['version']==r['version']
   assert r['begin_ns']<=begin['t_ns']<=end['t_ns']<=r['done_ns']
   max_late=max(max_late,r['begin_ns']-r['planned_ns'])
  for a,b in zip(f['reads'],f['reads'][1:]):max_poll_gap=max(max_poll_gap,b['begin_ns']-a['begin_ns'])
  regressions+=sum(r['version']<target for r in f['reads'][first+1:]);fresh_before+=fresh['done_ns']<commit['t_ns'];samples+=len(f['reads']);max_width=max(max_width,upper-lower)
  rows.append({'id':id,'lower_ms':lower,'upper_ms':upper,'write_call_ms':(f['write']['call_done_ns']-f['write']['call_begin_ns'])/1e6})
  if d['arm']=='C' and not d['reverse']:
   e=by_version[(id,target)]
   assert len(e)==4
   observed=e['binlog_observed']['t_ns'];pb=e['stream_publish_begin']['t_ns'];pr=e['stream_publish_return']['t_ns'];apply=e['stream_applied']['t_ns'];assert observed<=pb<=pr and pb<=apply
   stages.append({'id':id,'row_to_publish_ack_us':(pr-observed)/1000,'publish_to_apply_lower_us':max(0,apply-pr)/1000,'publish_to_apply_upper_us':(apply-pb)/1000,'apply_before_publish_ack':apply<pr,'commit_to_observed_signed_us':(observed-commit['t_ns'])/1000})
 assert len(rows)==len(d['observations'])==math.ceil(d['seconds']*d['rate'])
 for actual,stored in zip(rows,d['observations']):
  assert actual['id']==stored['id']
  for key in ['lower_ms','upper_ms','write_call_ms']:assert abs(actual[key]-stored[key])<1e-9
 return {'run':d['run'],'source_commit':d['source_commit'],'observations':len(rows),'poll_reads':samples,'regressions_after_first_fresh':regressions,'fresh_before_commit_return':fresh_before,'max_actual_poll_gap_ms':max_poll_gap/1e6,'max_poll_lateness_ms':max_late/1e6,'max_window_bracket_width_ms':max_width,'stages':stages}
def main():
 p=argparse.ArgumentParser();p.add_argument('archive');p.add_argument('--output',required=True);a=p.parse_args();root=Path(a.archive);rows=[verify(p.parent) for p in sorted((root/'raw/s2-matrix-v5').glob('*/result.json.gz'))];assert len(rows)==50
 out=Path(a.output);out.write_bytes(gzip.compress(json.dumps(rows,separators=(',',':')).encode(),mtime=0));print(json.dumps({'runs':len(rows),'observations':sum(r['observations'] for r in rows),'poll_reads':sum(r['poll_reads'] for r in rows),'regressions':sum(r['regressions_after_first_fresh'] for r in rows),'fresh_before_commit_return':sum(r['fresh_before_commit_return'] for r in rows),'max_poll_gap_ms':max(r['max_actual_poll_gap_ms'] for r in rows)}))
if __name__=='__main__':main()
