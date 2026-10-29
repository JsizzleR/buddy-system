#!/usr/bin/env python3
"""Installed Codex + real Buddy gate, deterministic loopback Responses fixture.

All state and test-hook trust are confined to a newly created /private/tmp root.
No provider credentials, real inference, hook-trust bypass or global config edits.
Use --prepare-only to build and inspect the exact untrusted hook definition.
This is local harness qualification, not a real-provider or mixed-fleet exercise.
"""
import argparse, gzip, hashlib, http.server, json, os, pathlib, queue, shlex
import subprocess, tempfile, threading, time, zlib, shutil, sys

CODEX=shutil.which('codex')

def _admission_lib():
    """scripts/admission_lib.py, loaded once per process under one name, so a
    runner and every fixture module it loads share ONE candidate binding."""
    import importlib.util
    name = "buddy_admission_lib"
    if name not in sys.modules:
        spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).resolve().with_name("admission_lib.py"))
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
    return sys.modules[name]


admission = _admission_lib()
candidate_flags = admission.candidate_flags
# The declared version a launch carries: the newest recorded build until
# bind_codex binds the binary this run was given (D-080).
CODEX_VERSION = admission.recorded_versions('codex')[-1]
# From 0.165.0 Codex verifies an apply_patch's grammar BEFORE its PreToolUse
# hooks (run40, 2026-10-29): the edge cases' malformed patch drew Codex's own
# "apply_patch verification failed" and no hook run, where 0.164.0 ran the hook
# and the gate denied it. Nothing was written either way, but the gate never
# sees that patch, so from this version the suite expects Codex's refusal and
# one gate row fewer. check-codex-gate.py draws the same line.
VERIFIES_BEFORE_HOOKS = '0.165.0'


def verifies_before_hooks(version):
    return admission.version_key(version) >= admission.version_key(VERIFIES_BEFORE_HOOKS)


def bind_codex(path):
    """D-080: bind the codex binary this run was given as its candidate, then
    run the RESOLVED bin/codex inside its release tree (the adapter refuses a
    link and hashes the tree around it), and carry its version on launches."""
    global CODEX, CODEX_VERSION
    if not path:
        raise RuntimeError('codex is not on PATH; provide --codex')
    binding = admission.bind('codex', path)
    CODEX, CODEX_VERSION = binding['artifact'], binding['version']
    return binding
PYTHON=sys.executable
LIFECYCLE_WRAPPER = r"""import json,os,pathlib,sqlite3,subprocess,sys,shutil,tempfile
raw=sys.stdin.read();payload=json.loads(raw)
e=dict(os.environ);e['BUDDY_HARNESS']='codex'
receipt_path=pathlib.Path(RECEIPT)
event=payload['hook_event_name']
if event=='SessionStart' and not receipt_path.exists():
    p=subprocess.run([BUDDY,'lifecycle','begin','--session',payload['session_id'],'--label','native-fixture'],input='',text=True,capture_output=True,env=e,cwd=payload['cwd'])
    if p.returncode==0:
        receipt=json.loads(p.stdout)
        # A write-once per-run capture, never a lookup of current ledger identity.
        with receipt_path.open('x') as f:json.dump(receipt,f)
else:
    receipt=json.loads(receipt_path.read_text())
    if receipt['session_id']!=payload['session_id']:raise RuntimeError('native identity mismatch')
    p=subprocess.run([BUDDY,'lifecycle','hook','--incarnation',receipt['incarnation']],input=raw,text=True,capture_output=True,env=e,cwd=payload['cwd'])
record={'payload':payload,'stdout':p.stdout,'stderr':p.stderr,'returncode':p.returncode,'ledger_snapshot':None}
try:
    dbpath=pathlib.Path(payload['cwd'])/'.git/buddy.db'
    wal=pathlib.Path(str(dbpath)+'-wal')
    # Buddy has closed its connection. Never read immutable against a live WAL.
    if wal.exists() and wal.stat().st_size:raise RuntimeError('observer requires closed checkpointed database; WAL is nonempty')
    fd,snapshot=tempfile.mkstemp(prefix='observation-',suffix='.db',dir=receipt_path.parent)
    os.close(fd);shutil.copyfile(dbpath,snapshot)
    with sqlite3.connect('file:'+snapshot+'?mode=ro&immutable=1',uri=True) as db:
        record['ledger_snapshot']=db.execute('SELECT incarnation,ended,(SELECT COUNT(*) FROM session_idle i WHERE i.session_id=s.session_id AND i.incarnation=s.incarnation) FROM sessions s WHERE session_id=?',(payload['session_id'],)).fetchone()
except Exception as exc:
    # Observer failure is recorded separately and never hides actual callback rc.
    record['observer_error']=str(exc)
with open(LOG,'a') as f:f.write(json.dumps(record)+'\n')
# Native Stop expects a JSON hook document, never the begin receipt.
if p.returncode==0:sys.stdout.write('{}\n')
sys.stderr.write(p.stderr);sys.exit(p.returncode)
"""
DENY_PATCH='*** Begin Patch\n*** Update File: guarded.txt\n@@\n-original\n+conflicting-write\n*** Add File: collateral.txt\n+must-not-exist\n*** End Patch'
ALLOW_PATCH='*** Begin Patch\n*** Add File: allowed.txt\n+allowed-control\n*** End Patch'

