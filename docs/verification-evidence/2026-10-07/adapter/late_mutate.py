from pathlib import Path
import subprocess,os,json
BASE=Path(__file__).parent;TREE=BASE/'final-mutants';env=dict(os.environ,GOCACHE=str(BASE/'go-cache'),GIT_OPTIONAL_LOCKS='0');env.pop('CLAUDE_CODE_SESSION_ID',None);env.pop('CODEX_THREAD_ID',None)
mutants=[
 ('v07-uncancellable-output','cmd/buddy-adapter/main.go','\t\tselect {\n\t\tcase <-out.done:\n\t\tcase <-ctx.Done():\n\t\t}','\t\t<-out.done','./cmd/buddy-adapter','TestAdapterSignalClosesWorkerWithFullStartupOutput'),
 ('v08-kill-leader-only','internal/harness/executable.go','_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)','_ = cmd.Process.Kill()','./internal/harness','TestNativeVersionProbeBoundsInheritedOutput'),
 ('v08-no-owned-group','internal/harness/executable.go','Setpgid: true','Setpgid: false','./internal/harness','TestNativeVersionProbeBoundsInheritedOutput'),
 ('v08-no-context-bound','internal/harness/executable.go','\t\tcase <-ctx.Done():\n\t\t\tbreak wait\n','', './internal/harness','TestNativeVersionProbeBoundsInheritedOutput')]
main=(TREE/'cmd/buddy-adapter/main.go').read_text();mark='func observeNativeEvents(';at=main.index(mark);end=main.find('\nfunc receiptEmitter(',at);old=main[at:] if end<0 else main[at:end];new='''func observeNativeEvents(ctx context.Context, events <-chan harness.Event,
 observe func(context.Context, harness.Event) (harness.Receipt, error), emit func(harness.Receipt, error)) {
 for e := range events {r,err:=observe(ctx,e);emit(r,err)}
}
''';mutants.append(('shutdown-observation-gap','cmd/buddy-adapter/main.go',old,new,'./cmd/buddy-adapter','TestAdapterExpectedShutdownDoesNotPublishDisconnect'))
results=[]
for name,file,old,new,pkg,pattern in mutants:
 p=TREE/file;src=p.read_text()
 if src.count(old)!=1:raise RuntimeError((name,src.count(old)))
 p.write_text(src.replace(old,new,1))
 try:r=subprocess.run(['go','test','-buildvcs=false','-count=1','-timeout=40s',pkg,'-run',pattern,'-v'],cwd=TREE,env=env,capture_output=True,text=True,timeout=60)
 finally:p.write_text(src)
 out=r.stdout+r.stderr;(BASE/'logs'/(name+'.log')).write_text(out)
 row=dict(name=name,rc=r.returncode,status='killed' if r.returncode==1 and '--- FAIL:' in out else ('survived' if r.returncode==0 else 'error'));results.append(row);print(row,flush=True)
(BASE/'late-mutations.json').write_text(json.dumps(results,indent=2))
