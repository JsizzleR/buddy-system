import json, os, pathlib, shutil, subprocess, tempfile

SOURCE = pathlib.Path('~/Projects/buddy-system')
OUT = pathlib.Path(__file__).parent
ROOT = pathlib.Path(tempfile.mkdtemp(prefix='buddy-verification-root-mutants-', dir='/private/tmp'))
files = subprocess.check_output(['git', '--no-optional-locks', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=SOURCE).decode().split('\0')
for name in sorted(set(files)):
    if name and (SOURCE/name).is_file():
        dst = ROOT/name
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(SOURCE/name, dst)
env = dict(os.environ, GIT_OPTIONAL_LOCKS='0')
for key in list(env):
    if key.startswith('GIT_') and key != 'GIT_OPTIONAL_LOCKS' or key in ('BUDDY_SESSION', 'CLAUDE_CODE_SESSION_ID', 'CODEX_THREAD_ID', 'CODEX_SESSION_ID'):
        env.pop(key)
cases = [('V09_pending_gap_overstated', 'internal/harness/controller.go', 'pending.State = "unknown"\n\t\t\tpending.Detail = "the listed approvals', 'pending.State = "observed"\n\t\t\tpending.Detail = "the listed approvals', './internal/harness', '^TestControllerMissedCancellationMakesKnownApprovalUncertain$'), ('V10_earlier_classification', 'internal/cli/cli.go', 'r.adapter.Current > 0', 'r.adapter.Current+r.adapter.Earlier > 0', './internal/cli', '^TestEarlierAdapterIncarnationDoesNotSuppressLegacyWake$'), ('V10_endpoint_incarnation', 'internal/store/adapter.go', 'SELECT COUNT(*) FROM session_endpoints WHERE session_id=? AND incarnation=?`, sessionID, incarnation', 'SELECT COUNT(*) FROM session_endpoints WHERE session_id=?`, sessionID', './internal/cli', '^TestEarlierAdapterIncarnationDoesNotSuppressLegacyWake$'), ('V10_sent_list_guard', 'internal/cli/sent.go', 'if si.Adapter {\n\t\t\t\tcontinue', 'if false {\n\t\t\t\tcontinue', './internal/cli', '^TestEarlierAdapterIncarnationDoesNotSuppressLegacyWake$'), ('V10_sent_row_guard', 'internal/cli/sent.go', 'if !si.Adapter {', 'if true {', './internal/cli', '^TestEarlierAdapterIncarnationDoesNotSuppressLegacyWake$')]
results = []
for name, file, old, new, package, test in cases:
    path = ROOT/file
    source = path.read_text()
    if source.count(old) != 1:
        raise RuntimeError((name, 'mutation did not target exactly once', source.count(old)))
    control = subprocess.run(['go', 'test', '-buildvcs=false', '-count=1', package, '-run', test], cwd=ROOT, env=env, text=True, capture_output=True, timeout=120)
    (OUT/(name+'-control.log')).write_text(control.stdout+control.stderr)
    if control.returncode:
        raise RuntimeError((name, 'positive control failed'))
    path.write_text(source.replace(old, new, 1))
    try:
        mutant = subprocess.run(['go', 'test', '-buildvcs=false', '-count=1', package, '-run', test], cwd=ROOT, env=env, text=True, capture_output=True, timeout=120)
    finally:
        path.write_text(source)
    log = mutant.stdout+mutant.stderr
    (OUT/(name+'-mutant.log')).write_text(log)
    result = dict(name=name, file=file, test=test, control_rc=control.returncode, mutant_rc=mutant.returncode, killed=mutant.returncode!=0 and '--- FAIL:' in log and '[build failed]' not in log)
    results.append(result)
    print(json.dumps(result), flush=True)
(OUT/'root-followup-mutations.json').write_text(json.dumps(dict(root=str(ROOT), results=results), indent=2)+'\n')