def clean_env():
    # Explicit allowlist prevents nested harness IDs and credentials escaping.
    # Git config isolation matches qualify-claude.clean_env (F-39): without it
    # the operator's global and system config (init.templateDir hooks, an
    # fsmonitor, commit signing) applied to every fixture `git init` here and
    # in qualify-adapters.py, which reuses this environment.
    keys=('PATH','HOME','USER','LOGNAME','SHELL','TMPDIR','LANG','LC_ALL','TERM')
    env={k:os.environ[k] for k in keys if k in os.environ}
    env['LC_ALL']='C'; env['TERM']='dumb'
    env['GIT_CONFIG_NOSYSTEM']='1'; env['GIT_CONFIG_GLOBAL']='/dev/null'
    return env

def run(args, *, cwd, env, input=None, timeout=120):
    # Bounded (F-39): an unbounded child is a runner that hangs until a
    # harness kills it, and then no result.json says why. TimeoutExpired kills
    # the child and propagates as a failure.
    p=subprocess.run(args,cwd=cwd,env=env,input=input,text=True,capture_output=True,timeout=timeout)
    if p.returncode: raise RuntimeError(f'{args[0]} returned {p.returncode}: {p.stderr}')
    return p

def exit_on_signals():
    # F-35, the same as qualify-claude.exit_on_signals (this file loads no
    # sibling): TERM/HUP raise SystemExit so `finally` cleanup still runs.
    import signal
    def handler(signum, frame):
        raise SystemExit(128 + signum)
    for signum in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, handler)

def require(ok, detail):
    # A qualification gate, never an `assert`: `python -O` strips asserts, and
    # the run then trusts hooks it never saw untrusted and exits 0 over a
    # failed result (F-39). check.sh refuses an assert statement in scripts/.
    if not ok:
        raise SystemExit('qualification gate failed: ' + detail)

