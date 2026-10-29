import csv,gzip,json,tempfile,unittest
from pathlib import Path
from audit_truth import calculate
class TruthTests(unittest.TestCase):
 def fixture(self,root,read_start=150,commit_time=100):
  def e(event,id,op,t,version=0,request=''):
   return dict(schema=1,boot_id='one-boot',session='one-session',instance='e1',event=event,id=id,op=op,t_ns=t,version=version,run='r',phase='window',request=request)
  events=[e('commit_call_begin','1',0,50,request='0'),e('commit_return','1',0,commit_time,2,'0'),e('read_begin','1',1,read_start,request='1'),e('read_return','1',1,300,1,'1'),e('read_begin','2',2,150,request='2'),e('read_return','2',2,200,1,'2')]
  audit=root/'audit.jsonl';audit.write_text(''.join(json.dumps(x)+'\n' for x in events))
  raw=root/'raw.csv.gz'
  with gzip.open(raw,'wt') as f:
   w=csv.DictWriter(f,fieldnames=['window','write','id','sent','status','version','err']);w.writeheader()
   for r in [dict(window='0',write='1',id='1',sent='1',status='200',version='2',err=''),dict(window='1',write='0',id='1',sent='1',status='200',version='1',err=''),dict(window='1',write='0',id='2',sent='1',status='200',version='1',err='')]:w.writerow(r)
  return audit,raw,events
 def test_two_keys_and_in_flight_ack_independence(self):
  with tempfile.TemporaryDirectory() as d:
   a,r,_=self.fixture(Path(d));result=calculate([a],r,'r');self.assertTrue(result['valid']);self.assertEqual(result['stale'],1);self.assertEqual(result['successful_formal_reads'],2)
 def test_commit_after_start_and_equal_timestamp_do_not_count(self):
  for begin in [75,100]:
   with tempfile.TemporaryDirectory() as d:
    a,r,_=self.fixture(Path(d),read_start=begin);result=calculate([a],r,'r');self.assertEqual(result['stale'],0)
 def test_missing_read_return_is_invalid(self):
  with tempfile.TemporaryDirectory() as d:
   a,r,events=self.fixture(Path(d));a.write_text(''.join(json.dumps(x)+'\n' for x in events[:-1]));self.assertFalse(calculate([a],r,'r')['valid'])
 def test_mixed_host_clocks_are_rejected(self):
  with tempfile.TemporaryDirectory() as d:
   a,r,events=self.fixture(Path(d));events[-1]['boot_id']='other-boot';a.write_text(''.join(json.dumps(x)+'\n' for x in events))
   with self.assertRaises(AssertionError):calculate([a],r,'r')
if __name__=='__main__':unittest.main()
