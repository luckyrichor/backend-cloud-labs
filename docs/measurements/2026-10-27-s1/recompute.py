#!/usr/bin/env python3
"""Recompute S1 scalar statistics from original JSON/HdrHistogram and gzip CSV."""
import argparse
import collections
import csv
import gzip
import hashlib
import json
import math
from pathlib import Path
import statistics
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
EVIDENCE = Path(__file__).resolve().parent
SOURCE = 'd2ca677db32a17eaed9aff776244262b88d42801'
METRICS = {
    'server_p50_us': lambda r: r['server_get_us']['p50_us'],
    'server_p95_us': lambda r: r['server_get_us']['p95_us'],
    'server_p99_us': lambda r: r['server_get_us']['p99_us'],
    'client_p50_us': lambda r: r['client_get_us']['p50_us'],
    'client_p95_us': lambda r: r['client_get_us']['p95_us'],
    'client_p99_us': lambda r: r['client_get_us']['p99_us'],
    'stale_ratio': lambda r: r['stale_ratio'],
    'source_qps': lambda r: r['source_reads_per_second'],
    'cache_hit_ratio': lambda r: r['cache_hit_ratio'],
    'client_cache_served_ratio': lambda r: r['client_cache_served_ratio'],
}
COMPARE = ['server_p99_us', 'client_p99_us', 'stale_ratio', 'source_qps']


def read_bytes(path):
    data = path.read_bytes()
    return gzip.decompress(data) if path.suffix == '.gz' else data


def read_sidecar(root, stem, suffix):
    for ending in ['.json', '.json.gz']:
        path = root / (stem + suffix + ending)
        if path.exists():
            return json.loads(read_bytes(path))
    raise FileNotFoundError(str(root / (stem + suffix + '.json')))


def stats(values):
    if not values:
        return dict(n=0, median=None, min=None, max=None)
    if not all(isinstance(v, (int, float)) and math.isfinite(v) for v in values):
        raise ValueError('non-finite or missing metric')
    return dict(n=len(values), median=statistics.median(values), min=min(values), max=max(values))


def assess(base, variant):
    result = dict(baseline=base, variant=variant)
    if min(base['n'], variant['n']) < 5:
        return dict(result, label='不确定', reason='不足五个共同有效重复')
    if base['median'] == 0:
        return dict(result, label='不确定', reason='基线中位数为零，不能定义相对提升')
    reduction = (base['median'] - variant['median']) / base['median']
    result['relative_reduction'] = reduction
    if abs(reduction) <= .1:
        return dict(result, label='无显著提升', reason='相对变化不超过10%')
    if not (variant['max'] < base['min'] or base['max'] < variant['min']):
        return dict(result, label='无显著提升', reason='重复的最小/最大范围重叠')
    return dict(result, label='显著', direction='改善' if reduction > 0 else '退化')


