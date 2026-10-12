from pathlib import Path
import os,json,subprocess,time
BASE=Path(__file__).parent;TREE=BASE/'adversarial';LOG=BASE/'logs'
env=dict(os.environ,GOCACHE=str(BASE/'go-cache'),GIT_OPTIONAL_LOCKS='0');env.pop('CLAUDE_CODE_SESSION_ID',None);env.pop('CODEX_THREAD_ID',None)
rows=json.loads((BASE/'mutation-results.json').read_text());names={r['name'] for r in rows if r['status']=='survived'}
exclude={'fd-getfl-error','fd-fstat-error','fd-newfile-nil','timeout-type'}
mutants=[m for m in json.loads((BASE/'mutants.json').read_text()) if m['name'] in names-exclude]
for m in mutants:
 if m['name'].startswith('claude-not-submitted'):m['tests']='TestVerificationClaudeApprovalRefusalsAreNotSubmitted'
 elif m['name'].startswith('codex-not-submitted'):m['tests']='TestVerificationCodexRefusalEdges'
 elif m['name']=='fd-lower-bound':m['tests']='TestVerificationOperatorStdinRefused'
 elif m['name']=='group-term':m['tests']='TestVerificationGroupTERMAllowsFlush'
 elif m['name']=='sigpipe-ignore':m['tests']='TestVerificationBrokenStdoutStillClosesWorker'
 elif m['name']=='replay-no-turn':m['tests']='TestControllerJournalRefusalRejectsNativeEvidence'
 elif m['name']=='endpoint-shared-budget':m['tests']='TestVerificationEndpointKeepsGateBudget'
ctrl='internal/harness/controller.go'
for label,old in [('v02-prior-result',' || prior.ResultRef != ""'),('v02-refusal-turn',' || r.Delivery.TurnID != ""'),('v02-refusal-result',' || r.Delivery.ResultRef != ""'),('v02-live-result',' && d.ResultRef == ""')]:
 mutants.append(dict(name=label,path=ctrl,old=old,new='',tests='TestController(JournalRefusalRejectsNativeEvidence|LiveRefusalWithNativeEvidenceStaysReserved)',pkg='./internal/harness',occ=1))
results=[]
for m in mutants:
 p=TREE/m['path'];src=p.read_text();at=-1
 for _ in range(m['occ']):at=src.find(m['old'],at+1)
 if at<0:raise RuntimeError('not found '+m['name'])
 p.write_text(src[:at]+m['new']+src[at+len(m['old']):]);start=time.time()
 cmd=['go','test','-buildvcs=false','-count=1','-timeout=60s',m['pkg'],'-run',m['tests'],'-v']
 try:r=subprocess.run(cmd,cwd=TREE,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=90);out=r.stdout.decode(errors='replace');rc=r.returncode
 finally:p.write_text(src)
 (LOG/('supplement-'+m['name']+'.log')).write_text(out)
 status='survived' if rc==0 else ('build-error' if '[build failed]' in out or '[setup failed]' in out else 'killed')
 row=dict(name=m['name'],status=status,rc=rc,elapsed=round(time.time()-start,2),cmd=cmd);results.append(row);(BASE/'supplement-results.json').write_text(json.dumps(results,indent=2));print(json.dumps(row),flush=True)
