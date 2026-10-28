#!/usr/bin/env python3
"""Lossless, whitelisted S1 evidence archive at the paths recorded in bench_run."""
import datetime,gzip,hashlib,importlib.util,json,re,shutil
from pathlib import Path
ROOT=Path.cwd();STATE=ROOT/'.local/s1-suite';DEST=ROOT/'docs/measurements/2026-10-27-s1'
SOURCE='d2ca677db32a17eaed9aff776244262b88d42801'
spec=importlib.util.spec_from_file_location('suite',ROOT/'scripts/s1-suite.py');c=importlib.util.module_from_spec(spec);spec.loader.exec_module(c)
query="""SELECT JSON_OBJECT('run_id',run_id,'story',story,'arm',arm,'repeat',repeat_no,'source_commit',git_commit,
'data_version',data_version,'path',result_path,'status',status,'started_at',DATE_FORMAT(started_at,'%Y-%m-%dT%H:%i:%s.%f'))
FROM bcl.bench_run WHERE story LIKE 'S1%' ORDER BY started_at,run_id;"""
rows=[json.loads(line) for line in c.sql(query).splitlines()]
SUFFIXES=['.csv.gz','.done.json','.network.json','.container.json','.correctness.json','.worker.json',
          '.service.log','.trace.jsonl.gz','.cpu.pprof','.mutex.pprof','.goroutine.pprof']
archived=[];unresolved=[];created=set()

def write(path,data):
    path.parent.mkdir(parents=True,exist_ok=True)
    if path.exists() and path.read_bytes()!=data:raise RuntimeError('refusing to replace different evidence: '+str(path))
    if not path.exists():path.write_bytes(data)
    created.add(path)

def stamp(document):
    value=document['result']['started_at']
    return datetime.datetime.fromisoformat(value.replace('Z','+00:00')).astimezone(datetime.timezone.utc).replace(tzinfo=None)

def matches(path,row):
    try:
        d=json.loads(path.read_bytes())
        return all(d[key]==row[column] for key,column in [('story','story'),('arm','arm'),('repeat','repeat'),('git_commit','source_commit'),('data_version','data_version')]) and abs((stamp(d)-datetime.datetime.fromisoformat(row['started_at'])).total_seconds())<=0.000002
    except (ValueError,KeyError):return False

for row in rows:
    relative=Path(row['path']);parts=relative.parts
    if len(parts)<5 or parts[:2]!=('docs','measurements') or not re.fullmatch(r'\d{4}-\d{2}-\d{2}-s1',parts[2]):continue
    if relative.is_absolute() or '..' in parts or not relative.name.endswith('.json.gz'):raise RuntimeError('unsafe recorded path')
    expected=STATE.joinpath(*parts[3:]).with_suffix('')
    source=expected if expected.is_file() and matches(expected,row) else None
    if source is None:
        candidates=[p for p in STATE.rglob(expected.name) if matches(p,row)]
        if candidates:
            hashes={hashlib.sha256(p.read_bytes()).hexdigest() for p in candidates}
            if len(hashes)!=1:raise RuntimeError('ambiguous historic raw result: '+row['run_id'])
            source=candidates[0]
    if source is None:
        unresolved.append(dict(row,reason='No matching local JSON with the registered identity and timestamp'))
        if row['source_commit']==SOURCE:raise RuntimeError('current source evidence missing: '+row['path'])
        continue
    target=ROOT/relative;raw=source.read_bytes();write(target,gzip.compress(raw,mtime=0))
    for suffix in SUFFIXES:
        side=source.with_name(source.stem+suffix)
        if side.is_file():write(target.parent/(source.stem+suffix),side.read_bytes())
    archived.append(dict(row,raw_json_sha256=hashlib.sha256(raw).hexdigest()))
# User-interrupted attempts have no completed result/registry row; keep them separate.
for folder in (STATE/'matrix-idle-2026-10-27').glob('interrupted-user-pause-*'):
    for path in folder.iterdir():
        if path.is_file() and (path.name.endswith('.json') or path.name.endswith('.jsonl.gz')):
            write(DEST/'matrix-idle-2026-10-27'/folder.name/path.name,path.read_bytes())
META=['environment.json','prepared.json','import.json','design.json','primary-mac-environment.json','primary-provenance.json','primary-design.json',
      'primary-completion.json','supplement-client-environment.json','supplement-policy.json','supplement-rounds.json',
      'tx-calibration-environment.json','paused-state-2026-10-27.json']
for name in META:
    if (STATE/name).is_file():write(DEST/'metadata'/name,(STATE/name).read_bytes())
for path in STATE.glob('profile-*-top.txt'):write(DEST/'metadata'/path.name,path.read_bytes())
# Exact control programs used after the primary run; no credentials or binaries.
for src,name in [(ROOT/'docs/measurements/2026-10-27-s1/control/supplement.py','supplement.py'),
                 (ROOT/'docs/measurements/2026-10-27-s1/control/tx-calibration.py','tx-calibration.py'),
                 (STATE/'find-race.py','find-race.py')]:
    assert src.is_file()
    target=DEST/'control'/name
    if target!=src:write(target,src.read_bytes())
(DEST/'registry.json').write_text(json.dumps(dict(archived=archived,unresolved_legacy=unresolved),indent=2,ensure_ascii=False)+'\n')
# Check full credential values against prospective public evidence, including gzip payloads.
secret=json.loads((ROOT/'.local/deploy/client-env.json').read_text())
needles=[v.encode() for k,v in secret.items() if isinstance(v,str) and len(v)>=8 and re.search(r'PASSWORD|TOKEN|SECRET|AUTHORIZATION|DSN|API_KEY',k,re.I)]
bad=[]
for path in created|{DEST/'registry.json'}:
    raw=path.read_bytes()
    if raw.startswith(b'\x1f\x8b'):
        try:raw=gzip.decompress(raw)
        except OSError:pass
    if any(value in raw for value in needles):bad.append(str(path.relative_to(ROOT)))
if bad:raise RuntimeError('credential values found in: '+str(bad))
print(json.dumps(dict(archived_registry_rows=len(archived),unresolved_legacy=len(unresolved),created_files=len(created),bytes=sum(p.stat().st_size for p in created))))