def list_hooks(state,work,env):
    e=dict(env);e['CODEX_HOME']=str(state)
    p=subprocess.Popen([CODEX,'app-server','--stdio'],cwd=work,env=e,
                       stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    q=queue.Queue()
    def reader():
        for line in p.stdout:
            try:q.put(json.loads(line))
            except ValueError:pass
    threading.Thread(target=reader,daemon=True).start()
    def call(rid,method,params):
        p.stdin.write(json.dumps({'id':rid,'method':method,'params':params})+'\n');p.stdin.flush()
        until=time.monotonic()+15
        while time.monotonic()<until:
            try:v=q.get(timeout=.5)
            except queue.Empty:continue
            if v.get('id')==rid:
                if 'error'in v:raise RuntimeError(v['error'])
                return v['result']
        raise RuntimeError('app-server read timeout')
    try:
        call(1,'initialize',{'clientInfo':{'name':'buddy_local_gate_qualification','version':'1'},'capabilities':{'experimentalApi':True}})
        p.stdin.write('{"method":"initialized"}\n');p.stdin.flush()
        return call(2,'hooks/list',{'cwds':[str(work)]})
    finally:
        p.stdin.close()
        try:p.wait(timeout=5)
        except subprocess.TimeoutExpired:p.terminate();p.wait(timeout=5)
        (state.parent/'hook-inspect.stderr').write_text(p.stderr.read())

def config_text(port,wrapper,lifecycle=None):
    base = f'''model_provider = "buddy_fixture"
model = "gpt-6.1-sol"
approval_policy = "never"
sandbox_mode = "workspace-write"
web_search = "disabled"
[model_providers.buddy_fixture]
name = "Buddy deterministic local fixture"
base_url = "http://127.0.0.1:{port}/v1"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
[features]
apps = false
plugins = false
code_mode = false
enable_request_compression = false
shell_snapshot = false
[agents]
enabled = false
[analytics]
enabled = false
[[hooks.PreToolUse]]
matcher = "^(apply_patch|Bash)$"
[[hooks.PreToolUse.hooks]]
type = "command"
command = {json.dumps(shlex.quote(PYTHON)+' '+shlex.quote(str(wrapper)))}
timeout = 10
'''
    if lifecycle:
        for event in ('SessionStart','UserPromptSubmit','PostToolUse','Stop','SessionEnd'):
            base += '\n[[hooks.'+event+']]\n[[hooks.'+event+'.hooks]]\ntype = \"command\"\ncommand = '+json.dumps(shlex.quote(PYTHON)+' '+shlex.quote(str(lifecycle)))+'\ntimeout = 3\n'
    return base

class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self,*args):pass
    def do_POST(self):
        raw=self.rfile.read(int(self.headers.get('Content-Length','0')))
        if self.headers.get('Content-Encoding')=='gzip':raw=gzip.decompress(raw)
        if self.headers.get('Content-Encoding')=='deflate':raw=zlib.decompress(raw)
        req=json.loads(raw)
        # Capture fixture-only tool results and schema names; do not copy env or auth.
        summary={'path':self.path,'tools':req.get('tools'),
                 'tool_outputs':[x for x in req.get('input',[]) if x.get('type')in ('function_call_output','custom_tool_call_output')]}
        self.server.requests.append(summary)
        idx=len(self.server.requests)
        rid=f'resp_buddy_{idx}'
        if idx<=len(self.server.patches):
            item={'type':'custom_tool_call','id':f'ctc_buddy_{idx}',
                  'call_id':f'call_buddy_{idx}','name':'apply_patch',
                  'input':self.server.patches[idx-1],'status':'completed'}
            # Codex intercepts this shell form into its patch engine, but emits
            # a Bash hook. The direct apply_patch-only matcher left the held
            # file writable even though cmdGate's shell detector was fixed.
            # Keep the later direct patch as a positive enforcement control.
            if idx == 1 and getattr(self.server, 'shell_patch', False):
                item = {'type': 'function_call', 'id': f'fc_buddy_{idx}',
                    'call_id': f'call_buddy_{idx}', 'name': 'exec_command',
                    'arguments': json.dumps({'cmd': "apply_patch <<'EOF'\n" + self.server.patches[0] + "\nEOF",
                        'workdir': str(self.server.work), 'login': False, 'max_output_tokens': 2000}),
                    'status': 'completed'}
        else:
            item={'type':'message','id':'msg_buddy_done','status':'completed','role':'assistant',
                  'content':[{'type':'output_text','text':'Fixture complete.','annotations':[]}]}
        ev=[{'type':'response.created','response':{'id':rid,'object':'response','status':'in_progress','output':[]}},
            {'type':'response.output_item.added','output_index':0,'item':dict(item)},
            {'type':'response.output_item.done','output_index':0,'item':item},
            {'type':'response.completed','response':{'id':rid,'object':'response','status':'completed','output':[item],
                 'usage':{'input_tokens':1,'output_tokens':1,'total_tokens':2}}}]
        body=''.join('event: '+x['type']+'\ndata: '+json.dumps(x)+'\n\n' for x in ev).encode()
        self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)


