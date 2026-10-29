"""A.2 exact commit-return truth from a single Linux boot; no cross-clock joins."""
import bisect,collections,csv,gzip,json
from pathlib import Path

def calculate(paths,raw_csv,run):
 events=[]
 for path in paths:
  path=Path(path);data=gzip.decompress(path.read_bytes()) if path.suffix=='.gz' else path.read_bytes()
  events.extend(json.loads(l) for l in data.splitlines())
 boots={e['boot_id'] for e in events};assert len(boots)==1,'mixed boot clocks'
 assert all(e.get('session') and e.get('schema')==1 for e in events),'missing session/schema'
 with gzip.open(raw_csv,'rt') as f:raw=list(csv.DictReader(f))
 sql_begins={};begins={};ends={};commits=collections.defaultdict(list);writes={};window_reads=[];invalid=[]
 for e in events:
  if e['event']=='commit_call_begin':sql_begins[(e.get('run'),e.get('request'))]=e
  if e['event']=='commit_error':invalid.append('database commit returned error or unknown outcome')
  if e['event']=='commit_return':
   assert e.get('version',0)>0
   commits[e['id']].append((e['t_ns'],e['version']))
   key=(e.get('run'),e.get('request'))
   if key in writes:invalid.append('duplicate committed request')
   writes[key]=e
  if e['event'] not in ('read_begin','read_return','read_error'):continue
  key=(e['session'],e['op'])
  if e['event']=='read_begin':
   if key in begins:invalid.append('duplicate read begin')
   begins[key]=e
  else:
   if key in ends:invalid.append('duplicate read end')
   ends[key]=e
 for key,e in begins.items():
  end=ends.get(key)
  if end is None:invalid.append('missing read end');continue
  if end['t_ns']<e['t_ns'] or e['id']!=end['id']:invalid.append('bad read interval')
  if e.get('phase')=='window' and e.get('run')==run:window_reads.append((e,end))
 if set(ends)-set(begins):invalid.append('read end without begin')
 timelines={}
 for id,changes in commits.items():
  times=[];versions=[];at=[];highest=1;highest_at=0
  for t,v in sorted(changes):
   if v>highest:highest=v;highest_at=t
   times.append(t);versions.append(highest);at.append(highest_at)
  timelines[id]=(times,versions,at)
 stale=0;ages=[];examples=[];seen=set();latencies=[]
 for begin,end in window_reads:
  request=begin.get('request','')
  try:i=int(request);record=raw[i]
  except (ValueError,IndexError):invalid.append('request/CSV mapping missing');continue
  if i in seen:invalid.append('duplicate formal request')
  seen.add(i)
  if record['window']!='1' or record['write']!='0' or record['sent']!='1' or record['status']!='200' or record['err'] or str(record['id'])!=begin['id'] or int(record['version'])!=end.get('version') or end['event']!='read_return':invalid.append('formal read does not match raw successful response');continue
  latest=1;time_of_latest=0
  if begin['id'] in timelines:
   times,versions,at=timelines[begin['id']];j=bisect.bisect_left(times,begin['t_ns'])-1
   if j>=0:latest=versions[j];time_of_latest=at[j]
  latencies.append(max(1,(end['t_ns']-begin['t_ns'])//1000))
  if end['version']<latest:
   stale+=1;ages.append(max(1,(begin['t_ns']-time_of_latest)//1000))
   if len(examples)<10:examples.append({'id':begin['id'],'request':request,'read_begin_ns':begin['t_ns'],'commit_return_ns':time_of_latest,'returned':end['version'],'committed_before_begin':latest})
 expected=set()
 for i,r in enumerate(raw):
  if r['sent']!='1' or r['err'] or r['status']!='200':continue
  if r['write']=='1':
   event=writes.get((run,str(i)))
   if event is None or event['id']!=r['id'] or event.get('version')!=int(r['version']):invalid.append('missing or mismatched commit-return log')
   call=sql_begins.get((run,str(i)))
   if call is None or event is None or call['id']!=event['id'] or call['t_ns']>event['t_ns']:invalid.append('missing SQL begin or invalid commit bracket')
  elif r['window']=='1':expected.add(i)
 if expected!=seen:invalid.append('all-key formal read coverage mismatch')
 return {'valid':not invalid,'invalid_reasons':sorted(set(invalid)),'definition':'returned version < maximum same-id version with commit_return_ns strictly before server read_begin_ns','clock_boot':next(iter(boots)),'run':run,'successful_formal_reads':len(expected),'logged_formal_reads':len(window_reads),'unique_formal_read_keys':len({b['id'] for b,e in window_reads}),'commits':sum(len(c) for c in commits.values()),'stale':stale,'stale_ratio':stale/len(expected) if expected else None,'examples':examples,'stale_age_us':ages,'service_core_read_us':latencies}
