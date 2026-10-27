import gzip
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('suite',Path(__file__).resolve().parents[1]/'s1-suite.py')
suite=importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)

class TraceEvidenceTests(unittest.TestCase):
    def test_independent_attempts_archive_their_own_event_file(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            for name,version in [('attempt-one',1),('attempt-two',2)]:
                trace=root/name;trace.mkdir()
                rows=[{'op':1,'event':'begin'},{'op':1,'event':'store_read','version':version}]
                raw=('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
                (trace/'events.jsonl').write_bytes(raw)
                target=root/(name+'.gz')
                suite.archive_trace(trace,target)
                self.assertEqual(gzip.decompress(target.read_bytes()),raw)
    def test_mixed_instances_are_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            (root/'events.jsonl').write_text('{"op":1,"event":"begin"}\n'*2)
            with self.assertRaises(RuntimeError):suite.archive_trace(root,root/'out.gz')
            self.assertFalse((root/'out.gz').exists())
    def test_existing_evidence_is_preserved(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            (root/'events.jsonl').write_text('{"op":1,"event":"begin"}\n')
            (root/'out.gz').write_bytes(b'original')
            with self.assertRaises(FileExistsError):suite.archive_trace(root,root/'out.gz')
            self.assertEqual((root/'out.gz').read_bytes(),b'original')

if __name__=='__main__':unittest.main()
