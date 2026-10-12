from pathlib import Path
import os,subprocess,shutil,json,difflib,time
R=Path(__file__).parent;S=Path('~/Projects/buddy-system');D=R/'repair-mutant';D.mkdir(exist_ok=True)
for name in ['internal','cmd','scripts','docs','skills','go.mod','go.sum','CLAUDE.md','AGENTS.md','README.md']:
 p=S/name
 if p.is_dir():shutil.copytree(p,D/name,dirs_exist_ok=True)
 else:shutil.copy2(p,D/name)
env=dict(os.environ,GOCACHE=str(R/'go-cache'),GIT_OPTIONAL_LOCKS='0',GIT_CONFIG_GLOBAL='/dev/null',GIT_CONFIG_NOSYSTEM='1')
for k in ['CODEX_THREAD_ID','CLAUDE_CODE_SESSION_ID']:env.pop(k,None)
mut=[
('V03-resolver-omitted','internal/cli/gatetargets.go','return resolveMissingTarget(p)','return p, nil'),
('V03-readlink-cleaned','internal/cli/gatetargets.go','remaining = append(strings.Split(link, string(filepath.Separator)), remaining...)','remaining = append(strings.Split(filepath.Clean(link), string(filepath.Separator)), remaining...)'),
('V03-resolution-error-ignored','internal/cli/cli.go','targets, err := gateTargets(h.Cwd, path)\n\t\tif err != nil {','targets, err := gateTargets(h.Cwd, path)\n\t\tif false && err != nil {'),
('V03-budget-allowed','internal/cli/gatetargets.go','if links > maxSymlinks {','if links > maxSymlinks { return next, nil }; if links > maxSymlinks {'),
('V04-shell-recursion-omitted','internal/cli/patch.go','return i+1 < len(words) && known[i+1] && shellPatchScript(words[i+1], depth+1)','return false && i+1 < len(words) && known[i+1] && shellPatchScript(words[i+1], depth+1)'),
('V04-command-position-ignored','internal/cli/patch.go','found = shellPatchArgs(words, known, depth)','found = true || shellPatchArgs(words, known, depth)'),
('V04-quoted-word-ignored','internal/cli/patch.go','case *syntax.SglQuoted:\n','case *syntax.SglQuoted:\n return "",false\n'),
]
results=[]
for name,path,old,new in mut:
 p=D/path;orig=p.read_text();num=orig.count(old)
 if num<1:raise RuntimeError((name,num))
 changed=orig.replace(old,new,1);p.write_text(changed)
 (R/(name+'.diff')).write_text(''.join(difflib.unified_diff(orig.splitlines(True),changed.splitlines(True),fromfile=path,tofile=path)))
 try:r=subprocess.run(['go','test','-buildvcs=false','-count=1','./internal/cli','-run','^TestGate','-v'],cwd=D,env=env,capture_output=True,text=True,timeout=120)
 finally:p.write_text(orig)
 output=r.stdout+r.stderr;(R/(name+'.log')).write_text(output)
 result=dict(name=name,rc=r.returncode,result='SURVIVED' if r.returncode==0 else 'BUILD_ERROR' if '[build failed]' in output else 'KILLED',failures=[x for x in output.splitlines() if '--- FAIL:' in x]);results.append(result);print(json.dumps(result),flush=True)
 (R/'repair-mutation-results.json').write_text(json.dumps(results,indent=2)+'\n')
