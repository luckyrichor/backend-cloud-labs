import csv,gzip,json,sys
from pathlib import Path
root=Path(sys.argv[1]);found=[]
for p in sorted(root.glob('S1-A-*.trace.jsonl.gz')):
    rows=[]
    with gzip.open(p,'rt') as f:
        rows=[json.loads(l) for l in f if l.strip()]
    rows.sort(key=lambda r:r['t_ns'])
    starts={};stores={};commits={};deletes={};candidate=None
    for r in rows:
        event=r['event'];op=r['op'];version=r.get('version',0)
        if event=='begin':starts[op]=r
        elif event=='store_read':stores[op]=r
        elif event=='write_commit':commits[version]=r
        elif event=='delete':deletes[version]=r
        elif event=='fill' and op in stores:
            newer=[v for v,c in commits.items() if v>version and stores[op]['t_ns']<c['t_ns']<r['t_ns'] and v in deletes and deletes[v]['t_ns']<r['t_ns']]
            if newer:
                v=max(newer);candidate=[starts.get(op),stores[op],commits[v],deletes[v],r]
        elif event=='cache_hit' and candidate and version==candidate[-1].get('version'):
            found.append({'file':p.name,'timeline':candidate+[r]});break
print(json.dumps(found,indent=2))
