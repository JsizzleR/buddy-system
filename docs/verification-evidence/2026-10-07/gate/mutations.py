from pathlib import Path
import os,subprocess,shutil,json,difflib,time
ROOT=Path(__file__).parent
SRC=ROOT/'current'; MUT=ROOT/'mutant'
shutil.copytree(SRC,MUT,dirs_exist_ok=True)
env=dict(os.environ,GOCACHE=str(ROOT/'go-cache'),GIT_OPTIONAL_LOCKS='0',GIT_CONFIG_GLOBAL='/dev/null',GIT_CONFIG_NOSYSTEM='1')
for x in ['CLAUDE_CODE_SESSION_ID','CODEX_THREAD_ID']:env.pop(x,None)
mut=[]
def add(name,path,old,new,run):mut.append((name,path,old,new,run))
g='^TestGate|TestVerificationFourDistinctTargetSpellings'
m='TestLegacyViewsReportAdapterMail|TestLegacyCorrectionOfAdapterMailRefused|TestNoLegacyWakeForAdapterMailOrRecipients|TestLifecycleHookRenewsObservedClaimsOnly|TestHelloSessionStdinRefusalNamesRecipe'
add('gate-shellPatchCommand','internal/cli/patch.go','return h.ToolName != "apply_patch" &&','return false && h.ToolName != "apply_patch" &&',g)
add('gate-governed-shell-deny','internal/cli/cli.go','if shellPatch {\n\t\t\tdeny(env, shellPatchReason(h))','if false && shellPatch {\n\t\t\tdeny(env, shellPatchReason(h))',g)
add('gate-ungoverned-shellPatchDirs','internal/cli/cli.go','dirs = append(dirs, shellPatchDirs(h)...)','dirs = append(dirs, []string{}...)',g)
add('gate-home-denyIfHeld','internal/cli/cli.go','if rel, _, outside := placeCanonical(rc.top, cp); !outside {','if rel, _, outside := placeCanonical(rc.top, cp); !outside && !nested {',g)
for n,line in enumerate(['add(canon(lex))','add(entryOf(lex, canon))','add(physical(raw))','add(entryOf(raw, physical))','add(danglingTarget(e))'],1):
 new='_ = e' if n==5 else '// mutation removed '+line
 add('gate-target-add-'+str(n),'internal/cli/gatetargets.go',line,new,g)
add('gate-danglingTarget','internal/cli/gatetargets.go','if err != nil || fi.Mode()&os.ModeSymlink == 0 {','if true || err != nil || fi.Mode()&os.ModeSymlink == 0 {',g)
add('gate-governedAny','internal/cli/cli.go','func governedAny(h hookInput, env Env, dirs []string, why string, repos map[string]gateRepo) string {','func governedAny(h hookInput, env Env, dirs []string, why string, repos map[string]gateRepo) string {\n dirs = nil',g)
add('gate-idErr-early-refusal','internal/cli/cli.go','idErr := classifyHook(&h, env)','idErr := classifyHook(&h, env)\n if idErr != nil { deny(env, "identity conflict"); return 0 }',g)
add('gate-pause-read','internal/cli/cli.go','note, paused, err = repo.st.PausedFor(h.SessionID, label)','_ = label // mutation removes pause read',g)
add('mail-wakeAddr-early-return','internal/cli/wake.go','if r.adapterRecipient() {','if false && r.adapterRecipient() {',m)
add('mail-endpoint-count','internal/store/adapter.go','b.Endpoint = n > 0','b.Endpoint = false && n > 0',m)
add('mail-who-line','internal/cli/who.go','fmt.Fprintln(env.Stdout, "ADAPTER      "+adapterRegister(ab, now))','_ = ab // mutation removes ADAPTER line',m)
add('mail-sendNote-why','internal/cli/cli.go','if r.adapterRecipient() {','if false && r.adapterRecipient() {',m)
add('mail-backlog-note','internal/cli/cli.go','b.WriteString(adapterBacklogNote(r.adapter, now))','// mutation removes adapter backlog note',m)
add('mail-correctable','internal/store/inbox.go','} else if adapter {','} else if false && adapter {',m)
add('mail-lifecycle-extraction','internal/cli/lifecycle.go','var rels []string\n\tif event == "beat" {','var rels []string\n\tif false && event == "beat" {',m)
add('mail-BeatLogical-under-renew','internal/store/logical.go','if scopeCovers(scope, rel) {','if false && scopeCovers(scope, rel) {',m)
add('mail-BeatLogical-over-renew','internal/store/logical.go','if scopeCovers(scope, rel) {','if true || scopeCovers(scope, rel) {',m)
add('mail-hello-wrap','internal/cli/cli.go','herr = fmt.Errorf("%w; a hand-run `hello --session` takes no hook input, so give it an empty stdin (</dev/null): bytes there are read as a hook\'s and refused when they are not one", herr)','// mutation removes empty stdin guidance',m)
results=[]
for name,path,old,new,run in mut:
 p=MUT/path; original=(SRC/path).read_text()
 if original.count(old)!=1:raise RuntimeError((name,original.count(old)))
 changed=original.replace(old,new,1);p.write_text(changed)
 (ROOT/(name+'.diff')).write_text(''.join(difflib.unified_diff(original.splitlines(True),changed.splitlines(True),fromfile=path,tofile=path)))
 started=time.monotonic()
 try:
  r=subprocess.run(['go','test','-buildvcs=false','-count=1','./internal/cli','-run',run,'-v'],cwd=MUT,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=180)
  output=r.stdout;rc=r.returncode
 except subprocess.TimeoutExpired as e: output=str(e);rc=124
 finally:p.write_text(original)
 (ROOT/(name+'.log')).write_text(output)
 state='SURVIVED' if rc==0 else ('BUILD_ERROR' if '[build failed]' in output or '[setup failed]' in output else ('TIMEOUT' if rc==124 else 'KILLED'))
 entry=dict(name=name,result=state,rc=rc,seconds=round(time.monotonic()-started,3),failures=[l for l in output.splitlines() if '--- FAIL:' in l])
 results.append(entry);print(json.dumps(entry),flush=True)
 (ROOT/'mutation-results.json').write_text(json.dumps(results,indent=2)+'\n')
