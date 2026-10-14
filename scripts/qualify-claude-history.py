#!/usr/bin/env python3
"""Measure supported Claude history lookup before widening recovery admission.

An input UUID or an assistant end_turn is not the native terminal result.
Five disposable cases compare SDK history with original wire/journal evidence;
the helper launches no model and its output is never spliced into the journal.
Requires an explicitly supplied existing SDK 0.3.202 directory. Installs nothing.
"""
import argparse
import datetime
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import traceback
import uuid

sys.dont_write_bytecode = True
REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("history_interrupted", REPO / "scripts/qualify-claude-interrupted.py")
interrupted = importlib.util.module_from_spec(spec)
spec.loader.exec_module(interrupted)
resume, mixed, common, ROLE = interrupted.resume, interrupted.mixed, interrupted.common, interrupted.ROLE
control, snap = resume.control, resume.snap
MODES = ("completed", "lost_result", "lost_ack", "crash_active", "crash_approval")


def lines(path):
    return [json.loads(line) for line in path.read_text().splitlines()]


RELAY = "sys.stdout.buffer.write(line);sys.stdout.buffer.flush()"


def observer(mode):
    def build(path, native, trace):
        resume.observer(path, native, trace)
        # Record the original first, then drop only named output frames. The
        # result marker is an explicit barrier, not an elapsed-time guess.
        source = path.read_text()
        # EXACTLY ONE relay line, or refuse (F-40). An unchecked str.replace
        # whose target a refactor of resume.observer renamed would build a
        # proxy that drops nothing: a lost_result case then fails late and
        # confusingly, and a completed control proves nothing about the drop.
        found = source.count(RELAY)
        if found != 1:
            raise RuntimeError("history fault injector for mode %r is UNARMED: resume.observer's proxy "
                               "has %d native-output relay lines, want exactly 1" % (mode, found))
        source = source.replace(RELAY, '''try:
  frame=json.loads(line)
 except ValueError:frame={}
 drop=MODE in ('lost_result','lost_ack') and (frame.get('type')=='result' or
  (MODE=='lost_ack' and frame.get('type')=='user' and frame.get('isReplay') is True))
 if drop:
  record('dropped_frame',frame=frame)
  if frame.get('type')=='result':
   with open(TRACE+'.terminal','w') as marker:marker.write(frame['uuid'])
  continue
 sys.stdout.buffer.write(line);sys.stdout.buffer.flush()'''.replace("MODE", repr(mode)).replace("TRACE", repr(str(trace))))
        path.write_text(source)
    return build


def native_files(state):
    # With the owned child absent these hashes detect any SDK-side writes,
    # including transcript changes. No production native directory is scanned.
    return {str(p.relative_to(state)): common.digest(p) for p in state.rglob('*') if p.is_file()}


def preflight(fleet, args):
    # A PATH upgrade must fail before a build, provider or native worker. The
    # shared fleet rechecks these exact bytes/version immediately before launch.
    native = Path(args.claude).resolve(strict=True)
    args.claude = str(native)
    env = dict(fleet.env, CLAUDE_CONFIG_DIR=str(fleet.root / 'version-claude'), DISABLE_AUTOUPDATER='1')
    observed = common.run([native, '--version'], fleet.root, env).strip()
    versions = {'claude': dict(version=observed, executable_sha256=common.digest(native))}
    common.write_json(fleet.root / 'native-versions.json', versions)
    fleet.result['native_versions'] = versions
    if observed != args.claude_version + ' (Claude Code)':
        raise RuntimeError('Claude native version differs from the qualification contract: ' + observed)


