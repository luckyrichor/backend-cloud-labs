#!/usr/bin/env python3
"""Offline aggregation for the S1 single-branch supplement.

Reads the new five-round arms from a .local results directory and the frozen
A/B/B-off reference rounds from docs/measurements/2026-10-28-s1-v2/raw/race.
No database, Mac, Docker or network access is used.

Every selected round must pass the recorded quality gates and the frozen design
before a table is produced. The old reference was measured at source a00e151;
the new arms must share one later source commit. Results are therefore
descriptive across batches, not a same-commit causal estimate.
"""
import argparse,gzip,json,statistics
from pathlib import Path

NEW_ARMS=('B-gate-only','B-write-only')
REFERENCE_ARMS=('A','B','B-off')
STORY_REFERENCE='S1-v2-fault-fill'
STORY_NEW='S1-v2-single-branch'
DESIGN=dict(ratio=.95,rate=400,warmup='10s',window='32s',fill_delay='50ms',
            data_version='catalog/b0e5aba50106/seed-20261023/8025233af339',
            max_inflight=1536,connections_per_target=256,prime_connections=128,timeout='10s')
OLD_SOURCE_PREFIX='a00e151'
LIMITATION='旧参考批次a00e151与新冻结提交是不同测量批次，只作描述性比较，不作同提交因果估计'


def read(path):
 path=Path(path)
 return gzip.decompress(path.read_bytes()) if path.suffix=='.gz' else path.read_bytes()
def load_json(path):return json.loads(read(path))
def truth_stem(path):
 name=Path(path).name
 for suffix in ['.a2.json.gz','.a2.json']:
  if name.endswith(suffix):return name[:-len(suffix)]
 raise ValueError('not an A.2 truth file: '+str(path))
def sidecar(directory,name,suffix):
 for candidate in [directory/(name+suffix),directory/(name+suffix+'.gz')]:
  if candidate.exists():return candidate
 return None
def trace_counts(path):
 if path is None or not path.exists():return None
 counts={}
 for line in read(path).splitlines():
  event=json.loads(line).get('event')
  if event:counts[event]=counts.get(event,0)+1
 return counts
def path_counters(counters):
 return dict(sets=counters.get('bcl_e1_cache_sets_total',0),deletes=counters.get('bcl_e1_cache_deletes_total',0),
             fills_applied=counters.get('bcl_e1_cache_fills_applied_total',0),fills_rejected=counters.get('bcl_e1_cache_fills_rejected_total',0),
             full_reads=counters.get('bcl_e1_source_full_reads_total',0),writes=counters.get('bcl_e1_source_writes_total',0))
def path_ok(arm,counters):
 c=path_counters(counters)
 if c['writes']<=0:return False
 if arm=='B-gate-only':return c['deletes']==c['writes'] and c['sets']==0
 return c['deletes']==0 and c['sets']>0 and c['fills_applied']==0 and c['fills_rejected']==0


