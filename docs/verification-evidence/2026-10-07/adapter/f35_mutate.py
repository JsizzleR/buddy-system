from pathlib import Path
import subprocess,json,sys
BASE=Path(__file__).parent;TREE=BASE/'f35';p=TREE/'scripts/qualify-claude.py';src=p.read_text();results=[]
mutants=[
 ('f35-old-group-equality','and descendant_of(identity, leaders):','and identity["pgid"] in {v["pgid"] for v in leaders}:','OwnedCleanup.test_native_ownership_follows_ancestry_across_groups'),
 ('f35-unverified-ancestry','and descendant_of(identity, leaders):','and True:','OwnedCleanup.test_native_ownership_follows_ancestry_across_groups'),
 ('f35-birth-comparison','return start is not None and start == identity["start"]','return start is not None','OwnedCleanup.test_fallback_signals_only_the_exact_owned_identity'),
 ('f35-crash-only-adapter','signal_owned_groups([client.owned, *natives])','signal_owned_groups([client.owned])','OwnedCleanup.test_deliberate_crash_ends_both_captured_groups')]
for name,old,new,test in mutants:
 if src.count(old)!=1:raise RuntimeError((name,src.count(old)))
 p.write_text(src.replace(old,new,1))
 try:r=subprocess.run([sys.executable,'-I','-B','scripts/test_native_proxy.py',test],cwd=TREE,capture_output=True,text=True,timeout=30)
 finally:p.write_text(src)
 out=r.stdout+r.stderr;(BASE/'logs'/(name+'.log')).write_text(out);row=dict(name=name,rc=r.returncode,status='killed' if r.returncode and 'FAIL:' in out else ('survived' if not r.returncode else 'error'));results.append(row);print(row,flush=True)
(BASE/'f35-mutations.json').write_text(json.dumps(results,indent=2))
