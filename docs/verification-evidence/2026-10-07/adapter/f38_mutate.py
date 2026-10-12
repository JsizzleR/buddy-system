from pathlib import Path
import subprocess,json,sys
BASE=Path(__file__).parent;TREE=BASE/'f35';results=[]
for file,func in [('qualify-compaction.py','compact'),('qualify-interrupted-compaction.py','requests')]:
 p=TREE/'scripts'/file;src=p.read_text()
 for mode in ['daemon-no-join','stop-removed']:
  modified=src
  if mode=='daemon-no-join':
   old=f'inject_thread=threading.Thread(target={func});inject_thread.start()'
   if src.count(old)!=1 or src.count('inject_thread.join()')!=1:raise RuntimeError('unarmed')
   modified=src.replace(old,f'inject_thread=threading.Thread(target={func},daemon=True);inject_thread.start()').replace('inject_thread.join()','# deliberately omitted join')
   tests=['NativeProxyShutdown.test_injection_concurrent_with_input_relay','NativeProxyShutdown.test_native_stops_reading_with_injection_pending']
  else:
   if src.count('stop.set()\ncode=p.wait()')!=1:raise RuntimeError('unarmed stop')
   modified=src.replace('stop.set()\ncode=p.wait()','# deliberately omitted stop\ncode=p.wait()')
   tests=['NativeProxyShutdown.test_child_exit_with_input_still_open']
  p.write_text(modified)
  try:r=subprocess.run([sys.executable,'-I','-B','scripts/test_native_proxy.py',*tests],cwd=TREE,capture_output=True,text=True,timeout=30)
  finally:p.write_text(src)
  name=f'{file}-{mode}';out=r.stdout+r.stderr;(BASE/'logs'/(name+'.log')).write_text(out)
  row=dict(name=name,rc=r.returncode,status='survived' if not r.returncode else ('killed' if 'TimeoutExpired' in out or 'FAIL:' in out else 'error'));results.append(row);print(row,flush=True)
(BASE/'f38-mutations.json').write_text(json.dumps(results,indent=2))