def derive(inputs, summary):
    rows = []
    for root in inputs:
        with tempfile.TemporaryDirectory(prefix='s1-json-') as directory:
            temporary = Path(directory)
            originals = {}
            for path in sorted(root.iterdir()):
                if not (path.name.endswith('.json') or path.name.endswith('.json.gz')):
                    continue
                raw = read_bytes(path)
                document = json.loads(raw)
                if not isinstance(document.get('result'), dict) or 'git_commit' not in document:
                    continue
                if document['git_commit'] != SOURCE:
                    raise ValueError('mixed source versions: ' + str(path))
                name = path.name.removesuffix('.gz')
                if name in originals:
                    raise ValueError('duplicate compressed/plain result: ' + name)
                originals[name] = (document, raw)
                (temporary / name).write_bytes(raw)
            scalars = json.loads(subprocess.check_output([str(summary), str(temporary)], text=True))
            for row in scalars:
                name = row['file']; stem = name.removesuffix('.json')
                document, raw = originals[name]
                audit = read_sidecar(root, stem, '.correctness')
                done = read_sidecar(root, stem, '.done')
                if done['source_commit'] != SOURCE:
                    raise ValueError('completion source mismatch: ' + name)
                if row['valid'] and not audit['ok']:
                    raise ValueError('valid client failed source audit: ' + name)
                errors = collections.Counter()
                with gzip.open(root / (stem + '.csv.gz'), 'rt') as stream:
                    for record in csv.DictReader(stream):
                        if record['err']:
                            phase = 'measured' if record['window'] == '1' else 'warmup'
                            errors[phase + ':' + record['err']] += 1
                result = document['result']
                strategy = result['server'].get('strategy')
                if row['valid']:
                    expected = {'A': 'cache-aside', 'B': 'versioned', 'B-off': 'versioned', 'C': 'strict', 'D': 'cache-first'}
                    if strategy is None or strategy.get('source') != 'mysql' or strategy.get('mode') != expected[row['arm']] or strategy.get('ungated_fills') != (row['arm'] == 'B-off') or strategy.get('ttl') != '1m0s':
                        raise ValueError('runtime source/strategy/TTL mismatch: ' + name)
                row['runtime_strategy'] = strategy
                successes = result['reads']['Success']
                row.update(file=root.name + '/' + name + '.gz', raw_json_sha256=hashlib.sha256(raw).hexdigest(),
                           source_audit_ok=audit['ok'], errors=dict(sorted(errors.items())),
                           client_cache_served_ratio=result['reads_served_from_cache'] / successes if successes else 0,
                           observed_send_rate=result['observed_window_send_rate'],
                           source_window_seconds_approx=result['server'].get('window_seconds_approx'),
                           client=document['client'], config=document['config'])
                rows.append(row)
    rows.sort(key=lambda r: (r['story'], r['ratio'], r['rate'], r['arm'], r['repeat']))
    grouped = collections.defaultdict(list)
    for row in rows:
        grouped[(row['story'], row['rate'], row['ratio'], row['arm'])].append(row)
    groups = []; quality = []
    for (story, rate, ratio, arm), attempts in sorted(grouped.items()):
        good = [r for r in attempts if r['valid']]
        if len({r['repeat'] for r in good}) != len(good):
            raise ValueError('duplicate eligible repeat')
        key = dict(story=story, rate=rate, ratio=ratio, arm=arm)
        errors = collections.Counter()
        for attempt in attempts:
            errors.update(attempt['errors'])
        quality.append(dict(key, attempts=len(attempts), valid=len(good), invalid=len(attempts)-len(good),
                            read_requests=sum(r['reads'] for r in attempts),
                            write_requests=sum(r['writes'] for r in attempts),
                            failed_reads=sum(r['read_failed'] for r in attempts),
                            failed_writes=sum(r['write_failed'] for r in attempts),
                            measured_dropped=sum(r['measured_dropped'] for r in attempts),
                            warmup_dropped=sum(r['warmup_dropped'] for r in attempts),
                            error_classes=dict(sorted(errors.items()))))
        if good:
            groups.append(dict(key, repeats=len(good), repeat_ids=sorted(r['repeat'] for r in good),
                               files=[r['file'] for r in good], reads_total=sum(r['reads'] for r in good),
                               stale_total=sum(r['stale_reads'] for r in good),
                               metrics={m: stats([extract(r) for r in good]) for m, extract in METRICS.items()}))
    comparisons = []
    for (story, rate, ratio, arm), attempts in sorted(grouped.items()):
        if arm == 'A' or story == 'S1-profile':
            continue
        baselines = ['A'] + (['B'] if arm == 'B-off' else [])
        for baseline in baselines:
            base = {r['repeat']: r for r in grouped.get((story, rate, ratio, baseline), []) if r['valid']}
            variant = {r['repeat']: r for r in attempts if r['valid']}
            common = sorted(base.keys() & variant.keys())
            comparisons.append(dict(story=story, rate=rate, ratio=ratio, arm=arm, versus=baseline,
                                    common_repeat_ids=common,
                                    files_baseline=[base[i]['file'] for i in common],
                                    files_variant=[variant[i]['file'] for i in common],
                                    metrics={m: assess(stats([METRICS[m](base[i]) for i in common]),
                                                       stats([METRICS[m](variant[i]) for i in common]))
                                             for m in COMPARE}))
    return dict(completed_attempts=len(rows), valid_attempts=sum(r['valid'] for r in rows),
                source_commits=sorted({r['source_commit'] for r in rows}),
                data_versions=sorted({r['data_version'] for r in rows}),
                rule='At least five common valid repeat IDs; >10% relative reduction and nonoverlapping min/max ranges. This is an experimental rule, not a confidence interval or p-value. Zero baseline is uncertain.',
                latency_unit='microseconds', stale_definition='Version below highest same-ID write acknowledged before read send; conservative lower bound.',
                source_qps_definition='Source read counter delta / actual snapshot bracket including drain; approximate.',
                cache_hit_definition='Redis hits / (hits+misses); strict still reads the source even on a cache hit.',
                groups=groups, comparisons=comparisons, quality=quality, rows=rows)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, action='append')
    parser.add_argument('--out', type=Path, default=ROOT / '.local/s1-recomputed')
    parser.add_argument('--summary-bin', type=Path)
    args = parser.parse_args()
    inputs = args.input or [EVIDENCE/'matrix-idle-2026-10-27', EVIDENCE/'tx-calibration-2026-10-27']
    if len({p.name for p in inputs}) != len(inputs):
        raise ValueError('input directory names must be unique')
    args.out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='s1-summary-') as directory:
        summary = args.summary_bin.resolve() if args.summary_bin else Path(directory)/'summary'
        if not args.summary_bin:
            subprocess.run(['go','-C',str(ROOT/'loadgen'),'build','-o',str(summary),'./cmd/s1summary'], check=True)
        result = derive(inputs, summary)
    (args.out/'analysis.json').write_text(json.dumps(result, indent=2, sort_keys=True, ensure_ascii=False)+'\n')
    print(json.dumps(dict(completed_attempts=result['completed_attempts'], valid_attempts=result['valid_attempts'],
                          groups=len(result['groups']), output=str(args.out/'analysis.json'))))

if __name__ == '__main__':
    main()