def validate_round(path,truth,client,correctness,arm,repeat,expected_source=None,source_prefix=None,require_path=False):
 name=truth_stem(path)
 if truth.get('story')!=client.get('story') or truth.get('arm')!=arm or truth.get('repeat')!=repeat:
  raise RuntimeError('truth/client metadata mismatch for '+name)
 if client.get('arm')!=arm or client.get('repeat')!=repeat:
  raise RuntimeError('client metadata mismatch for '+name)
 commit=truth.get('source_commit')
 if not commit or client.get('git_commit')!=commit:raise RuntimeError('client/truth source mismatch for '+name)
 if expected_source is not None and commit!=expected_source:raise RuntimeError('unexpected source commit for '+name)
 if source_prefix is not None and not commit.startswith(source_prefix):raise RuntimeError('reference source is not '+source_prefix+' for '+name)
 if truth.get('valid') is not True:raise RuntimeError('A.2 truth invalid for '+name)
 if truth.get('client_valid') is not True:raise RuntimeError('A.2 client_valid false for '+name)
 if truth.get('source_audit_ok') is not True:raise RuntimeError('A.2 source audit failed for '+name)
 if client.get('result',{}).get('valid') is not True:raise RuntimeError('client result invalid for '+name)
 if correctness.get('ok') is not True:raise RuntimeError('source correctness sidecar failed for '+name)
 config=client.get('config',{})
 expected={'read_ratio':DESIGN['ratio'],'rate':DESIGN['rate'],'warmup':DESIGN['warmup'],'window':DESIGN['window'],
           'timeout':DESIGN['timeout'],'max_inflight':DESIGN['max_inflight'],'connections_per_target':DESIGN['connections_per_target'],
           'prime_connections':DESIGN['prime_connections'],'seed':20261026+repeat,'bypass_writes':False}
 for key,value in expected.items():
  if config.get(key)!=value:raise RuntimeError(f'{key}={config.get(key)!r} differs from frozen design for {name}')
 if client.get('data_version')!=DESIGN['data_version']:raise RuntimeError('data version differs from frozen design for '+name)
 if truth.get('fill_delay')!=DESIGN['fill_delay']:raise RuntimeError('fill delay differs from frozen design for '+name)
 counters=client['result']['server']['counters_delta'];observed=path_counters(counters)
 if observed['writes']<=0:raise RuntimeError('no successful source writes for '+name)
 if require_path:
  if truth.get('path_counters_ok') is not True:raise RuntimeError('path counters not accepted for '+name)
  if truth.get('write_path')!=('delete' if arm=='B-gate-only' else 'plain-fill'):raise RuntimeError('unexpected write path for '+name)
  if not path_ok(arm,counters):raise RuntimeError('cache counters prove the wrong path for '+name)
 return dict(arm=arm,repeat=repeat,stem=name,stale=truth['stale'],reads=truth['successful_formal_reads'],
  stale_ratio=truth['stale_ratio'],valid=True,source_commit=commit,data_version=client.get('data_version'),
  ratio=config['read_ratio'],rate=config['rate'],warmup=config['warmup'],window=config['window'],
  fill_delay=truth.get('fill_delay'),counters=observed,path_counters_ok=truth.get('path_counters_ok'),
  write_path=truth.get('write_path'),trace=trace_counts(sidecar(Path(path).parent,name,'.trace.jsonl')))


def collect(directory,arms,story,expected_source=None,source_prefix=None,require_path=False):
 directory=Path(directory);records={arm:{} for arm in arms};commits=set()
 files=sorted(list(directory.glob('*.a2.json.gz'))+list(directory.glob('*.a2.json')))
 for path in files:
  truth=load_json(path)
  if truth.get('story')!=story or truth.get('arm') not in records:continue
  arm,repeat=truth['arm'],truth['repeat']
  if repeat not in range(1,6):continue
  if repeat in records[arm]:raise RuntimeError(f'duplicate round {story} {arm} r{repeat}')
  name=truth_stem(path)
  client_path=sidecar(directory,name,'.json')
  correctness_path=sidecar(directory,name,'.correctness.json')
  if client_path is None:raise RuntimeError('missing client JSON for '+name)
  if correctness_path is None:raise RuntimeError('missing source correctness sidecar for '+name)
  record=validate_round(path,truth,load_json(client_path),load_json(correctness_path),arm,repeat,
                        expected_source,source_prefix,require_path)
  records[arm][repeat]=record;commits.add(record['source_commit'])
  if record['reads']<=0 or record['stale']<0:raise RuntimeError('impossible stale/read counts for '+name)
  if abs(record['stale_ratio']-record['stale']/record['reads'])>1e-12:raise RuntimeError('stale ratio inconsistent for '+name)
 for arm in arms:
  if sorted(records[arm])!=list(range(1,6)):raise RuntimeError(f'{story} {arm} lacks repeats 1-5')
 if len(commits)!=1:raise RuntimeError(f'{story} mixes source commits')
 return records


def range_stats(values):
 return dict(median=statistics.median(values),min=min(values),max=max(values))
