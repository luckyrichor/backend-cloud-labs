#!/usr/bin/env python3
"""Rebuild S2 scalar/HDR evidence solely from the public archive."""
import argparse,gzip,json,math,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
def main():
 p=argparse.ArgumentParser();p.add_argument('archive');p.add_argument('--output',required=True);a=p.parse_args();root=Path(a.archive).resolve();out=Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True)
 subprocess.run(['python3',str(ROOT/'scripts/p2/verify_s2.py'),str(root),'--output',str(out/'recomputed.json.gz')],check=True)
 raw=subprocess.check_output(['go','-C',str(ROOT/'loadgen'),'run','./cmd/p2summary',str(root/'raw/s2-matrix-v5')]);(out/'histograms.json.gz').write_bytes(gzip.compress(raw,mtime=0))
 vectors={};rows=json.loads(gzip.decompress((out/'recomputed.json.gz').read_bytes()))
 for r in rows:
  for field in ['row_to_publish_ack_us','publish_to_apply_lower_us','publish_to_apply_upper_us']:
   if r['stages']:vectors[r['run']+'/'+field]=[math.ceil(s[field]) for s in r['stages']]
 for kind in ['consumer-pause','bridge-crash','consumer-crash-pending','duplicate-reorder']:
  values=[]
  for path in sorted((root/'raw/s2-faults-v5').glob('*/fault.json.gz')):
   d=json.loads(gzip.decompress(path.read_bytes()));assert d['valid']
   if d['kind']!=kind:continue
   x=d['details'];start=x.get('resume_ns',x.get('restart_ns'))
   if start is not None:values.append(math.ceil((x['fresh']['done_ns']-start)/1000))
  if values:vectors['fault/'+kind+'/recovery_us']=values
 raw=subprocess.check_output(['go','-C',str(ROOT/'loadgen'),'run','./cmd/p2summary','--vectors'],input=json.dumps(vectors).encode());(out/'stage-histograms.json.gz').write_bytes(gzip.compress(raw,mtime=0))
if __name__=='__main__':main()
