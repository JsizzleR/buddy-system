#!/usr/bin/env python3
"""Opt-in installed Codex adapter qualification with a deterministic local provider.

Builds only temporary binaries; all state and exact-hash hook trust are confined
to a new /private/tmp directory. No global installation or real inference.
Claude/provider/mixed-fleet/recovery qualification are separate gates.
"""
import argparse, importlib.util, pathlib, tempfile, json, subprocess, http.server, threading, queue, time, hashlib, os, sys
_ap=argparse.ArgumentParser(description=__doc__);_ap.add_argument('--codex',help='the codex binary to qualify (default: codex on PATH)');_args=_ap.parse_args()
sys.dont_write_bytecode=True
repo=pathlib.Path(__file__).resolve().parent.parent
spec=importlib.util.spec_from_file_location('fixture',repo/'scripts/qualify-codex.py'); fixture=importlib.util.module_from_spec(spec);spec.loader.exec_module(fixture)
fixture.exit_on_signals()  # F-35: TERM/HUP still run the finally below
# D-080: the binary this run was given is its candidate; every launch below is bound to it.
binding=fixture.bind_codex(_args.codex or fixture.CODEX)
root=pathlib.Path(tempfile.mkdtemp(prefix='buddy-step2-native-',dir='/private/tmp'));work=root/'work';work.mkdir();state=root/'codex';state.mkdir();bindir=root/'bin';bindir.mkdir()
env=fixture.clean_env();buildenv=dict(env,GOCACHE='/private/tmp/buddy-codex-go-cache',GIT_OPTIONAL_LOCKS='0')
buddy=bindir/'buddy';adapter=bindir/'buddy-adapter';receipt=root/'worker.json'
fixture.run(['go','build','-o',str(buddy),'./cmd/buddy'],cwd=repo,env=buildenv,timeout=600)
fixture.run(['go','build','-o',str(adapter),'./cmd/buddy-adapter'],cwd=repo,env=buildenv,timeout=600)
fixture.run(['git','init','-q',str(work)],cwd=root,env=env)
fixture.run([str(buddy),'init'],cwd=work,env=env)
fixture.run([str(buddy),'hello','--session','holder'],cwd=work,env=env)
fixture.run([str(buddy),'claim','held','--session','holder','--scope','guarded.txt','--desc','native fixture only'],cwd=work,env=env)
(work/'guarded.txt').write_text('original\n')
server=http.server.HTTPServer(('127.0.0.1',0),fixture.Fixture);server.requests=[];server.patches=[fixture.DENY_PATCH,fixture.ALLOW_PATCH]
quote=lambda s:"'"+str(s).replace("'","'\"'\"'")+"'"
gate=quote(adapter)+' hook --receipt '+quote(receipt)+' --buddy '+quote(buddy)
config=state/'config.toml'
base=fixture.config_text(server.server_address[1],root/'unused.py').replace(json.dumps(fixture.shlex.quote(fixture.PYTHON)+' '+fixture.shlex.quote(str(root/'unused.py'))),json.dumps(gate))
config.write_text(base)
hooks=fixture.list_hooks(state,work,env);(root/'hooks-untrusted.json').write_text(json.dumps(hooks,indent=2));h=hooks['data'][0]['hooks'][0]
fixture.require(h['trustStatus']=='untrusted','hook must be discovered UNTRUSTED before trust: '+json.dumps(hooks))
config.write_text(base+'\n[hooks.state.'+json.dumps(h['key'])+']\ntrusted_hash = '+json.dumps(h['currentHash'])+'\n')
hooks=fixture.list_hooks(state,work,env);(root/'hooks-trusted.json').write_text(json.dumps(hooks,indent=2));h=hooks['data'][0]['hooks'][0];fixture.require(h['trustStatus']=='trusted','exact-hash trust did not take: '+json.dumps(hooks))
launch={'worktree':str(work),'permission_mode':'workspace-write','hook_config':str(config),'harness_version':fixture.CODEX_VERSION,'gate_command':gate,'gate_hash':h['currentHash'],'hooks':{'configured':'observed','trusted':'observed','observed':'unknown','no_mods':'unknown'}}
(root/'launch.json').write_text(json.dumps(launch,indent=2))
threading.Thread(target=server.serve_forever,daemon=True).start()
events=[];q=queue.Queue();checks={}
p=subprocess.Popen(fixture.candidate_flags([str(adapter),'--harness','codex','--executable',fixture.CODEX,'--buddy',str(buddy),'--launch',str(root/'launch.json'),'--receipt',str(receipt),'--label','adapter-native-fixture']),cwd=work,env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=(root/'adapter.stderr').open('w'),text=True)
def reader():
 for line in p.stdout:
  try:v=json.loads(line)
  except ValueError:continue
  events.append(v);q.put(v)
