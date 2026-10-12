from pathlib import Path
import os, subprocess, shutil, json, difflib

R = Path(__file__).parent
S = Path('~/Projects/buddy-system')
D = R / 'budget-mutant'
D.mkdir(exist_ok=True)
for name in ['internal', 'cmd', 'go.mod', 'go.sum']:
    p = S / name
    if p.is_dir():
        shutil.copytree(p, D / name, dirs_exist_ok=True)
    else:
        shutil.copy2(p, D / name)
p = D / 'internal/cli/cli.go'
orig = p.read_text()
old = 'why = fmt.Sprintf("buddy gate cannot place shell patch routing (%s); use the apply_patch tool", fence.Line(routeErr.Error(), 512))'
new = 'deny(env, fmt.Sprintf("buddy gate cannot place shell patch routing (%s); use the apply_patch tool", fence.Line(routeErr.Error(), 512))); return 0'
if orig.count(old) != 1:
    raise RuntimeError('anchor')
changed = orig.replace(old, new, 1)
p.write_text(changed)
name = 'V04-budget-blanket-refusal'
(R / (name + '.diff')).write_text(''.join(difflib.unified_diff(orig.splitlines(True), changed.splitlines(True), fromfile='internal/cli/cli.go', tofile='internal/cli/cli.go')))
r = subprocess.run(['go', 'test', '-buildvcs=false', '-count=1', './internal/cli', '-run', '^TestGateShellBudgetRequiresJurisdiction$'], cwd=D, env=dict(os.environ, GOCACHE=str(R / 'go-cache'), GIT_OPTIONAL_LOCKS='0'), capture_output=True, text=True)
output = r.stdout + r.stderr
(R / (name + '.log')).write_text(output)
result = dict(name=name, rc=r.returncode, result='SURVIVED' if r.returncode == 0 else 'BUILD_ERROR' if '[build failed]' in output else 'KILLED', failures=[x for x in output.splitlines() if '--- FAIL:' in x])
(R / 'budget-mutation-result.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))
