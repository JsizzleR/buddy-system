from pathlib import Path
import os,subprocess,json,time,statistics
R=Path(__file__).parent; repo=R/'latency-repo';repo.mkdir(exist_ok=True)
env=dict(os.environ,GIT_CONFIG_GLOBAL='/dev/null',GIT_CONFIG_NOSYSTEM='1',GIT_OPTIONAL_LOCKS='0')
for k in list(env):
 if k.startswith('BUDDY_') or k.startswith('CLAUDE_') or k.startswith('CODEX_') or k in ['GIT_DIR','GIT_COMMON_DIR','GIT_WORK_TREE','GIT_INDEX_FILE']:env.pop(k,None)
def run(args,**kw):return subprocess.run(args,cwd=repo,env=env,capture_output=True,text=True,check=True,**kw)
run(['git','init','-q']);run([str(R/'buddy'),'init'])
paths=[]
for n in range(128):
 p=repo/'src'/f'd{n}'/'file.go';p.parent.mkdir(parents=True,exist_ok=True);p.write_text('x\n');paths.append(str(p))
patch='*** Begin Patch\n'+''.join('*** Delete File: '+p+'\n' for p in paths)+'*** End Patch\n'
cases={'edit':dict(tool_name='Edit',tool_input=dict(file_path=paths[0])),'patch128':dict(tool_name='apply_patch',tool_input=dict(command=patch))}
results={}
for name,case in cases.items():
 payload=json.dumps(dict(session_id='latency',cwd=str(repo),**case));(R/(name+'.hook.json')).write_text(payload)
 values=[]
 for n in range(40):
  start=time.perf_counter_ns();p=run([str(R/'buddy'),'gate'],input=payload);elapsed=(time.perf_counter_ns()-start)/1e6
  if p.stdout.strip():raise RuntimeError(p.stdout)
  values.append(elapsed)
 values.sort();results[name]=dict(samples=40,min_ms=values[0],median_ms=statistics.median(values),p95_ms=values[37],max_ms=values[-1],over_100_ms=sum(v>100 for v in values),all_ms=values)
 print(name,json.dumps({k:v for k,v in results[name].items() if k!='all_ms'}),flush=True)
(R/'latency.json').write_text(json.dumps(results,indent=2)+'\n')
