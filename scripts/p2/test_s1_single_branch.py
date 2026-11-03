import gzip,json,tempfile,unittest
from pathlib import Path
from unittest import mock

import s1_single_branch as controller
import s1_single_branch_report as report


class ControllerConfigTests(unittest.TestCase):
 def test_two_named_arms_and_alternating_order(self):
  self.assertEqual(controller.ARMS,('B-gate-only','B-write-only'))
  self.assertEqual(controller.arm_order(1),('B-gate-only','B-write-only'))
  self.assertEqual(controller.arm_order(2),('B-write-only','B-gate-only'))
 def test_arm_switches_are_orthogonal(self):
  gate=controller.CONFIG['B-gate-only'];write=controller.CONFIG['B-write-only']
  self.assertEqual((gate['mode'],gate['gated_aside'],gate['ungated_fills'],gate['write_path']),('cache-aside',True,False,'delete'))
  self.assertEqual((write['mode'],write['gated_aside'],write['ungated_fills'],write['write_path']),('versioned',False,True,'plain-fill'))
 def test_new_arm_metadata_passes_existing_limits(self):
  for arm in controller.ARMS:
   name=controller.stem(arm,1)
   self.assertTrue(name.replace('-','').replace('_','').isalnum(),name)
   self.assertLessEqual(len(arm),64)
   self.assertLessEqual(len(controller.STORY),32)
   self.assertNotIn(arm,('A','B','B-off','C','CV','D'))
 def test_path_counters_distinguish_the_branches(self):
  gate=dict(bcl_e1_cache_sets_total=0,bcl_e1_cache_deletes_total=10,bcl_e1_cache_fills_applied_total=90,
            bcl_e1_cache_fills_rejected_total=3,bcl_e1_source_full_reads_total=90,bcl_e1_source_writes_total=10)
  write=dict(bcl_e1_cache_sets_total=100,bcl_e1_cache_deletes_total=0,bcl_e1_cache_fills_applied_total=0,
             bcl_e1_cache_fills_rejected_total=0,bcl_e1_source_full_reads_total=90,bcl_e1_source_writes_total=10)
  self.assertTrue(controller.path_ok('B-gate-only',gate));self.assertTrue(controller.path_ok('B-write-only',write))
  self.assertFalse(controller.path_ok('B-gate-only',dict(gate,bcl_e1_cache_sets_total=1)))
  self.assertFalse(controller.path_ok('B-gate-only',dict(gate,bcl_e1_cache_deletes_total=9)))
  self.assertFalse(controller.path_ok('B-write-only',dict(write,bcl_e1_cache_deletes_total=1)))
  self.assertFalse(controller.path_ok('B-write-only',dict(write,bcl_e1_cache_fills_applied_total=1)))
 def test_trace_directory_is_the_same_mount_and_archive_source(self):
  trace=controller.round_trace_dir(Path('/tmp/results'),'round',stamp=123)
  args=controller.service_args('B-gate-only','round',trace,Path('/tmp/service.env'),42)
  mount=f'{trace}:/trace'
  self.assertEqual(args[args.index('-v')+1],mount)
  self.assertIn('GUIDE_TRACE_FILE=/trace/events.jsonl',args)
  self.assertTrue(mount.startswith(str(controller.STATE/'traces'/'results'/'round-123')))
 def test_archive_consumes_the_same_trace_directory(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory);trace=root/'round-1';trace.mkdir();(trace/'events.jsonl').write_text('{}\n')
   target=root/'round.trace.jsonl.gz'
   with mock.patch.object(controller.legacy,'archive_trace') as mocked,mock.patch.object(controller.subprocess,'check_output',return_value=b'{}\n') as read_audit:
    controller.archive_trace(trace,target)
   mocked.assert_called_once_with(trace,target);read_audit.assert_called_once()
   self.assertEqual((root/'round.audit.jsonl.gz').read_bytes(),gzip.compress(b'{}\n',mtime=0))