def lookup(fleet, args):
    r = fleet.result
    state = fleet.root / (ROLE + '-state')
    (state / 'empty-native').mkdir()
    r['before_lookup'] = snap(fleet, 'before-lookup')
    r['lookup_before'] = dict(native=native_files(state / 'native'),
        empty_native=native_files(state / 'empty-native'),
        journal=common.digest(fleet.receipts[ROLE].with_suffix('.json.events')),
        wire=common.digest(state / 'native-observations.jsonl'), requests=len(fleet.providers[ROLE].requests))
    r['missing_id'] = str(uuid.uuid4())
    argv = [args.node, REPO / 'scripts/claude-history.mjs', args.sdk, fleet.identities[ROLE]['session_id'],
        fleet.worktrees[ROLE], r['missing_id']]
    for name, native in (('history', state / 'native'), ('missing-history', state / 'empty-native')):
        native.mkdir(exist_ok=True)
        p = subprocess.run([str(x) for x in argv], cwd=fleet.worktrees[ROLE], env=resume.native_env(fleet, native),
            text=True, capture_output=True, timeout=20)
        (state / (name + '.json')).write_text(p.stdout)
        (state / (name + '.stderr')).write_text(p.stderr)
        if p.returncode or p.stderr:
            raise RuntimeError('SDK history lookup failed: ' + p.stderr)
        json.loads(p.stdout)
    r['after_lookup'] = snap(fleet, 'after-lookup')
    r['lookup_after'] = dict(native=native_files(state / 'native'),
        empty_native=native_files(state / 'empty-native'),
        journal=common.digest(fleet.receipts[ROLE].with_suffix('.json.events')),
        wire=common.digest(state / 'native-observations.jsonl'), requests=len(fleet.providers[ROLE].requests))


