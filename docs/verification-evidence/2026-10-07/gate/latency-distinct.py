exec(open('/private/tmp/buddy-gate-mail-verification-20261007/latency.py').read().split('paths=[]')[0])
for d in ['docs','physical/deep','lexical-dest','physical-dest']:(repo/d).mkdir(parents=True,exist_ok=True)
(repo/'docs/link').unlink(missing_ok=True)
(repo/'docs/link').symlink_to('../physical/deep')
paths=[]
for n in range(128):
 for d in ['lexical-dest','physical-dest']:(repo/d/f'f{n}').write_text('x\n')
 (repo/'docs'/f'f{n}').unlink(missing_ok=True)
 (repo/'docs'/f'f{n}').symlink_to(f'../lexical-dest/f{n}')
 (repo/'physical'/f'f{n}').unlink(missing_ok=True)
 (repo/'physical'/f'f{n}').symlink_to(f'../physical-dest/f{n}')
 paths.append(str(repo)+f'/docs/link/../f{n}')
patch='*** Begin Patch\n'+''.join('*** Delete File: '+p+'\n' for p in paths)+'*** End Patch\n'
payload=json.dumps(dict(session_id='latency',cwd=str(repo),tool_name='apply_patch',tool_input=dict(command=patch)))
(R/'patch128-distinct.hook.json').write_text(payload)
values=[]
for n in range(40):
 start=time.perf_counter_ns();p=run([str(R/'buddy'),'gate'],input=payload);elapsed=(time.perf_counter_ns()-start)/1e6
 if p.stdout.strip():raise RuntimeError(p.stdout)
 values.append(elapsed)
values.sort();result=dict(samples=40,min_ms=values[0],median_ms=statistics.median(values),p95_ms=values[37],max_ms=values[-1],over_100_ms=sum(v>100 for v in values),all_ms=values)
print(json.dumps({k:v for k,v in result.items() if k!='all_ms'}));(R/'latency-distinct.json').write_text(json.dumps(result,indent=2)+'\n')
