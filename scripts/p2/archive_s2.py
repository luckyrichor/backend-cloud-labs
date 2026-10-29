#!/usr/bin/env python3
"""Lossless allowlisted S2 evidence archive, excluding runtime secrets/binaries."""
import argparse,gzip,hashlib,json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
def main():
 p=argparse.ArgumentParser();p.add_argument('--output',required=True);a=p.parse_args();out=Path(a.output);out.mkdir(parents=True,exist_ok=True)
 state=ROOT/'.local/p2-suite';sensitive=[]
 client=json.loads((ROOT/'.local/deploy/client-env.json').read_text())
 sensitive += [str(v).encode() for k,v in client.items() if any(word in k for word in ['PASSWORD','SECRET','TOKEN','AUTHORIZATION','DSN']) and len(str(v))>=8]
 for line in (ROOT/'.local/deploy/e1-s1.env').read_text().splitlines():
  if '=' in line:
   k,v=line.split('=',1)
   if any(word in k for word in ['PASSWORD','SECRET','TOKEN','AUTHORIZATION','DSN']) and len(v)>=8:sensitive.append(v.encode())
 manifest=[];attempts=[]
 for folder in sorted(state.glob('s2-*')):
  if not folder.is_dir():continue
  for source in sorted(folder.rglob('*')):
   if not source.is_file() or source.suffix not in ['.json','.gz','.jsonl','.log']:continue
   raw=source.read_bytes();plain=gzip.decompress(raw) if source.suffix=='.gz' else raw
   if any(secret in plain for secret in sensitive):raise RuntimeError('sensitive content detected in '+str(source.relative_to(state)))
   rel=source.relative_to(state);dest=out/'raw'/rel
   if source.suffix!='.gz':dest=dest.with_name(dest.name+'.gz');payload=gzip.compress(raw,compresslevel=6,mtime=0)
   else:payload=raw
   dest.parent.mkdir(parents=True,exist_ok=True)
   if dest.exists():assert dest.read_bytes()==payload,'archive conflict'
   else:dest.write_bytes(payload)
   manifest.append({'path':str(dest.relative_to(out)),'sha256':hashlib.sha256(payload).hexdigest(),'uncompressed_sha256':hashlib.sha256(plain).hexdigest(),'source_bytes':len(plain),'archived_bytes':len(payload)})
   if source.name in ['result.json.gz','fault.json']:
    d=json.loads(plain);final=folder.name in ['s2-matrix-v5','s2-faults-v5']
    attempts.append({'path':str(dest.relative_to(out)),'original_valid':d['valid'],'final_eligible':final and d['valid'],'source_commit':d.get('source_commit'),'arm':d.get('arm'),'rate':d.get('rate'),'repeat':d.get('repeat'),'kind':d.get('kind'),'reverse':d.get('reverse',False),'classification':'formal v5' if final else 'diagnostic or superseded observer; excluded from final performance comparison'})
 (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');(out/'attempts.json').write_text(json.dumps(attempts,indent=2)+'\n')
 print(json.dumps({'files':len(manifest),'attempts':len(attempts),'bytes':sum(x['archived_bytes'] for x in manifest),'formal':sum(x['final_eligible'] for x in attempts)}))
if __name__=='__main__':main()
