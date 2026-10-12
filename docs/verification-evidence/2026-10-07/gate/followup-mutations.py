from pathlib import Path
import os,subprocess,shutil,json,difflib
R=Path(__file__).parent;S=Path('~/Projects/buddy-system');D=R/'followup-mutant';D.mkdir(exist_ok=True)
for name in ['internal','cmd','scripts','docs','skills','go.mod','go.sum','CLAUDE.md','AGENTS.md','README.md']:
 p=S/name
 if p.is_dir():shutil.copytree(p,D/name,dirs_exist_ok=True)
 else:shutil.copy2(p,D/name)
env=dict(os.environ,GOCACHE=str(R/'go-cache'),GIT_OPTIONAL_LOCKS='0',GIT_CONFIG_GLOBAL='/dev/null',GIT_CONFIG_NOSYSTEM='1')
for k in ['CODEX_THREAD_ID','CLAUDE_CODE_SESSION_ID']:env.pop(k,None)
mut=[
('V03-error-returns-before-other-spellings','internal/cli/gatetargets.go','if resolutionErr == nil {','return out, err\n if resolutionErr == nil {'),
('V04-ansi-literal-unknown','internal/cli/patch.go','value, err := expand.Literal(nil,','if p.Dollar && strings.Contains(p.Value, "\\\\") { return "", false }; value, err := expand.Literal(nil,'),
('V04-nested-cwd-context-omitted','internal/cli/patch.go','shellPatchScript(words[i+1], depth+1, context)','shellPatchScript(words[i+1], depth+1, nil)'),
('V04-all-directory-context-omitted','internal/cli/patch.go','shellPatchScript(h.ToolInput.Command, 0, context)','shellPatchScript(h.ToolInput.Command, 0, nil)'),
('V04-combined-lookup-not-recognized','internal/cli/patch.go','name == "command" && !strings.HasPrefix(arg, "--") && strings.ContainsAny(arg[1:], "vV")','name == "command" && (arg == "-v" || arg == "-V")'),
('V04-raw-word-prefilter','internal/cli/patch.go','mentions := strings.Contains(script, "apply_patch") || strings.Contains(script, "applypatch")','mentions := strings.Contains(script, "apply_patch") || strings.Contains(script, "applypatch"); if !mentions { return false }')
]
results=[]
for name,path,old,new in mut:
 p=D/path;orig=p.read_text()
 if old not in orig:raise RuntimeError(name)
 changed=orig.replace(old,new,1);p.write_text(changed)
 (R/(name+'.diff')).write_text(''.join(difflib.unified_diff(orig.splitlines(True),changed.splitlines(True),fromfile=path,tofile=path)))
 try:r=subprocess.run(['go','test','-buildvcs=false','-count=1','./internal/cli','-run','TestGate(ResolutionErrorStillChecksOtherSpellings|ShellStaticQuotingAndNestedJurisdiction|ShellWrapperPatchAndQuotedControls)','-v'],cwd=D,env=env,capture_output=True,text=True,timeout=120)
 finally:p.write_text(orig)
 output=r.stdout+r.stderr;(R/(name+'.log')).write_text(output)
 result=dict(name=name,rc=r.returncode,result='SURVIVED' if r.returncode==0 else 'BUILD_ERROR' if '[build failed]' in output else 'KILLED',failures=[x for x in output.splitlines() if '--- FAIL:' in x]);results.append(result);print(json.dumps(result),flush=True)
 (R/'followup-mutation-results.json').write_text(json.dumps(results,indent=2)+'\n')