def fixture(root,story,arm,repeat,commit='a'*40,stale=1,reads=100,client_gz=False,config_patch=None,client_valid=True,
            correctness_ok=True,path_ok=True,counters_patch=None,truth_valid=True,truth_client_valid=True,source_audit_ok=True):
 gate=arm in ('B-gate-only','A')
 counters={'bcl_e1_cache_sets_total':0 if gate else 100,'bcl_e1_cache_deletes_total':10 if gate else 0,
           'bcl_e1_cache_fills_applied_total':90 if gate else 0,'bcl_e1_cache_fills_rejected_total':0,
           'bcl_e1_source_full_reads_total':90,'bcl_e1_source_writes_total':10}
 if counters_patch:counters.update(counters_patch)
 truth=dict(story=story,arm=arm,repeat=repeat,source_commit=commit,client_valid=truth_client_valid,valid=truth_valid,source_audit_ok=source_audit_ok,
            stale=stale,successful_formal_reads=reads,stale_ratio=stale/reads,fill_delay='50ms')
 if story==report.STORY_NEW:
  truth.update(path_counters_ok=path_ok,write_path='delete' if gate else 'plain-fill')
 client=dict(story=story,arm=arm,repeat=repeat,git_commit=commit,data_version=report.DESIGN['data_version'],
             config=dict(read_ratio=.95,rate=400,warmup='10s',window='32s',timeout='10s',max_inflight=1536,
                         connections_per_target=256,prime_connections=128,seed=20261026+repeat,bypass_writes=False),
             result=dict(valid=client_valid,server=dict(counters_delta=counters)))
 if config_patch:client['config'].update(config_patch)
 name=f'{story}-{arm}-p95-q400-r{repeat}'
 (root/(name+'.a2.json.gz')).write_bytes(gzip.compress(json.dumps(truth).encode(),mtime=0))
 path=root/(name+('.json.gz' if client_gz else '.json'))
 data=json.dumps(client).encode();path.write_bytes(gzip.compress(data,mtime=0) if client_gz else data)
 (root/(name+'.correctness.json')).write_text(json.dumps(dict(ok=correctness_ok,keys=10,acknowledged_writes=10)))
 (root/(name+'.trace.jsonl.gz')).write_bytes(gzip.compress(b'\n'.join([json.dumps(dict(event='fill')).encode(),json.dumps(dict(event='delete')).encode()]),mtime=0))
 return truth,client


def write_new(root,**kw):
 for arm in report.NEW_ARMS:
  for repeat in range(1,6):fixture(root,report.STORY_NEW,arm,repeat,**kw)


