from pathlib import Path
import subprocess, os, json, time, sys
BASE=Path(__file__).parent; TREE=BASE/'mutant'; LOG=BASE/'logs'
env=dict(os.environ,GOCACHE=str(BASE/'go-cache'),GIT_OPTIONAL_LOCKS='0');env.pop('CLAUDE_CODE_SESSION_ID',None);env.pop('CODEX_THREAD_ID',None)
mutants=[]
def add(name,path,old,new,tests,pkg='./internal/harness',occ=1):
 mutants.append(dict(name=name,path=path,old=old,new=new,tests=tests,pkg=pkg,occ=occ))
main='cmd/buddy-adapter/main.go';proc='internal/harness/process.go';ctrl='internal/harness/controller.go';claude='internal/harness/claude.go';codex='internal/harness/codex.go'
ad='TestAdapterOperatorDescriptorAdmission'
add('fd-lower-bound',main,'if fd < 3 {','if false && fd < 3 {',ad,'./cmd/buddy-adapter')
add('fd-open',main,'unix.F_GETFD, 0); err != nil {','unix.F_GETFD, 0); false && err != nil {',ad,'./cmd/buddy-adapter')
add('fd-getfl-error',main,'flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)\n\tif err != nil {','flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)\n\tif false && err != nil {',ad,'./cmd/buddy-adapter')
add('fd-readonly',main,'if flags&unix.O_ACCMODE != unix.O_RDONLY {','if false && flags&unix.O_ACCMODE != unix.O_RDONLY {',ad,'./cmd/buddy-adapter')
add('fd-fstat-error',main,'unix.Fstat(fd, &st); err != nil {','unix.Fstat(fd, &st); false && err != nil {',ad,'./cmd/buddy-adapter')
add('fd-type',main,'\tdefault:\n\t\treturn nil, fmt.Errorf("operator approval descriptor %d must be a pipe, FIFO, terminal or file", fd)','\tdefault:\n\t\tif false { return nil, fmt.Errorf("operator approval descriptor %d must be a pipe, FIFO, terminal or file", fd) }',ad,'./cmd/buddy-adapter')
add('fd-cloexec',main,'syscall.CloseOnExec(fd)','_ = fd // removed CloseOnExec',ad,'./cmd/buddy-adapter')
add('fd-newfile-nil',main,'if f == nil {','if false && f == nil {',ad,'./cmd/buddy-adapter')
add('setpgid',proc,'Setpgid: true','Setpgid: false','TestJSONProcessCloseEndsDescendants|TestJSONProcessLeaderExitEndsGroup')
add('group-term',proc,'_ = syscall.Kill(-p.pgid, syscall.SIGTERM)','// removed group TERM','TestJSONProcessCloseEndsDescendants|TestJSONProcessLeaderExitEndsGroup')
add('group-kill',proc,'_ = syscall.Kill(-p.pgid, syscall.SIGKILL)','// removed group KILL','TestJSONProcessCloseEndsDescendants|TestJSONProcessLeaderExitEndsGroup')
add('reader-close',proc,'\t_ = p.stdout.Close()\n\treturn nil','\t// removed reader close\n\treturn nil','TestJSONProcessCloseFinishesDespiteStdoutHolders')
add('signal-handler',main,'signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)','// removed signal registration','TestAdapterSignalEndsWorkerTree','./cmd/buddy-adapter')
add('sigpipe-ignore',main,'signal.Ignore(syscall.SIGPIPE)','// removed SIGPIPE ignore','TestAdapterSignalEndsWorkerTree','./cmd/buddy-adapter')
for path in [claude,codex]:
 src=(TREE/path).read_text();n=0
 for line_no,line in enumerate(src.splitlines(),1):
  if 'return ' in line and 'ErrNotSubmitted' in line:
   n+=1
   target=line+'\n';replacement=target.replace('ErrNotSubmitted','errors.New("mutation: ordinary failure")')
   # repeated exact lines need nth occurrence
   occ=src[:src.index(target)].count(target)+1
   before='\n'.join(src.splitlines()[:line_no-1])+'\n'
   occ=before.count(target)+1
   add(f'{Path(path).stem}-not-submitted-{line_no}',path,target,replacement,'TestClaude.*(Approval|Cancelled|Refusals)|TestCodex.*(Approval|Refusals|Reconcile)|TestController',occ=occ)
add('controller-not-submitted',ctrl,'if errors.Is(err, ErrNotSubmitted) && (d.State == "" || d.State == "unknown") && d.TurnID == "" {','if false && errors.Is(err, ErrNotSubmitted) && (d.State == "" || d.State == "unknown") && d.TurnID == "" {','TestControllerNotSubmittedWakeIsRetryable|TestControllerClaudeBusyWakeRefusalRetriesAndResumes')
add('replay-open-intent',ctrl,'!open || prior.State != "unknown" || prior.TurnID != ""','false && (!open || prior.State != "unknown" || prior.TurnID != "")','TestControllerJournalNotSubmittedRequiresOpenIntent')
add('replay-no-turn',ctrl,'!open || prior.State != "unknown" || prior.TurnID != ""','!open || prior.State != "unknown"','TestControllerJournalNotSubmittedRequiresOpenIntent')
add('observation-gap',ctrl,'c.observationGap = fmt.Sprintf("native %s event was not recorded: %s", fence.Line(e.Kind, 64), fence.Line(err.Error(), 256))','c.observationGap = ""','TestControllerUnrecordedEventIsNeverObservedAbsent')
add('duplicate-key',claude,'if _, dup := out[key]; dup {','if _, dup := out[key]; false && dup {','TestClaudeSettings|TestClaudeEffectiveSettings')
for token in ['unsupported Claude adapter settings key','unsupported Claude PreToolUse group key','unsupported Claude gate hook key']:
 lines=[line for line in (TREE/claude).read_text().splitlines() if 'return fmt.Errorf("'+token in line]; old=lines[0]
 add('case-'+token.split()[2].lower(),claude,old,'\t\t\tif false { '+old.strip()+' }','TestClaudeSettings|TestClaudeEffectiveSettings')
