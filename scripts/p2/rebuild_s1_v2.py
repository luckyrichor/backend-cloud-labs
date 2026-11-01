#!/usr/bin/env python3
"""Recompute full-key truth and quality selection from the public S1 archive.

Requires a separately built s1summary binary for original HDR histograms. No
database, remote machine, runtime credentials or original .local tree is used.
"""
import argparse,gzip,hashlib,json,statistics,subprocess,tempfile
from pathlib import Path
from audit_truth import calculate
from s1_quality import inspect

def main():
 p=argparse.ArgumentParser();p.add_argument('archive');p.add_argument('--summary-binary',required=True);p.add_argument('--output',required=True);a=p.parse_args()
 archive=Path(a.archive).resolve();output=Path(a.output);output.mkdir(parents=True,exist_ok=True)
 manifest=json.loads((archive/'manifest.json').read_text())
 for e in manifest:
  payload=(archive/e['path']).read_bytes();assert hashlib.sha256(payload).hexdigest()==e['sha256'],e['path']
  assert hashlib.sha256(gzip.decompress(payload)).hexdigest()==e['uncompressed_sha256'],e['path']
 verified=[];rows=[]
 with tempfile.TemporaryDirectory(prefix='s1-public-rebuild-') as directory:
  work=Path(directory)
  for phase in ['natural','race','bypass']:
   dest=work/phase;dest.mkdir()
   for truthfile in sorted((archive/'raw'/phase).glob('*.a2.json.gz')):
    stem=truthfile.name[:-len('.a2.json.gz')];original=json.loads(gzip.decompress(truthfile.read_bytes()));audits=[truthfile.parent/(stem+'.audit.jsonl.gz')]
    if phase=='bypass':audits.append(truthfile.parent/(stem+'.bypass.audit.jsonl.gz'))
    truth=calculate(audits,truthfile.parent/(stem+'.csv.gz'),stem)
    for k,v in truth.items():assert original[k]==v,(stem,k)
    verified.append(dict(run=stem,phase=phase,valid=truth['valid'],reads=truth['successful_formal_reads'],keys=truth['unique_formal_read_keys'],stale=truth['stale']))
    (dest/truthfile.name).write_bytes(truthfile.read_bytes())
    for suffix in ['.json','.correctness.json']:
     (dest/(stem+suffix)).write_bytes(gzip.decompress((truthfile.parent/(stem+suffix+'.gz')).read_bytes()))
   rows.extend(dict(row,phase=phase) for row in json.loads(subprocess.check_output([str(Path(a.summary_binary).resolve()),str(dest)],text=True)))
  quality=inspect(work)
  assert quality==json.loads(gzip.decompress((archive/'raw/quality.json.gz').read_bytes()))
  groups=[]
  for g in quality['groups']:
   for arm in g['arms']:
    selected=[r for r in rows if r['phase']==g['phase'] and r['story']==g['story'] and r['arm']==arm and r['ratio']==g['ratio'] and r['rate']==g['rate'] and r['repeat'] in g['selected_repeats']]
    assert len(selected)==5
    truths=[json.loads(gzip.decompress((work/g['phase']/(r['file'][:-5]+'.a2.json.gz')).read_bytes())) for r in selected]
    def stats(values):return dict(median=statistics.median(values),min=min(values),max=max(values))
    counters={k:stats([r['server_counters'].get(k,0) for r in selected]) for k in selected[0]['server_counters']}
    groups.append(dict(phase=g['phase'],ratio=g['ratio'],rate=g['rate'],arm=arm,repeats=g['selected_repeats'],stale=sum(t['stale'] for t in truths),reads=sum(t['successful_formal_reads'] for t in truths),key_count_range=[min(t['unique_formal_read_keys'] for t in truths),max(t['unique_formal_read_keys'] for t in truths)],stale_ratio=stats([t['stale_ratio'] for t in truths]),client_get_p99_us=stats([r['client_get_us']['p99_us'] for r in selected]),server_get_p99_us=stats([r['server_get_us']['p99_us'] for r in selected]),source_qps=stats([r['source_reads_per_second'] for r in selected]),cache_hit_ratio=stats([r['cache_hit_ratio'] for r in selected]),counters=counters))
 result=dict(quality=quality,verified=verified,groups=groups,rows=rows)
 payload=json.dumps(result,sort_keys=True,separators=(',',':')).encode();(output/'recomputed.json.gz').write_bytes(gzip.compress(payload,mtime=0))
 print(json.dumps(dict(verified=len(verified),selected=quality['selected_runs'],groups=len(groups),sha256=hashlib.sha256(payload).hexdigest())))
if __name__=='__main__':main()