threading.Thread(target=reader,daemon=True).start()
def wait(pred):
 until=time.monotonic()+40
 while time.monotonic()<until:
  try:v=q.get(timeout=.2)
  except queue.Empty:
   if p.poll()!=None:raise RuntimeError('adapter exited '+str(p.returncode)+' '+(root/'adapter.stderr').read_text())
   continue
  if v.get('error'):raise RuntimeError(v)
  if pred(v):return v
 raise RuntimeError('adapter timeout')
def send(obj):p.stdin.write(json.dumps(obj)+'\n');p.stdin.flush()
try:
 start=wait(lambda v:v.get('operation')=='start');ident=start['identity'];checks['captured_native_identity']=ident['session_id']!='' and ident['incarnation']!=''
 sender=json.loads(fixture.run([str(buddy),'lifecycle','begin','--session','fixture-coordinator'],cwd=work,env=env).stdout)
 queued=json.loads(fixture.run([str(buddy),'adapter','queue','--session',sender['session_id'],'--incarnation',sender['incarnation'],'--to',ident['session_id'],'--to-incarnation',ident['incarnation'],'--body','Fixture assignment remains peer data.'],cwd=work,env=env).stdout);mid=queued['delivery']['message_id']
 request={'schema':'buddy.control.v1','id':'wake-1','operation':'send','identity':ident,'message_id':mid}
 send(request);accepted=wait(lambda v:v.get('request_id')=='wake-1');checks['native_wake_accepted']=accepted['wake_delivery']['state']=='delivered'
 result=wait(lambda v:v.get('native_event',{}).get('kind')=='result');checks['native_result_separate']=result['completion']['state']=='unknown' and result['native_event']['result_ref']!=''
 idle=wait(lambda v:v.get('native_event',{}).get('kind')=='idle');checks['native_idle_translated']=idle['state']['detail']=='idle'
 checks['conflict_patch_denied']=(work/'guarded.txt').read_text()=='original\n' and not (work/'collateral.txt').exists()
 checks['positive_patch_allowed']=(work/'allowed.txt').read_text()=='allowed-control\n'
 inbox=json.loads(fixture.run([str(buddy),'adapter','inbox','--session',ident['session_id'],'--incarnation',ident['incarnation']],cwd=work,env=env).stdout)
 checks['assignment_still_queued_after_result']=inbox['messages']['count']==1
 request['id']='wake-duplicate';send(request);wait(lambda v:v.get('request_id')=='wake-duplicate');checks['duplicate_wake_not_replayed']=len(server.requests)==3
 delivery=json.loads(fixture.run([str(buddy),'adapter','delivered','--session',ident['session_id'],'--incarnation',ident['incarnation'],'--message',str(mid)],cwd=work,env=env).stdout)['delivery']
 checks['body_delivery_explicit']=delivery['state']=='delivered' and delivery['acknowledged']['status']=='unknown' and delivery['completed']['status']=='unknown'
 print('ROOT='+str(root),flush=True);print(json.dumps(checks,indent=2),flush=True)
finally:
 p.stdin.close()
 try:p.wait(timeout=10)
 except subprocess.TimeoutExpired:p.terminate();p.wait(timeout=5)
 server.shutdown()
 (root/'events.json').write_text(json.dumps(events,indent=2))
 result={'root':str(root),'checks':checks,'passed':all(checks.values()) and len(checks)==9,'adapter_exit':p.returncode,'buddy_sha256':hashlib.sha256(buddy.read_bytes()).hexdigest(),'adapter_sha256':hashlib.sha256(adapter.read_bytes()).hexdigest(),'fixture_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'native_version':fixture.CODEX_VERSION,'qualification_candidates':[binding],'codex_sha256':binding['sha256'],'provider':'deterministic loopback fixture; no real inference'}
 (root/'result.json').write_text(json.dumps(result,indent=2));print('RECEIPT='+str(root/'result.json'),flush=True)
# Not `assert`: under `python -O` a failed qualification exited 0 (F-39).
fixture.require(result['passed'],json.dumps(result))