def summarize(records,arms):
 rows=[]
 for arm in arms:
  rounds=[records[arm][r] for r in sorted(records[arm])]
  ratios=[r['stale_ratio'] for r in rounds]
  rows.append(dict(arm=arm,rounds=len(rounds),stale=sum(r['stale'] for r in rounds),reads=sum(r['reads'] for r in rounds),
   pooled_stale_ratio=sum(r['stale'] for r in rounds)/sum(r['reads'] for r in rounds),
   per_round_stale_ratio=range_stats(ratios),per_round=rounds,
   source_commits=sorted({r['source_commit'] for r in rounds}),
   source_full_reads=sum(r['counters']['full_reads'] for r in rounds)))
 return rows
def load_batch(directory,arms,story,expected_source=None,source_prefix=None,require_path=False):
 records=collect(directory,arms,story,expected_source,source_prefix,require_path)
 return summarize(records,arms)


def main(a):
 reference=load_batch(a.reference,REFERENCE_ARMS,STORY_REFERENCE,source_prefix=OLD_SOURCE_PREFIX)
 new=load_batch(a.results,NEW_ARMS,STORY_NEW,expected_source=a.source_commit,require_path=True)
 result=dict(reference=reference,new=new,limitation=LIMITATION,
             design=dict(ratio=DESIGN['ratio'],rate=DESIGN['rate'],warmup=DESIGN['warmup'],window=DESIGN['window'],
                         fill_delay=DESIGN['fill_delay'],data_version=DESIGN['data_version'],repeats=5),
             reference_source='a00e151...',new_source=sorted({c for row in new for c in row['source_commits']}))
 output=Path(a.output);output.parent.mkdir(parents=True,exist_ok=True)
 output.write_text(json.dumps(result,indent=2,sort_keys=True)+'\n')
 if a.markdown:
  lines=['# S1 single-branch supplement','',f'跨批次限制：{LIMITATION}。','','## 每轮明细','',
         '| 组 | 轮 | 陈旧数/成功读 | 比例 | 源完整读 | sets | deletes | 门控应用/拒绝 | trace fill/delete |','|---|---:|---:|---:|---:|---:|---:|---:|---:|']
  for batch,title in [(reference,'参考（旧a00e151）'),(new,'新单分支')]:
   for row in batch:
    for r in row['per_round']:
     trace=r['trace'] or {}
     lines.append(f"| {title} {row['arm']} | {r['repeat']} | {r['stale']}/{r['reads']} | {r['stale_ratio']:.6f} | {r['counters']['full_reads']} | {r['counters']['sets']} | {r['counters']['deletes']} | {r['counters']['fills_applied']}/{r['counters']['fills_rejected']} | {trace.get('fill',0)}/{trace.get('delete',0)} |")
  lines+=['','## 五轮汇总','',
          '| 组 | 轮数 | 陈旧数/成功读 | 汇总比例 | 逐轮比例中位数 | 最小 | 最大 |','|---|---:|---:|---:|---:|---:|---:|']
  for batch,title in [(reference,'参考（旧a00e151）'),(new,'新单分支')]:
   for row in batch:
    q=row['per_round_stale_ratio']
    lines.append(f"| {title} {row['arm']} | {row['rounds']} | {row['stale']}/{row['reads']} | {row['pooled_stale_ratio']:.6f} | {q['median']:.6f} | {q['min']:.6f} | {q['max']:.6f} |")
  target=Path(a.markdown);target.parent.mkdir(parents=True,exist_ok=True)
  target.write_text('\n'.join(lines)+'\n')
 print(json.dumps({'new':[(r['arm'],r['stale'],r['reads']) for r in new],
                   'reference':[(r['arm'],r['stale'],r['reads']) for r in reference]}))


if __name__=='__main__':
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--results',required=True)
 p.add_argument('--reference',default='docs/measurements/2026-10-28-s1-v2/raw/race')
 p.add_argument('--output',required=True)
 p.add_argument('--markdown')
 p.add_argument('--source-commit')
 main(p.parse_args())