def app_server_run(work,env,root):
    p=subprocess.Popen([CODEX,'app-server','--stdio'],cwd=work,env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    q=queue.Queue();events=[]
    def reader():
        for line in p.stdout:
            try:q.put(json.loads(line))
            except ValueError:pass
    threading.Thread(target=reader,daemon=True).start()
    def send(rid,method,params):
        p.stdin.write(json.dumps({'id':rid,'method':method,'params':params})+'\n');p.stdin.flush()
    def wait_for(pred):
        until=time.monotonic()+30
        while time.monotonic()<until:
            try:v=q.get(timeout=.5)
            except queue.Empty:continue
            events.append(v)
            if pred(v):return v
        raise RuntimeError('app-server timeout')
    try:
        send(1,'initialize',{'clientInfo':{'name':'buddy_local_gate_qualification','version':'1'},'capabilities':{'experimentalApi':True}})
        wait_for(lambda v:v.get('id')==1)
        p.stdin.write('{"method":"initialized"}\n');p.stdin.flush()
        send(2,'thread/start',{'cwd':str(work),'approvalPolicy':'never','sandbox':'workspace-write'})
        start=wait_for(lambda v:v.get('id')==2)
        tid=start['result']['thread']['id']
        send(3,'turn/start',{'threadId':tid,'input':[{'type':'text','text':'Execute the supplied fixture tool sequence. This disposable repo is authorized for the fixture.'}]})
        completed=wait_for(lambda v:v.get('method')=='turn/completed')
        if completed['params']['turn']['status']!='completed':raise RuntimeError('native turn did not complete successfully')
        send(4,'thread/archive',{'threadId':tid})
        archived=wait_for(lambda v:v.get('id')==4)
        if 'error' in archived:raise RuntimeError(archived['error'])
    finally:
        p.stdin.close()
        try:p.wait(timeout=5)
        except subprocess.TimeoutExpired:p.terminate();p.wait(timeout=5)
        while not q.empty():events.append(q.get_nowait())
        stdout=''.join(json.dumps(v)+'\n' for v in events)
        stderr=p.stderr.read()
    return subprocess.CompletedProcess(p.args,p.returncode,stdout,stderr)


# CLAIM RENEWAL THROUGH THE ADAPTER (F-26, D-078). The modes above drive the
# app-server directly, so they never exercise buddy-adapter's controller, and
# the controller is what renews a worker's claims from a completed fileChange.
# This mode runs the real adapter with its receipt-bound gate and checks the
# ledger itself: the claims covering a written file and both ends of a move
# moved, the held claim behind the denied patch and a never-named control claim
# did not, and the journal recorded the renewal after the wake and before idle.
MOVE_PATCH='*** Begin Patch\n*** Update File: source.txt\n*** Move to: moved.txt\n@@\n-original\n+changed\n*** End Patch'

def claim_renewals(work,root):
    """{slug: renewed} for open claims, read from an immutable private copy.

    The ledger is read only with every buddy connection closed (an empty or
    absent WAL), the lifecycle wrapper's rule: never read immutable over a live
    WAL. It is the fixture's own ledger, never a real one."""
    import sqlite3
    db=work/'.git/buddy.db';wal=pathlib.Path(str(db)+'-wal')
    for _ in range(50):
        if not wal.exists() or wal.stat().st_size==0:break
        time.sleep(.1)
    else:
        raise RuntimeError('fixture ledger WAL stayed nonempty; refusing an immutable read')
    fd,snap=tempfile.mkstemp(prefix='renewal-',suffix='.db',dir=root);os.close(fd);shutil.copyfile(db,snap)
    with sqlite3.connect('file:'+snap+'?mode=ro&immutable=1',uri=True) as conn:
        return {slug:renewed for slug,renewed in conn.execute("SELECT slug,renewed FROM claims WHERE state='open'")}

def adapter_main(args,root,work,state,env,buddy):
    adapter=args.adapter.resolve();receipt=root/'worker.json';checks={}
    (work/'source.txt').write_text('original\n')
    server=http.server.HTTPServer(('127.0.0.1',0),Fixture);server.requests=[];server.patches=[DENY_PATCH,ALLOW_PATCH,MOVE_PATCH];server.work=work;server.shell_patch=False
    quote=lambda s:"'"+str(s).replace("'","'\"'\"'")+"'"
    gate=quote(adapter)+' hook --receipt '+quote(receipt)+' --buddy '+quote(buddy)
    config=state/'config.toml'
    base=config_text(server.server_address[1],root/'unused.py').replace(json.dumps(shlex.quote(PYTHON)+' '+shlex.quote(str(root/'unused.py'))),json.dumps(gate))
    config.write_text(base)
    hooks=list_hooks(state,work,env);(root/'hooks-untrusted.json').write_text(json.dumps(hooks,indent=2));h=hooks['data'][0]['hooks'][0]
    require(h['trustStatus']=='untrusted','hook must be discovered UNTRUSTED before trust: '+json.dumps(hooks))
    config.write_text(base+'\n[hooks.state.'+json.dumps(h['key'])+']\ntrusted_hash = '+json.dumps(h['currentHash'])+'\n')
    hooks=list_hooks(state,work,env);(root/'hooks-trusted.json').write_text(json.dumps(hooks,indent=2));h=hooks['data'][0]['hooks'][0]
    require(h['trustStatus']=='trusted','exact-hash trust did not take: '+json.dumps(hooks))
    launch={'worktree':str(work),'permission_mode':'workspace-write','hook_config':str(config),'harness_version':CODEX_VERSION,'gate_command':gate,'gate_hash':h['currentHash'],'hooks':{'configured':'observed','trusted':'observed','observed':'unknown','no_mods':'unknown'}}
    (root/'launch.json').write_text(json.dumps(launch,indent=2))
    threading.Thread(target=server.serve_forever,daemon=True).start()
    events=[];q=queue.Queue()
    p=subprocess.Popen(candidate_flags([str(adapter),'--harness','codex','--executable',CODEX,'--buddy',str(buddy),'--launch',str(root/'launch.json'),'--receipt',str(receipt),'--label','renewal-native-fixture']),cwd=work,env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=(root/'adapter.stderr').open('w'),text=True)
    def reader():
        for line in p.stdout:
            try:v=json.loads(line)
            except ValueError:continue
            events.append(v);q.put(v)
    threading.Thread(target=reader,daemon=True).start()
    def wait(pred):
        until=time.monotonic()+60
        while time.monotonic()<until:
            try:v=q.get(timeout=.2)
            except queue.Empty:
                if p.poll() is not None:raise RuntimeError('adapter exited '+str(p.returncode)+' '+(root/'adapter.stderr').read_text())
                continue
            if v.get('error'):raise RuntimeError(json.dumps(v))
            if pred(v):return v
        raise RuntimeError('adapter timeout')
    def send(obj):p.stdin.write(json.dumps(obj)+'\n');p.stdin.flush()
    result={'qualification':'installed native Codex through buddy-adapter, deterministic local provider fixture; no real inference','root':str(root)}
    try:
        start=wait(lambda v:v.get('operation')=='start');ident=start['identity']
        checks['captured_native_identity']=bool(ident['session_id'] and ident['incarnation'])
        checks['claim_renewal_capability_declared']=start['capabilities'].get('claim_renewal',{}).get('support')=='experimental'
        for slug,scope in (('worker-allowed','allowed.txt'),('worker-move-source','source.txt'),('worker-move-destination','moved.txt'),('worker-control','control.txt')):
            run([str(buddy),'claim',slug,'--session',ident['session_id'],'--scope',scope,'--desc','renewal qualification only'],cwd=work,env=env)
        before=claim_renewals(work,root)
        time.sleep(1.2)  # renewed is whole seconds
        sender=json.loads(run([str(buddy),'lifecycle','begin','--session','fixture-coordinator'],cwd=work,env=env).stdout)
        queued=json.loads(run([str(buddy),'adapter','queue','--session',sender['session_id'],'--incarnation',sender['incarnation'],'--to',ident['session_id'],'--to-incarnation',ident['incarnation'],'--body','Fixture assignment remains peer data.'],cwd=work,env=env).stdout)
        send({'schema':'buddy.control.v1','id':'wake-1','operation':'send','identity':ident,'message_id':queued['delivery']['message_id']})
        wake=wait(lambda v:v.get('request_id')=='wake-1');checks['native_wake_accepted']=wake['wake_delivery']['state']=='delivered'
        wait(lambda v:v.get('native_event',{}).get('kind')=='result')
        idle=wait(lambda v:v.get('native_event',{}).get('kind')=='idle');checks['native_idle_translated']=idle['state']['detail']=='idle'
    finally:
        p.stdin.close()
        try:p.wait(timeout=10)
        except subprocess.TimeoutExpired:p.terminate();p.wait(timeout=5)
        server.shutdown();server.server_close()
        (root/'events.json').write_text(json.dumps(events,indent=2));(root/'requests.json').write_text(json.dumps(server.requests,indent=2))
    checks['adapter_exit_zero']=p.returncode==0
    checks['conflict_patch_denied']=(work/'guarded.txt').read_text()=='original\n' and not (work/'collateral.txt').exists()
    checks['positive_patch_allowed']=(work/'allowed.txt').exists() and (work/'allowed.txt').read_text()=='allowed-control\n'
    checks['move_patch_applied']=not (work/'source.txt').exists() and (work/'moved.txt').exists() and (work/'moved.txt').read_text()=='changed\n'
    after=claim_renewals(work,root)
    journal=[json.loads(x) for x in pathlib.Path(str(receipt)+'.events').read_text().splitlines()]
    renewals=[(i,r) for i,r in enumerate(journal) if r.get('renewal')]
    applied=[(i,r) for i,r in renewals if r['renewal']['result']['state']=='applied']
    placed=sorted({x for _,r in applied for x in r['renewal'].get('placed_paths') or []})
    wake_at=next((i for i,r in enumerate(journal) if r.get('request_id')=='wake-1'),None)
    idle_at=next((i for i,r in enumerate(journal) if (r.get('native_event') or {}).get('kind')=='idle'),None)
    first=next((i for i,r in applied if 'allowed.txt' in (r['renewal'].get('placed_paths') or [])),None)
    checks['renewal_written_claim_moved']=after.get('worker-allowed',0)>before.get('worker-allowed',0)
    checks['renewal_move_source_claim_moved']=after.get('worker-move-source',0)>before.get('worker-move-source',0)
    checks['renewal_move_destination_claim_moved']=after.get('worker-move-destination',0)>before.get('worker-move-destination',0)
    checks['renewal_control_claim_unmoved']=after.get('worker-control')==before.get('worker-control') is not None
    checks['renewal_held_claim_unmoved']=after.get('guarded')==before.get('guarded') is not None
    checks['renewal_journalled_after_wake_before_idle']=None not in (wake_at,idle_at,first) and wake_at<first<idle_at
    checks['renewal_placed_only_written_paths']=bool(placed) and set(placed)<={'allowed.txt','source.txt','moved.txt'}
    checks['renewal_coverage_complete']=bool(renewals) and all(r['renewal']['coverage']['state']=='observed' for _,r in renewals)
    checks['renewal_same_identity']=bool(renewals) and all(r['identity']==ident for _,r in renewals)
    result.update({'checks':checks,'renewal':{'before':before,'after':after,'journal':[r['renewal'] for _,r in renewals],
        'observed_native_spellings':sorted({x for _,r in renewals for x in r['renewal'].get('paths') or []}),'placed_paths':placed},
        'codex_version':run([CODEX,'--version'],cwd=work,env=env).stdout.strip(),'qualification_candidates':[admission.summary('codex')],
        'buddy_sha256':hashlib.sha256(buddy.read_bytes()).hexdigest(),'adapter_sha256':hashlib.sha256(adapter.read_bytes()).hexdigest(),
        'codex_path':str(pathlib.Path(CODEX).resolve()),
        'codex_sha256':hashlib.sha256(pathlib.Path(CODEX).resolve().read_bytes()).hexdigest(),'mode':{'adapter':True},
        'fixture_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'requests':len(server.requests),
        'passed':all(checks.values()) and len(checks)==17})
    # F-37: every file this receipt's evidence rests on, bound to it.
    names=['worker.json','worker.json.events','worker.json.proc','events.json','requests.json','launch.json','adapter.stderr',
           'hooks-untrusted.json','hooks-trusted.json','state/config.toml','work/guarded.txt','work/allowed.txt','work/moved.txt']
    result['evidence_sha256']={n:hashlib.sha256((root/n).read_bytes()).hexdigest() for n in names if (root/n).is_file()}
    (root/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2));print('RECEIPT='+str(root/'result.json'),flush=True)
    if not result['passed']:raise SystemExit(1)


