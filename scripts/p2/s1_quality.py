#!/usr/bin/env python3
"""Select whole common quality-valid S1 groups; never select by measured latency."""
import argparse,gzip,json
from pathlib import Path
GROUPS=[('natural','S1-v2',ratio,rate,['A','B','C','CV','D']) for ratio in [.95,.99] for rate in [100,250,400]]+[
 ('race','S1-v2-fault-fill',.95,400,['A','B','B-off','C','CV','D']),('bypass','S1-v2-bypass-tx',.95,400,['A','B','C','CV','D'])]
def inspect(root):
 groups=[];sources=set();selected=0
 for phase,story,ratio,rate,arms in GROUPS:
  records={arm:{} for arm in arms}
  for path in (root/phase).glob('*.a2.json.gz'):
   d=json.loads(gzip.decompress(path.read_bytes()))
   if (d['story'],d['ratio'],d['rate'])!=(story,ratio,rate):continue
   arm=d['arm'];sources.add(d['source_commit']);stem=path.name[:-len('.a2.json.gz')]
   client=json.loads((path.parent/(stem+'.json')).read_text());correct=json.loads((path.parent/(stem+'.correctness.json')).read_text())
   assert client['git_commit']==d['source_commit']
   good=d['valid'] and d['client_valid'] and d['source_audit_ok'] and client['result']['valid'] and correct['ok']
   if good:records[arm][d['repeat']]=d
  common=sorted(set.intersection(*(set(records[arm]) for arm in arms)))
  assert len(common)>=5,('insufficient complete quality-valid group',phase,ratio,rate,common)
  keep=common[:5];selected+=len(arms)*5
  groups.append({'phase':phase,'story':story,'ratio':ratio,'rate':rate,'arms':arms,'selected_repeats':keep,'common_valid_repeats':common})
 assert len(sources)==1,'mixed measured source commits'
 return {'valid':True,'source_commit':next(iter(sources)),'groups':groups,'selected_runs':selected,'rule':'first five common quality-valid repetitions of every complete group; all attempts preserved; no latency/stale-outcome filtering'}
def main():
 p=argparse.ArgumentParser();p.add_argument('root');p.add_argument('--output',required=True);a=p.parse_args();result=inspect(Path(a.root));path=Path(a.output)
 if path.exists():assert json.loads(path.read_text())==result
 else:path.write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps({'valid':True,'selected_runs':result['selected_runs'],'groups':len(result['groups'])}))
if __name__=='__main__':main()