class OfflineReportTests(unittest.TestCase):
 def test_five_round_summary_uses_pooled_and_per_round_ratios(self):
  records={arm:{repeat:dict(stale=repeat,reads=100,stale_ratio=repeat/100,valid=True,source_commit='a'*40,
                            counters=dict(full_reads=10)) for repeat in range(1,6)} for arm in report.NEW_ARMS}
  rows=report.summarize(records,report.NEW_ARMS)
  row=rows[0]
  self.assertEqual((row['stale'],row['reads'],row['pooled_stale_ratio']),(15,500,.03))
  self.assertEqual(row['per_round_stale_ratio'],dict(median=.03,min=.01,max=.05))
  self.assertEqual(row['source_commits'],['a'*40])
 def test_collect_accepts_plain_and_gzip_clients(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory)
   for repeat in range(1,6):fixture(root,report.STORY_NEW,report.NEW_ARMS[0],repeat,stale=repeat)
   for repeat in range(1,6):fixture(root,report.STORY_NEW,report.NEW_ARMS[1],repeat,stale=repeat,client_gz=True)
   records=report.collect(root,report.NEW_ARMS,report.STORY_NEW,require_path=True)
   self.assertEqual([records[a][1]['stale'] for a in report.NEW_ARMS],[1,1])
   rows=report.summarize(records,report.NEW_ARMS)
   self.assertTrue(all(r['rounds']==5 for r in rows))
 def test_missing_round_is_rejected(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory)
   for repeat in range(1,5):fixture(root,report.STORY_NEW,report.NEW_ARMS[0],repeat)
   with self.assertRaises(RuntimeError):report.collect(root,(report.NEW_ARMS[0],),report.STORY_NEW)
 def test_design_drift_is_rejected(self):
  for patch in [dict(rate=399),dict(read_ratio=.99),dict(warmup='9s'),dict(window='31s'),dict(seed=1),dict(timeout='9s')]:
   with tempfile.TemporaryDirectory() as directory:
    root=Path(directory);write_new(root)
    fixture(root,report.STORY_NEW,report.NEW_ARMS[0],1,config_patch=patch)
    with self.assertRaises(RuntimeError):report.collect(root,report.NEW_ARMS,report.STORY_NEW,require_path=True)
 def test_invalid_client_correctness_or_truth_is_rejected(self):
  cases=[dict(client_valid=False),dict(correctness_ok=False),dict(truth_valid=False),dict(truth_client_valid=False),dict(source_audit_ok=False)]
  for kw in cases:
   with tempfile.TemporaryDirectory() as directory:
    root=Path(directory);write_new(root)
    fixture(root,report.STORY_NEW,report.NEW_ARMS[0],1,**kw)
    with self.assertRaises(RuntimeError):report.collect(root,report.NEW_ARMS,report.STORY_NEW,require_path=True)
 def test_wrong_path_counters_are_rejected(self):
  for arm,patch in [('B-gate-only',dict(bcl_e1_cache_sets_total=1)),('B-write-only',dict(bcl_e1_cache_deletes_total=1))]:
   with tempfile.TemporaryDirectory() as directory:
    root=Path(directory);write_new(root)
    fixture(root,report.STORY_NEW,arm,1,counters_patch=patch)
    with self.assertRaises(RuntimeError):report.collect(root,report.NEW_ARMS,report.STORY_NEW,require_path=True)
 def test_mixed_source_commits_are_rejected(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory);write_new(root)
   fixture(root,report.STORY_NEW,report.NEW_ARMS[0],1,commit='b'*40)
   with self.assertRaises(RuntimeError):report.collect(root,report.NEW_ARMS,report.STORY_NEW,require_path=True)
 def test_expected_source_commit_must_match(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory);write_new(root)
   with self.assertRaises(RuntimeError):report.load_batch(root,report.NEW_ARMS,report.STORY_NEW,expected_source='b'*40,require_path=True)
 def test_reference_design_drift_is_rejected(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory)
   for repeat in range(1,6):fixture(root,report.STORY_REFERENCE,'A',repeat,commit='a00e151'+'0'*33)
   fixture(root,report.STORY_REFERENCE,'A',1,commit='a00e151'+'0'*33,config_patch=dict(window='31s'))
   with self.assertRaises(RuntimeError):report.load_batch(root,('A',),report.STORY_REFERENCE,source_prefix='a00e151')
 def test_reference_source_prefix_is_enforced(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory)
   for repeat in range(1,6):fixture(root,report.STORY_REFERENCE,'A',repeat,commit='b'*40)
   with self.assertRaises(RuntimeError):report.load_batch(root,('A',),report.STORY_REFERENCE,source_prefix='a00e151')
 def test_public_reference_rounds_are_readable(self):
  reference=controller.ROOT/'docs/measurements/2026-10-28-s1-v2/raw/race'
  if not reference.exists():self.skipTest('public S1 archive not present')
  rows=report.load_batch(reference,report.REFERENCE_ARMS,report.STORY_REFERENCE,source_prefix='a00e151')
  self.assertEqual(len(rows),3)
  self.assertTrue(all(r['rounds']==5 and r['reads']>0 and r['stale']>=0 for r in rows))
  self.assertGreater(next(r for r in rows if r['arm']=='A')['stale'],0)
  self.assertTrue(all(r['source_commits']==['a00e15101716f21cd674662e74210130719a0bcc'] for r in rows))


if __name__=='__main__':unittest.main()