def exercise(fleet, mode, args):
    r, work = fleet.result, fleet.worktrees[ROLE]
    sid = fleet.identities[ROLE]['session_id']
    provider, client = fleet.providers[ROLE], fleet.clients[ROLE]
    state = fleet.root / (ROLE + '-state')
    fleet.run([fleet.buddy, 'hello', '--session', 'fixture-owner'])
    fleet.run([fleet.buddy, 'claim', 'run-history', '--session', 'fixture-owner', '--scope', '.buddy/slot/history'])
    fleet.run([fleet.buddy, 'claim', 'worker', '--session', sid, '--scope', 'claude.txt'], work)
    fleet.run([fleet.buddy, 'wait', '--session', sid, '--on', 'run-history', '--ready', 'HEAD'], work)
    r['ready_sha'] = fleet.run(['git', 'rev-parse', 'HEAD'], work).strip()
    r['queue'] = queued = fleet.queue(ROLE, ROLE, 'Original body stays queued; history is not permission to retry.')
    r['before_fault'] = snap(fleet, 'before-fault')
    release, arrived, finished = threading.Event(), threading.Event(), threading.Event()
    original = provider._respond
    if mode == 'crash_active':
        def held(request, path):
            response = original(request, path)
            if len(provider.requests) == 2:
                arrived.set()
                if not release.wait(30):
                    raise RuntimeError('active-response barrier expired')
                finished.set()
            return response
        provider._respond = held
    crash = mode != 'completed'
    try:
        if mode == 'completed':
            r['wake'], r['output'] = fleet.turn(ROLE, mode, queued, [])
        else:
            steps = [] if mode.startswith('lost_') else [fleet.shell(ROLE, 'marker',
                "(work/'.fixture/marker.txt').open('a').write('once\\n')")]
            scripted = provider.queue_turn(mode, steps)
            request = dict(schema='buddy.control.v1', id='original-wake', operation='send',
                identity=fleet.identities[ROLE], message_id=queued['delivery']['message_id'])
            client.send(request)
            # lost_ack is killed while Send still awaits its acknowledgment;
            # the original journal contains intent only, not a guessed turn ID.
            if mode != 'lost_ack':
                r['wake'] = client.wait(lambda v: v.get('request_id') == 'original-wake')
            if mode.startswith('crash_'):
                r['approval'] = client.wait(lambda v: v.get('native_event', {}).get('kind') == 'approval')
                approval = r['approval']['native_event']['approval']
                if not scripted.matches_approval(approval['detail']):
                    raise RuntimeError('unexpected native approval')
                if mode == 'crash_active':
                    r['approved'] = control(fleet, 'allow-marker', 'approve', operator=True,
                        approval=dict(id=approval['id'], native_session_id=sid,
                            turn_id=r['wake']['wake_delivery']['turn_id'], decision='allow'))
                    if not arrived.wait(15):
                        raise RuntimeError('native response barrier not reached')
                    r['provider_barrier'] = arrived.is_set()
                    r['response_released_at_fault'] = release.is_set()
            else:
                deadline = time.monotonic() + 15
                while not (state / 'native-observations.jsonl.terminal').exists():
                    if time.monotonic() > deadline:
                        raise RuntimeError('native terminal drop barrier not reached')
                    time.sleep(.02)
            r['script'] = scripted.snapshot()
        interrupted.stop(fleet, crash)
    finally:
        release.set()
        if mode == 'crash_active':
            finished.wait(5)
            provider._respond = original
    if mode == 'crash_active':
        deadline = time.monotonic() + 2
        while not provider.errors and time.monotonic() < deadline:
            time.sleep(.02)
        r['fault_provider_errors'] = list(provider.errors)
        provider.errors[:] = [v for v in provider.errors if v.get('error_type') != 'connection_closed']
    r['after_fault'] = snap(fleet, 'after-fault')
    inputs = [v['frame'] for v in lines(state / 'native-observations.jsonl')
        if v['type'] == 'adapter_frame' and v['frame'].get('type') == 'user']
    if len(inputs) != 1:
        raise RuntimeError('expected exactly one original native input')
    r['native_input_uuid'] = inputs[0]['uuid']
    lookup(fleet, args)
    replacement = resume.resume_client(fleet, 'resume')
    fleet.clients[ROLE] = replacement
    r['sessions'][ROLE]['events'] = replacement.events
    r['restart']['resume'] = replacement.wait(lambda v: v.get('operation') == 'resume', permit_error=True)
    r['after_resume'] = snap(fleet, 'after-resume')
    r['requests_after_resume'] = len(provider.requests)
    if mode == 'completed':
        if r['restart']['resume'].get('error'):
            raise RuntimeError(r['restart']['resume']['error'])
        r['duplicate'] = control(fleet, 'duplicate', 'send', message_id=queued['delivery']['message_id'])
        r['requests_after_duplicate'] = len(provider.requests)
    else:
        if not r['restart']['resume'].get('error'):
            raise RuntimeError('missing terminal/acceptance admitted resume')
        fleet.clients.pop(ROLE).close()
        r['refusal_exit'] = replacement.process.returncode
        r['sessions'][ROLE].update(adapter_exit=replacement.process.returncode, unparsed=replacement.unparsed,
            native_child_exit=common.observed_child_exit(state / 'native-observations.jsonl'))
        r['journal_after_refusal'] = common.digest(fleet.receipts[ROLE].with_suffix('.json.events'))
        r['wire_after_refusal'] = common.digest(state / 'native-observations.jsonl')
    for slug, owner in (('worker', sid), ('run-history', 'fixture-owner')):
        fleet.run([fleet.buddy, 'release', slug, '--session', owner])
    r['ledger'] = snap(fleet, 'final')


