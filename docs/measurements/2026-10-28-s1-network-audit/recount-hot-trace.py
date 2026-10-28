"""Existing S1 traces only. Whole-attempt hot-key diagnostic, not A.2 all-key result."""
from pathlib import Path
import bisect,collections,gzip,json
ROOT=Path(__file__).resolve().parents[3]
base=ROOT/'docs/measurements/2026-10-27-s1'
rows=[]
for folder in ['matrix-idle-2026-10-27','tx-calibration-2026-10-27']:
 for p in sorted((base/folder).glob('*.trace.jsonl.gz')):
  meta=json.loads(gzip.decompress(p.with_name(p.name.replace('.trace.jsonl.gz','.json.gz')).read_bytes()))
  if not meta['result']['valid']:continue
  events=[json.loads(l) for l in gzip.decompress(p.read_bytes()).splitlines()]
  ops=collections.defaultdict(list)
  for e in events:ops[e['op']].append(e)
  commits=sorted((e['t_ns'],e['version']) for e in events if e['event']=='write_commit')
  times=[];versions=[];best=1
  for t,v in commits:best=max(best,v);times.append(t);versions.append(best)
  complete=stale=incomplete=0;examples=[]
  for op,es in ops.items():
   starts=[e for e in es if e['event']=='read_start']
   if not starts:continue
   terminal=[e for e in es if e['event'] in ('cache_hit','cache_validated','fill')]
   if len(starts)!=1 or not terminal:incomplete+=1;continue
   end=max(terminal,key=lambda e:e['t_ns']);start=starts[0]['t_ns']
   idx=bisect.bisect_left(times,start)-1;truth=versions[idx] if idx>=0 else 1
   complete+=1
   if end['version']<truth:
    stale+=1
    if len(examples)<3:examples.append(dict(op=op,start_ns=start,returned=end['version'],prior_observed_committed=truth))
  if 'bypass' in meta['story'].lower():stale=None
  rows.append(dict(run=p.stem.replace('.trace.jsonl',''),arm=meta['arm'],story=meta['story'],trace=str(p.relative_to(ROOT)),commits=len(commits),reads=complete,stale=stale,incomplete=incomplete,ratio=stale/complete if complete and stale is not None else None,external_write_truth_missing='bypass' in meta['story'].lower(),examples=examples))
groups=[]
for story in sorted(set(r['story'] for r in rows)):
 for arm in 'ABCD':
  selected=[r for r in rows if r['story']==story and r['arm']==arm]
  if not selected:continue
  n=sum(r['reads'] for r in selected);stale=None if any(r['external_write_truth_missing'] for r in selected) else sum(r['stale'] for r in selected)
  groups.append(dict(story=story,arm=arm,runs=len(selected),reads=n,stale=stale,ratio=stale/n if n and stale is not None else None,incomplete=sum(r['incomplete'] for r in selected),external_write_truth_missing=any(r['external_write_truth_missing'] for r in selected)))
result={'scope':'one configured key, whole attempt incl prime/warmup; service-observed write return, NOT MySQL commit instant; bypass external writes absent; no all-key/formal-window claim','groups':groups,'runs':rows}
print(json.dumps(result,ensure_ascii=False,indent=2))
