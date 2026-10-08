"""Summarize the new CSV; run from repository root after rule_bench completes."""
import csv
import hashlib
import json
import platform
import statistics
import subprocess
from datetime import datetime, timezone
from pathlib import Path

csv_path = Path('docs/measurements/2026-10-08-e3-batch.csv')
rows = list(csv.DictReader(csv_path.open()))
assert len(rows) == 240, 'expected six scenarios x four modes x ten rounds'
groups = {}
for row in rows:
    key = (row['scenario'], int(row['documents']), row['mode'])
    groups.setdefault(key, []).append(row)
summary = []
for (scenario, size, mode), rounds in sorted(groups.items()):
    assert len(rounds) == 10
    assert sorted(int(r['repeat']) for r in rounds) == list(range(10))
    reference = groups[(scenario, size, 'string_scalar')][0]
    assert all((r['matches'], r['index_checksum']) ==
               (reference['matches'], reference['index_checksum']) for r in rounds)
    times = [float(r['ns_per_document']) for r in rounds]
    baseline = statistics.median(float(r['ns_per_document']) for r in groups[(scenario, size, 'string_scalar')])
    summary.append(dict(scenario=scenario, documents=size, mode=mode,
                        median_ns_per_document=statistics.median(times),
                        min_ns_per_document=min(times), max_ns_per_document=max(times),
                        speedup_vs_string=baseline/statistics.median(times)))
source_files = list(Path('experiments/e3-rule-engine').glob('*.hpp')) + list(Path('experiments/e3-rule-engine').glob('*.cpp'))
source_files += [Path('experiments/e3-rule-engine/CMakeLists.txt'), Path(__file__)]
report = dict(generated_at=datetime.now(timezone.utc).isoformat(),
              base_commit=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),
              source_dirty=bool(subprocess.check_output(['git','status','--porcelain'],text=True)),
              system=platform.platform(),
              compiler=subprocess.check_output(['c++','--version'],text=True).splitlines()[0],
              cmake=subprocess.check_output(['cmake','--version'],text=True).splitlines()[0],
              cpu=subprocess.check_output(['lscpu'],text=True),
              build_type='Release', timing='steady_clock; ns/document; median of ten round averages, not request p95',
              interference='sanitizer build/tests overlapped initial measurement; shared VM, no CPU isolation',
              release_ctest='2/2 passed; original 222 plus 19568 differential/batch checks',
              sanitizer_ctest='ASan/UBSan Debug 2/2 passed; detect_leaks=0',
              source_sha256={str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(source_files)},
              csv_sha256=hashlib.sha256(csv_path.read_bytes()).hexdigest(), summary=summary)
Path('docs/measurements/2026-10-08-e3-batch.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
for result in summary:
    if result['mode'] in ('enum_scalar','enum_batch'):
        print(f"{result['scenario']} N={result['documents']} {result['mode']}: "
              f"{result['median_ns_per_document']:.2f} ns/doc, {result['speedup_vs_string']:.2f}x string")