def run_case(root, mode, args):
    root.mkdir()
    result = dict(schema='buddy.claude-history.v1', root=str(root), mode=mode, provider='deterministic-loopback',
        claude_version=args.claude_version,
        started_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), passed=False,
        sessions={}, messages=[], ready=[], validations=[],
        unqualified=['terminal history reconciliation', 'unfinished Claude resume', 'history after compaction',
            'other native/SDK version pairs', 'arbitrary effects', 'real-provider recovery', 'production installation'])
    sources = ('qualify-claude-history.py', 'check-claude-history.py', 'claude-history.mjs',
        'qualify-claude-interrupted.py', 'qualify-claude-resume.py', 'qualify-mixed-fleet.py',
        'mixed_fleet_provider.py', 'qualify-claude.py', 'qualify-codex.py', 'check-mixed-fleet.py')
    result['fixture_sources'] = {name: common.digest(REPO / 'scripts' / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        preflight(fleet, args)
        result['sdk'] = dict(version='0.3.202', path=str(args.sdk),
            sha256={name: common.digest(args.sdk / name) for name in ('package.json', 'sdk.mjs', 'sdk.d.ts')})
        common.write_json(root / 'sdk-contract.json', result['sdk'])
        from mixed_fleet_provider import ScriptedProvider
        result['loopback'] = '::1' if args.ipv6 else '127.0.0.1'
        fleet.prepare(roles=((ROLE, 'claude', 'history-work'),), observer=observer(mode),
            provider_factory=lambda harness: ScriptedProvider(harness, loopback=result['loopback']))
        exercise(fleet, mode, args)
    except Exception:
        result['error'] = traceback.format_exc()
    finally:
        # F-35: natives are identified (pid, kernel start) while their
        # adapter is still alive and verified; never by a pid number later.
        trace = root / (ROLE + '-state') / 'native-observations.jsonl'
        owned = getattr(fleet, 'owned_natives', []) + common.owned_natives(
            trace, [c.owned for c in fleet.clients.values()])
        fleet.close()
        forced = []
        common.end_owned(owned, result.setdefault('cleanup_errors', []), forced)
        if forced:
            result['forced_cleanup_groups'] = forced
        if trace.exists():
            result['final_child_exit'] = common.observed_child_exit(trace)
        if any(common.digest(REPO / 'scripts' / name) != digest for name, digest in result['fixture_sources'].items()):
            result['error'] = result.get('error', '') + '\nFixture source changed during execution.'
        if 'sdk' in result and any(common.digest(args.sdk / name) != digest for name, digest in result['sdk']['sha256'].items()):
            result['error'] = result.get('error', '') + '\nSDK source changed during execution.'
        manifest = root / 'source-manifest.json'
        if manifest.exists() and common.source_manifest(fleet.env) != json.loads(manifest.read_text()):
            result['error'] = result.get('error', '') + '\nGo source changed during execution.'
        if 'error' not in result:
            try:
                checker = mixed.module('history_checker', 'check-claude-history.py')
                result['evidence_sha256'] = {str((root / p).relative_to(root)): common.digest(root / p)
                    for p in checker.evidence_paths(result)}
                result['validation'] = checker.validate_case(result)
                result['passed'] = result['validation']['passed']
            except Exception:
                result['error'] = traceback.format_exc()
        common.write_json(root / 'result.json', result)
        print(json.dumps({'mode': mode, **{k: result.get(k) for k in ('passed', 'validation', 'error', 'cleanup_errors')}}), flush=True)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--claude', default=shutil.which('claude'))
    parser.add_argument('--claude-version', help="defaults to the binary's own version; a typed one must match it")
    parser.add_argument('--node', default=shutil.which('node'))
    parser.add_argument('--sdk', required=True, type=Path, help='existing @anthropic-ai/claude-agent-sdk 0.3.202 directory')
    parser.add_argument('--ipv6', action='store_true')
    args = parser.parse_args()
    # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
    sys.modules["buddy_admission_lib"].bind_args(args)
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    args.sdk = args.sdk.resolve()
    package = json.loads((args.sdk / 'package.json').read_text())
    if package['name'] != '@anthropic-ai/claude-agent-sdk' or package['version'] != '0.3.202':
        parser.error('require the inspected SDK 0.3.202; nothing is installed automatically')
    root = Path(tempfile.mkdtemp(prefix='buddy-claude-history-', dir='/private/tmp'))
    print('ROOT=' + str(root), flush=True)
    cases = {}
    for mode in MODES:
        cases[mode] = run_case(root / mode, mode, args)
        if cases[mode].get('error'):
            break  # Preserve the failed evidence; do not repeat a failed preflight.
    result = dict(schema='buddy.claude-history-suite.v1', root=str(root), cases=cases,
        passed=set(cases) == set(MODES) and all(v['passed'] for v in cases.values()))
    if result['passed']:
        checker = mixed.module('history_suite_checker', 'check-claude-history.py')
        result['validation'] = checker.validate(result)
        result['passed'] = result['validation']['passed']
    common.write_json(root / 'result.json', result)
    print('RECEIPT=' + str(root / 'result.json'), flush=True)
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