def main():
    global CODEX
    ap=argparse.ArgumentParser();ap.add_argument('--prepare-only',action='store_true');ap.add_argument('--buddy',type=pathlib.Path);ap.add_argument('--lifecycle',action='store_true');ap.add_argument('--app-server',action='store_true');ap.add_argument('--edge-cases',action='store_true')
    ap.add_argument('--repo',type=pathlib.Path,default=pathlib.Path.cwd());ap.add_argument('--codex',default=CODEX)
    ap.add_argument('--shell-patch',action='store_true',help='route the first held-path patch through native exec_command; requires Bash gate coverage')
    ap.add_argument('--adapter',type=pathlib.Path,help='run the worker through this buddy-adapter and check claim renewal from completed fileChanges (D-078)')
    args=ap.parse_args()
    if args.adapter and (args.lifecycle or args.app_server or args.edge_cases or args.shell_patch or args.prepare_only):
        ap.error('--adapter is its own mode: the adapter owns lifecycle and the app-server')
    exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    if not args.codex:ap.error('codex is not on PATH; provide --codex')
    binding=bind_codex(args.codex)
    root=pathlib.Path(tempfile.mkdtemp(prefix='buddy-codex-contract-qualification-',dir='/private/tmp'))
    work=root/'work';work.mkdir();state=root/'state';state.mkdir();env=clean_env()
    buddy=args.buddy.resolve() if args.buddy else root/'buddy'
    if not args.buddy:
        buildenv=dict(env);buildenv['GOCACHE']='/private/tmp/buddy-codex-go-cache';buildenv['GIT_OPTIONAL_LOCKS']='0'
        run(['go','build','-o',str(buddy),'./cmd/buddy'],cwd=args.repo.resolve(),env=buildenv,timeout=600)
    run(['git','init','-q',str(work)],cwd=root,env=env)
    run([str(buddy),'init'],cwd=work,env=env)
    run([str(buddy),'hello','--session','qualification-holder','--label','holder'],cwd=work,env=env)
    run([str(buddy),'claim','guarded','--session','qualification-holder','--scope','guarded.txt','--desc','qualification only'],cwd=work,env=env)
    (work/'guarded.txt').write_text('original\n')
    if args.adapter:
        print('ROOT='+str(root),flush=True)
        adapter_main(args,root,work,state,env,buddy)
        return
    patches=[DENY_PATCH,ALLOW_PATCH];malformed=unreadable=None
    if args.edge_cases:
        broken=work/'broken';broken.mkdir();run(['git','init','-q',str(broken)],cwd=work,env=env)
        (broken/'.git/buddy.db').write_bytes(b'not a SQLite ledger')
        malformed='*** Begin Patch\n*** Add File: malformed.txt\nmissing plus prefix\n*** End Patch'
        unreadable='*** Begin Patch\n*** Add File: broken/should-not-exist.txt\n+must-not-exist\n*** End Patch'
        patches=[DENY_PATCH,malformed,unreadable,ALLOW_PATCH]
    wrapper=root/'gate-wrapper.py'
    wrapper.write_text('''import json,os,pathlib,subprocess,sys
raw=sys.stdin.read()
e=dict(os.environ);e['BUDDY_HARNESS']='codex'
p=subprocess.run([BUDDY,'gate'],input=raw,text=True,capture_output=True,env=e)
record={'payload':json.loads(raw),'stdout':p.stdout,'stderr':p.stderr,'returncode':p.returncode,
        'identity_env':{k:e.get(k) for k in ('CODEX_THREAD_ID','CODEX_SESSION_ID')}}
with open(LOG,'a') as f:f.write(json.dumps(record)+'\\n')
sys.stdout.write(p.stdout);sys.stderr.write(p.stderr);sys.exit(p.returncode)
'''.replace('BUDDY,',repr(str(buddy))+',').replace('open(LOG,','open('+repr(str(root/'hook-invocations.jsonl'))+','))
    lifecycle=None
    if args.lifecycle:
        lifecycle=root/'lifecycle-wrapper.py'
        source=LIFECYCLE_WRAPPER
        source=source.replace('BUDDY,',repr(str(buddy))+',').replace('pathlib.Path(RECEIPT)','pathlib.Path('+repr(str(root/'captured-receipt.json'))+')').replace('open(LOG,','open('+repr(str(root/'lifecycle-invocations.jsonl'))+',')
        lifecycle.write_text(source)
    config=state/'config.toml';config.write_text(config_text(9,wrapper,lifecycle))
    discovered=list_hooks(state,work,env)
    (root/'hooks-untrusted.json').write_text(json.dumps(discovered,indent=2))
    hooks=[h for entry in discovered['data'] for h in entry['hooks']]
    expected=6 if args.lifecycle else 1
    require(len(hooks)==expected and all(h['trustStatus']=='untrusted' for h in hooks),'hooks must be discovered UNTRUSTED before any trust: '+json.dumps(discovered))
    require(all(h['command'] in [shlex.quote(PYTHON)+' '+shlex.quote(str(x)) for x in (wrapper,lifecycle) if x] for h in hooks),'discovered hook commands are not the reviewed wrappers: '+json.dumps(discovered))
    print('ROOT='+str(root),flush=True)
    print('Reviewed command: '+hooks[0]['command'],flush=True)
    print('Exact hook key/hash: '+hooks[0]['key']+' '+hooks[0]['currentHash'],flush=True)
    if args.prepare_only:
        print('Prepared only: no hook trusted or executed; no Responses server started.');return
    server=http.server.HTTPServer(('127.0.0.1',0),Fixture);server.requests=[];server.patches=patches;server.work=work;server.shell_patch=args.shell_patch
    config.write_text(config_text(server.server_address[1],wrapper,lifecycle)+''.join(
       '\n[hooks.state.'+json.dumps(h['key'])+']\ntrusted_hash = '+json.dumps(h['currentHash'])+'\n' for h in hooks))
    verified=list_hooks(state,work,env)
    (root/'hooks-trusted.json').write_text(json.dumps(verified,indent=2))
    trusted=[h for entry in verified['data'] for h in entry['hooks']]
    require(len(trusted)==expected and all(h['trustStatus']=='trusted' for h in trusted),'exact-hash trust did not take: '+json.dumps(verified))
    threading.Thread(target=server.serve_forever,daemon=True).start()
    e=dict(env);e['CODEX_HOME']=str(state)
    command=[CODEX,'exec','--json','--ephemeral','-s','workspace-write','-C',str(work),
             'Execute the supplied fixture tool sequence. This disposable repo is authorized for the fixture.']
    try:
        p=app_server_run(work,e,root) if args.app_server else subprocess.run(command,cwd=work,env=e,text=True,capture_output=True,timeout=90)
        (root/'codex.stdout.jsonl').write_text(p.stdout);(root/'codex.stderr').write_text(p.stderr)
    finally:
        server.shutdown();server.server_close()
        (root/'requests.json').write_text(json.dumps(server.requests,indent=2))
    log=root/'hook-invocations.jsonl'
    records=[json.loads(x) for x in log.read_text().splitlines()] if log.exists() else []
    # Each edge case is found by its patch text, never by its row's position:
    # on 0.165.0 the malformed patch has no row, and the positional read passed
    # malformed_patch_denied on the unreadable case's denial.
    def rows_for(text):return [x for x in records if x['payload'].get('tool_input',{}).get('command')==text]
    # Every request repeats the call's output; copies that disagree are no answer.
    def output_of(call):
        seen={json.dumps(x.get('output')) for q in server.requests for x in q['tool_outputs'] if x.get('call_id')==call}
        return json.loads(seen.pop()) if len(seen)==1 else None
    verified_first=verifies_before_hooks(CODEX_VERSION)
    hooked=[x for x in patches if not(verified_first and x==malformed)]
    checks={
       'codex_exit_zero':p.returncode==0,
       'all_actual_patch_hooks':len(records)==len(hooked) and all(x['payload'].get('tool_name')==('Bash' if args.shell_patch and i==0 else 'apply_patch') for i,x in enumerate(records)),
       'guarded_unchanged':(work/'guarded.txt').read_text()=='original\n',
       'whole_denied_patch_unchanged':not (work/'collateral.txt').exists(),
       'allowed_control_written':(work/'allowed.txt').exists() and (work/'allowed.txt').read_text()=='allowed-control\n',
       'deny_envelope_seen':bool(records) and '"deny"'in records[0]['stdout'],
    }
    if args.shell_patch:
        checks['shell_routed_patch_hook_denied'] = bool(records) and records[0]['payload'].get('tool_name') == 'Bash' and '\"deny\"' in records[0]['stdout']
    if args.edge_cases:
        if verified_first:
            # Codex refused it before any hook: no gate row, and the model told so.
            checks['malformed_patch_denied']=not rows_for(malformed) and str(output_of('call_buddy_2')).startswith('apply_patch verification failed') and not(work/'malformed.txt').exists()
        else:
            checks['malformed_patch_denied']=len(rows_for(malformed))==1 and '\"deny\"' in rows_for(malformed)[0]['stdout'] and not(work/'malformed.txt').exists()
        checks['unreadable_ledger_denied']=len(rows_for(unreadable))==1 and '\"deny\"' in rows_for(unreadable)[0]['stdout'] and not(work/'broken/should-not-exist.txt').exists()
    if args.lifecycle:
        lifelog=root/'lifecycle-invocations.jsonl'
        life=[json.loads(x) for x in lifelog.read_text().splitlines()] if lifelog.exists() else []
        events=[x['payload']['hook_event_name'] for x in life]
        checks['native_lifecycle_events']=all(x in events for x in ('SessionStart','UserPromptSubmit','PostToolUse','Stop','SessionEnd'))
        checks['native_lifecycle_hooks_succeeded']=bool(life) and all(x['returncode']==0 for x in life)
        checks['lifecycle_observer_succeeded']=bool(life) and all('observer_error' not in x and x['ledger_snapshot'] for x in life)
        captured=json.loads((root/'captured-receipt.json').read_text())
        checks['same_captured_incarnation']=bool(life) and all(x.get('ledger_snapshot') and x['ledger_snapshot'][0]==captured['incarnation'] for x in life)
        checks['native_stop_idle_observed']=any(x['payload']['hook_event_name']=='Stop' and x.get('ledger_snapshot') and x['ledger_snapshot'][1] is None and x['ledger_snapshot'][2]==1 for x in life)
        checks['native_end_observed']=any(x['payload']['hook_event_name']=='SessionEnd' and x.get('ledger_snapshot') and x['ledger_snapshot'][1] is not None for x in life)
    result={'qualification':'installed native Codex with deterministic local provider fixture; no real inference',
            'codex_version':run([CODEX,'--version'],cwd=work,env=env).stdout.strip(),'qualification_candidates':[admission.summary('codex')],
            'buddy_sha256':hashlib.sha256(buddy.read_bytes()).hexdigest(),
            # The executable's bytes, not only its --version text (check-codex-gate "unbound").
            'codex_path':str(pathlib.Path(CODEX).resolve()),
            'codex_sha256':hashlib.sha256(pathlib.Path(CODEX).resolve().read_bytes()).hexdigest(),
            'mode':{'app_server':args.app_server,'lifecycle':args.lifecycle,'edge_cases':args.edge_cases,'shell_patch':args.shell_patch},
            'fixture_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
            'checks':checks,'codex_returncode':p.returncode,'requests':len(server.requests),'root':str(root)}
    # F-37: every file check-codex-gate.py reads, bound to this receipt.
    names=['captured-receipt.json','hook-invocations.jsonl','lifecycle-invocations.jsonl','requests.json','codex.stdout.jsonl',
           'hooks-untrusted.json','hooks-trusted.json','state/config.toml','gate-wrapper.py','lifecycle-wrapper.py',
           'work/guarded.txt','work/allowed.txt','work/broken/.git/buddy.db']
    result['evidence_sha256']={n:hashlib.sha256((root/n).read_bytes()).hexdigest() for n in names if (root/n).is_file()}
    (root/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2))
    if not all(checks.values()):raise SystemExit(1)
if __name__=='__main__':main()
