import importlib.util,json,copy
from pathlib import Path
p=Path('~/Projects/buddy-system/scripts/check-interrupted.py')
spec=importlib.util.spec_from_file_location('checks',p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
r=json.load(open('/private/tmp/buddy-interrupted-b26l88o3/result.json'))
s=r['sessions']['interrupt'];response=next(v for v in s['events'] if v.get('operation')=='reconcile');delivery=response['wake_delivery'];identity=s['identity'];mid=response['message_id']
wire=[json.loads(x) for x in (Path(r['root'])/'interrupt-state/native-observations.jsonl').read_text().splitlines()]
if not m.reconcile_boundary(response,identity,mid,delivery,wire):raise RuntimeError('retained native unsupported receipt rejected')
mutations=[(('error',),''),(('turn_observation',),{'source':'native_thread_read'}),(('message_id',),999),(('identity','incarnation'),'wrong'),(('wake_delivery','turn_id'),'wrong'),(('capabilities','reconcile','support'),'experimental'),(('completion','state'),'completed'),(('acknowledgment','state'),'completed'),(('body_delivery','state'),'delivered')]
count=0
for path,value in mutations:
 damaged=copy.deepcopy(response);cursor=damaged
 for key in path[:-1]:cursor=cursor[key]
 cursor[path[-1]]=value
 if m.reconcile_boundary(damaged,identity,mid,delivery,wire):raise RuntimeError(('accepted',path))
 count+=1
for method in ['thread/read','thread/turns/list']:
 if m.reconcile_boundary(response,identity,mid,delivery,wire+[{'type':'adapter_frame','frame':{'method':method}}]):raise RuntimeError('accepted native query')
 count+=1
lost=copy.deepcopy(response);lost.pop('error');lost['wake_delivery']={'state':'unknown'};lost['turn_observation']={'native_session_id':identity['session_id'],'source':'transport_journal','status':{'state':'unknown'}}
if not m.reconcile_boundary(lost,identity,mid,lost['wake_delivery'],[]):raise RuntimeError('synthetic unknown-turn control refused')
for key,value in [('source','native_thread_read'),('turn_id','guessed'),('native_session_id','wrong')]:
 bad=copy.deepcopy(lost);bad['turn_observation'][key]=value
 if m.reconcile_boundary(bad,identity,mid,lost['wake_delivery'],[]):raise RuntimeError(('accepted lost mutation',key))
 count+=1
print(json.dumps({'retained_native_refusal':'PASS','synthetic_unknown_turn':'PASS','rejected_mutations':count}))
