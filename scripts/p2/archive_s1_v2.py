#!/usr/bin/env python3
"""Archive supplemental S1 evidence losslessly, including failed/partial attempts.

Only allowlisted measurement directories/files are read. Runtime env/DSN, private
client files, executables and datasets are deliberately outside the allowlist.
"""
import argparse,gzip,hashlib,json,os,subprocess
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
def main():
 p=argparse.ArgumentParser();p.add_argument('--output',required=True);a=p.parse_args()
 state=ROOT/'.local/p2-s1';out=Path(a.output);out.mkdir(parents=True,exist_ok=True)
 assert json.loads((state/'measurement-finished.json').read_text())['valid']
 secrets=[]
 for name in ['client-env.json']:
  for k,v in json.loads((ROOT/'.local/deploy'/name).read_text()).items():
   if any(w in k for w in ['PASSWORD','SECRET','TOKEN','AUTHORIZATION','DSN']) and len(str(v))>=8:secrets.append(str(v).encode())
 for line in (ROOT/'.local/deploy/e1-s1.env').read_text().splitlines():
  if '=' in line:
   k,v=line.split('=',1)
   if any(w in k for w in ['PASSWORD','SECRET','TOKEN','AUTHORIZATION','DSN']) and len(v)>=8:secrets.append(v.encode())
 manifest=[];known=set();attempts=[]
 def archive(rel,raw):
  plain=gzip.decompress(raw) if rel.suffix=='.gz' else raw
  assert not any(s in plain for s in secrets),'sensitive content in '+str(rel)
  digest=hashlib.sha256(plain).hexdigest();known.add(digest)
  payload=raw if rel.suffix=='.gz' else gzip.compress(raw,compresslevel=6,mtime=0)
  dest=out/'raw'/rel
  if rel.suffix!='.gz':dest=dest.with_name(dest.name+'.gz')
  dest.parent.mkdir(parents=True,exist_ok=True)
  if dest.exists():assert dest.read_bytes()==payload,'conflicting evidence '+str(rel)
  else:dest.write_bytes(payload)
  manifest.append(dict(path=str(dest.relative_to(out)),sha256=hashlib.sha256(payload).hexdigest(),uncompressed_sha256=digest,source_bytes=len(plain),archived_bytes=len(payload)))
 for phase in ['probes','probes-v2','probes-final','natural','race','bypass','recovery-20261029','recovery-20261031']:
  for source in sorted((state/phase).rglob('*')):
   if source.is_file() and source.suffix in ['.json','.gz','.jsonl','.log']:
    archive(source.relative_to(state),source.read_bytes())
    if source.name.endswith('.a2.json.gz'):
     d=json.loads(gzip.decompress(source.read_bytes()))
     attempts.append(dict(path=str(source.relative_to(state)),source_commit=d['source_commit'],story=d['story'],arm=d['arm'],repeat=d['repeat'],rate=d['rate'],ratio=d['ratio'],client_valid=d['client_valid'],truth_valid=d['valid'],source_audit_ok=d['source_audit_ok']))
 for name in ['provenance.json','mac-environment.json','prepared.json','probe-qualified.json','quality.json','measurement-finished.json','events.jsonl','pipeline.log','resume-status-20261029.json','resume-status-20261031.json']:
  if (state/name).exists():archive(Path(name),(state/name).read_bytes())
 for source in sorted(state.glob('probe-qualified-history-*.json')):archive(source.relative_to(state),source.read_bytes())
 # Complete trace bytes are already archived. Retain every distinct interrupted
 # trace as well, without weakening mode0600 on the service-owned source file.
 for source in sorted((state/'traces').rglob('*.jsonl')):
  raw=subprocess.check_output(['sudo','-n','cat',str(source)])
  if hashlib.sha256(raw).hexdigest() not in known:archive(source.relative_to(state),raw)
 (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
 (out/'attempts.json').write_text(json.dumps(attempts,indent=2)+'\n')
 # Preserve paths recorded by bench_run at measurement time. These are byte-
 # identical aliases of the canonical client result, not additional attempts.
 aliases=[]
 for attempt in attempts:
  rel=Path(attempt['path']);stem=rel.name[:-len('.a2.json.gz')]
  canonical=out/'raw'/rel.parent/(stem+'.json.gz');alias=out/rel.parent/(stem+'.json.gz')
  alias.parent.mkdir(parents=True,exist_ok=True)
  if not alias.exists():os.link(canonical,alias)
  assert alias.read_bytes()==canonical.read_bytes()
  aliases.append(dict(path=str(alias.relative_to(out)),canonical=str(canonical.relative_to(out)),sha256=hashlib.sha256(alias.read_bytes()).hexdigest()))
 (out/'registry-paths.json').write_text(json.dumps(aliases,indent=2)+'\n')
 print(json.dumps(dict(files=len(manifest),complete_attempts=len(attempts),archived_bytes=sum(d['archived_bytes'] for d in manifest))))
if __name__=='__main__':main()
