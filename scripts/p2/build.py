#!/usr/bin/env python3
"""Build fixed, clean-source P2 binaries; credentials never enter the build."""
from pathlib import Path
import hashlib,json,os,shutil,subprocess
ROOT=Path(__file__).resolve().parents[2];STATE=ROOT/'.local/p2-suite';STATE.mkdir(parents=True,exist_ok=True)
if subprocess.check_output(['git','status','--porcelain'],cwd=ROOT).strip():raise SystemExit('clean source required')
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip();hashes={}
for name,module,command in [('guide','experiments/e1-shopping-guide','guide'),('binlog-stream','experiments/e1-shopping-guide','binlog-stream'),('source-probe','experiments/e1-shopping-guide','source-probe'),('loadgen','loadgen','loadgen'),('summary','loadgen','s1summary')]:
 path=STATE/name;args=['go','-C',str(ROOT/module),'build','-trimpath']
 if name=='loadgen':args+=['-ldflags','-X main.sourceCommit='+commit]
 subprocess.run(args+['-o',str(path),'./cmd/'+command],env=os.environ|{'CGO_ENABLED':'0'},check=True)
 hashes[name]=hashlib.sha256(path.read_bytes()).hexdigest()
mac=STATE/'loadgen-mac';subprocess.run(['go','-C',str(ROOT/'loadgen'),'build','-trimpath','-ldflags','-X main.sourceCommit='+commit,'-o',str(mac),'./cmd/loadgen'],env=os.environ|{'CGO_ENABLED':'0','GOOS':'darwin','GOARCH':'arm64'},check=True)
hashes['loadgen-mac']=hashlib.sha256(mac.read_bytes()).hexdigest()
(STATE/'provenance.json').write_text(json.dumps({'source_commit':commit,'binaries':hashes},indent=2));print(json.dumps({'source_commit':commit,'binaries':len(hashes)}))
