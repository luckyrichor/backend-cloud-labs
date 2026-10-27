#!/usr/bin/env python3
"""Complete validity-only whole-condition supplementary repeats, preserving primary runs."""
import fcntl,hashlib,importlib.util,json,math,subprocess
from pathlib import Path
ROOT=Path.cwd();STATE=ROOT/'.local/s1-suite';OUT=STATE/'matrix-idle-2026-10-27'
COMMIT='d2ca677db32a17eaed9aff776244262b88d42801'
spec=importlib.util.spec_from_file_location('suite',ROOT/'scripts/s1-suite.py');c=importlib.util.module_from_spec(spec);spec.loader.exec_module(c)
lock=(STATE/'controller.lock').open('w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
subprocess.run(['git','diff','--exit-code',COMMIT,'--','loadgen','experiments','scripts','support','deploy','datasets'],check=True)
provenance=json.loads((STATE/'provenance.json').read_text());assert provenance['source_commit']==COMMIT
for path,want in provenance['binaries'].items():
    assert hashlib.sha256((ROOT/path).read_bytes()).hexdigest()==want
manifest=json.loads((ROOT/'datasets/stats/catalog.json').read_text());design=json.loads((STATE/'design.json').read_text());assert design['source_commit']==COMMIT
c.EVIDENCE_ROOT=Path(design['evidence_root'])
plan_path=STATE/'supplement-rounds.json';plans=json.loads(plan_path.read_text()) if plan_path.exists() else []

def documents():
    rows=[]
    for p in OUT.glob('*.done.json'):
        stem=p.name.removesuffix('.done.json');d=json.loads((OUT/(stem+'.json')).read_text());assert d['git_commit']==COMMIT
        audit=json.loads((OUT/(stem+'.correctness.json')).read_text())
        if d['result']['valid'] and not audit['ok']:raise RuntimeError('valid run failed source audit')
        rows.append(d)
    return rows

def selected(rows,story,ratio,rate):return [d for d in rows if d['story']==story and d['config']['read_ratio']==ratio and d['config']['rate']==rate]
def sets(rows,arms):return {a:{d['repeat'] for d in rows if d['arm']==a and d['result']['valid']} for a in arms}
def sufficient(rows,arms):
    ids=sets(rows,arms)
    pairs=[('A',a) for a in arms if a!='A']
    if 'B-off' in arms:pairs.append(('B','B-off'))
    return all(len(v)>=5 for v in ids.values()) and all(len(ids[a]&ids[b])>=5 for a,b in pairs)

def perform(mac,plan):
    story,ratio,rate,repeat=plan['story'],plan['ratio'],plan['rate'],plan['repeat']
    for arm in plan['order']:
        c.one(mac,COMMIT,manifest,OUT,arm,ratio,rate,repeat,max(20,math.ceil(12000/(rate*ratio))),10,story,story=='S1-bypass')
    plan['completed']=True;c.save(plan_path,plans)

c.start_redis();c.stop_e1();c.restore();mac=c.Mac(COMMIT)
c.save(STATE/'supplement-client-environment.json',mac.environment | {'source_commit':COMMIT})
try:
    for plan in plans:
        if not plan.get('completed'):perform(mac,plan)
    conditions=[('S1',ratio,rate,['A','B','C','D']) for ratio in [.95,.99] for rate in design['rates']]
    conditions += [('S1-reverse',.95,design['rates'][-1],['A','B-off','B']),('S1-bypass',.95,design['rates'][-1],['A','B','C','D'])]
    for story,ratio,rate,arms in conditions:
        while True:
            rows=selected(documents(),story,ratio,rate)
            if sufficient(rows,arms):break
            repeat=max([5]+[d['repeat'] for d in rows])+1
            order=arms if repeat%2 else list(reversed(arms))
            plan=dict(story=story,ratio=ratio,rate=rate,repeat=repeat,order=order,completed=False,
                      previous_valid_repeats={a:sorted(v) for a,v in sets(rows,arms).items()})
            plans.append(plan);c.save(plan_path,plans);c.event('supplement-planned',**plan)
            perform(mac,plan)
    c.event('supplement-complete',rounds=len(plans))
finally:
    c.stop_e1();mac.close()
