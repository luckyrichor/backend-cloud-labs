#!/usr/bin/env python3
"""Render all S1 supplemental comparison cells from independently rebuilt data."""
import argparse,csv,gzip,json,statistics
from pathlib import Path
def main():
 p=argparse.ArgumentParser();p.add_argument('recomputed');p.add_argument('--output',required=True);a=p.parse_args();d=json.loads(gzip.decompress(Path(a.recomputed).read_bytes()));out=Path(a.output);out.mkdir(parents=True,exist_ok=True);rows=[];sections=[]
 for phase,title in [('natural','自然负载'),('race','50ms回填故障注入'),('bypass','TX loopback直写校准')]:
  lines=['## '+title,'','每行5轮；分位数先逐轮计算再取中位数。范围为五轮p99的最小–最大，单位ms。陈旧数/分母是五轮汇总。','','| 读比/请求率 | 组 | A.2陈旧数/成功读 | 客户端p99（范围） | 服务端p99（范围） | 源读QPS |','|---|---|---:|---:|---:|---:|']
  for g in [g for g in d['groups'] if g['phase']==phase]:
   selected=[r for r in d['rows'] if r['phase']==phase and r['arm']==g['arm'] and r['ratio']==g['ratio'] and r['rate']==g['rate'] and r['repeat'] in g['repeats']]
   def time(metric):
    q=g[metric];return f"{q['median']/1000:.3f}（{q['min']/1000:.3f}–{q['max']/1000:.3f}）"
   lines.append(f"| {int(g['ratio']*100)}%/{g['rate']} | {g['arm']} | {g['stale']}/{g['reads']} | {time('client_get_p99_us')} | {time('server_get_p99_us')} | {g['source_qps']['median']:.2f} |")
   row=dict(phase=phase,ratio=g['ratio'],rate=g['rate'],arm=g['arm'],repeats=','.join(map(str,g['repeats'])),stale=g['stale'],successful_reads=g['reads'],pooled_stale_ratio=g['stale']/g['reads'],stale_ratio_median=g['stale_ratio']['median'],stale_ratio_min=g['stale_ratio']['min'],stale_ratio_max=g['stale_ratio']['max'],source_qps=g['source_qps']['median'],cache_hit_ratio=g['cache_hit_ratio']['median'],full_queries=g['counters']['bcl_e1_source_full_reads_total']['median'],version_queries=g['counters']['bcl_e1_source_version_reads_total']['median'])
   for scope in ['client','server']:
    for op in ['get','put']:
     for percentile in ['p50_us','p95_us','p99_us']:
      values=[r[scope+'_'+op+'_us'][percentile] for r in selected];key=scope+'_'+op+'_'+percentile
      for suffix,value in [('median',statistics.median(values)),('min',min(values)),('max',max(values))]:row[key+'_'+suffix]=value
   row['http_success_ratio']=sum(r['reads']+r['writes']-r['read_failed']-r['write_failed'] for r in selected)/sum(r['reads']+r['writes'] for r in selected);rows.append(row)
  sections.append('\n'.join(lines))
 with (out/'groups.csv').open('w') as f:
  w=csv.DictWriter(f,fieldnames=list(rows[0]),lineterminator='\n');w.writeheader();w.writerows(rows)
 (out/'tables.md').write_text('\n\n'.join(sections)+'\n')
 print(json.dumps(dict(cells=len(rows),all_selected_http_success=all(r['http_success_ratio']==1 for r in rows))))
if __name__=='__main__':main()