add('matcher',claude,'if err := claudeMatcherCoversGate(matcher); err != nil {','if err := claudeMatcherCoversGate(matcher); false && err != nil {','TestClaudeSettings|TestClaudeEffectiveSettings')
add('matcher-syntax',claude,'if empty || strings.IndexFunc(list, func(r rune) bool { return !plain(r) }) >= 0 {','if false && (empty || strings.IndexFunc(list, func(r rune) bool { return !plain(r) }) >= 0) {','TestClaudeSettingsMatcherSemantics')
add('matcher-coverage',claude,'if !names[tool] {','if false && !names[tool] {','TestClaudeSettingsMatcherSemantics')
add('async',claude,'if async, ok := jsonBool(value); !ok || async {','if async, ok := jsonBool(value); false && (!ok || async) {','TestClaudeSettings|TestClaudeEffectiveSettings')
add('timeout-type',claude,'!ok || seconds < claudeHookTimeoutMinSeconds || seconds > claudeHookTimeoutMaxSeconds','seconds < claudeHookTimeoutMinSeconds || seconds > claudeHookTimeoutMaxSeconds || (false && !ok)','TestClaudeSettingsExactKeysAndBounds')
add('timeout-min',claude,'!ok || seconds < claudeHookTimeoutMinSeconds || seconds > claudeHookTimeoutMaxSeconds','!ok || seconds > claudeHookTimeoutMaxSeconds','TestClaudeSettingsExactKeysAndBounds')
add('timeout-max',claude,'!ok || seconds < claudeHookTimeoutMinSeconds || seconds > claudeHookTimeoutMaxSeconds','!ok || seconds < claudeHookTimeoutMinSeconds','TestClaudeSettingsExactKeysAndBounds')
exe='internal/harness/executable.go'
add('executable-identity',exe,'if err != nil || !os.SameFile(checked, now) || checked.Size() != now.Size() || !checked.ModTime().Equal(now.ModTime()) || checked.Mode() != now.Mode() {','if false && (err != nil || !os.SameFile(checked, now) || checked.Size() != now.Size() || !checked.ModTime().Equal(now.ModTime()) || checked.Mode() != now.Mode()) {','TestNativeExecutable')
add('endpoint-shared-budget','cmd/buddy-adapter/hook.go','harness.HookGateBudget-spent','harness.HookGateBudget-time.Since(started)+(spent-spent)','TestReceiptBoundHookExecutable/slow_endpoint_cannot_deny_the_tool_call','./cmd/buddy-adapter')
add('ledger-timeout',ctrl,'ctx, cancel := context.WithTimeout(ctx, c.ledgerTimeout)','ctx, cancel := context.WithCancel(ctx)','TestControllerStalledLedgerCannotWedgeShutdown')
add('ledger-lifetime-cancel',ctrl,'stop := context.AfterFunc(c.life, cancel)\n\treturn ctx, func() { stop(); cancel() }\n}\n\nfunc (c *Controller) Start','stop := func() bool { return true }\n\treturn ctx, func() { stop(); cancel() }\n}\n\nfunc (c *Controller) Start','TestControllerStalledLedgerCannotWedgeShutdown')
(BASE/'mutants.json').write_text(json.dumps(mutants,indent=2))
results=[]
for m in mutants:
 path=TREE/m['path'];src=path.read_text();old=m['old'];occ=m['occ'];at=-1
 for _ in range(occ):at=src.find(old,at+1)
 if at<0: raise RuntimeError(f"not found {m['name']} occurrence {occ}")
 path.write_text(src[:at]+m['new']+src[at+len(old):]);start=time.time()
 cmd=['go','test','-buildvcs=false','-count=1','-timeout=70s',m['pkg'],'-run',m['tests'],'-v']
 try:
  p=subprocess.run(cmd,cwd=TREE,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=100)
  out=p.stdout.decode(errors='replace');rc=p.returncode
 except subprocess.TimeoutExpired as e:out=(e.stdout or b'').decode(errors='replace');rc=124
 finally:path.write_text(src)
 logfile=LOG/(m['name']+'.log');logfile.write_text(out)
 status='survived' if rc==0 else ('build-error' if '[build failed]' in out or '[setup failed]' in out else ('timeout' if rc==124 else 'killed'))
 row=dict(name=m['name'],status=status,rc=rc,elapsed=round(time.time()-start,2),log=str(logfile),cmd=cmd)
 results.append(row);(BASE/'mutation-results.json').write_text(json.dumps(results,indent=2));print(json.dumps(row),flush=True)
