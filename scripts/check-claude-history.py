#!/usr/bin/env python3
"""Check history qualification from original captures, without starting a harness.

Conversation rows can correlate an input and still lack terminal evidence. This
checker deliberately requires both a completed positive control and refusals;
an empty history result alone cannot pass as a qualified lookup or safe retry.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import sys
import tempfile

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('history_evidence', Path(__file__).with_name('check-mixed-fleet.py'))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ROLE, STATE = 'claude_worker', 'claude_worker-state/'
MODES = ('completed', 'lost_result', 'lost_ack', 'crash_active', 'crash_approval')
STAGES = ('before_fault', 'after_fault', 'before_lookup', 'after_lookup', 'after_resume')


def evidence_paths(r):
    return [r[k] for k in (*STAGES, 'ledger')] + [STATE + p for p in (
        'identity.json', 'identity.json.events', 'native-observations.jsonl', 'launch.json',
        'history.json', 'missing-history.json', 'history.stderr', 'missing-history.stderr')] + [
        'native-versions.json', 'sdk-contract.json',
        ROLE + '-provider.json', ROLE + '/claude.txt', ROLE + '/codex.txt'] + (
        [ROLE + '/.fixture/marker.txt'] if r['mode'] == 'crash_active' else [])


def validate_case(r, overrides=None):
    e = base.Evidence(r, overrides)
    mode = r['mode']
    def read(path): return e.read(path)
    def lines(path): return [json.loads(v) for v in e.path(path).read_text().splitlines()]
    def digest(path): return hashlib.sha256(e.path(path).read_bytes()).hexdigest()
    def wire(): return lines(STATE + 'native-observations.jsonl')
    def frames(kind): return [v['frame'] for v in wire() if v['type'] == kind]
    def journal(): return lines(STATE + 'identity.json.events')
    def history(): return read(STATE + 'history.json')
    def pair(): return e.pair(ROLE)
    def tid(): return r['native_input_uuid']
    def mid(): return r['queue']['delivery']['message_id']
    def native_results(): return [v for v in frames('native_frame') if v.get('type') == 'result']
    def events(): return r['restart']['before']['events'] + r['sessions'][ROLE]['events']
    e.check('schema_and_pins', lambda: r['schema'] == 'buddy.claude-history.v1' and mode in MODES and
        not r.get('error') and
        r['provider'] == 'deterministic-loopback' and r['loopback'] in ('127.0.0.1', '::1') and
        set(r['sessions']) == {ROLE} and
        r['sdk']['version'] == '0.3.202' and set(r['sdk']['sha256']) == {'sdk.mjs', 'sdk.d.ts', 'package.json'} and
        all(re.fullmatch(r'[0-9a-f]{64}', v) for v in r['sdk']['sha256'].values()))
    e.check('exact_native_contract', lambda: e.contract(ROLE))
    e.check('exact_SDK_contract', lambda: read('sdk-contract.json') == r['sdk'] and all(
        read(STATE + name)['sdk_version'] == r['sdk']['version'] and
        read(STATE + name)['sdk_sha256'] == r['sdk']['sha256']
        for name in ('history.json', 'missing-history.json')))
    e.check('built_artifact_hashes', lambda: all(digest(path) == r[key] for path, key in (
        ('source-manifest.json', 'source_manifest_sha256'), ('bin/buddy', 'buddy_sha256'), ('bin/buddy-adapter', 'adapter_sha256'))))
    e.check('original_evidence_hashes', lambda: all(digest(p) == r['evidence_sha256'][str(e.path(p).relative_to(e.root))]
        for p in evidence_paths(r)))
    e.check('captured_identity', lambda: r['sessions'][ROLE]['identity'] == read(STATE + 'identity.json')['identity'] and
        bool(journal()) and all(v['identity'] == r['sessions'][ROLE]['identity'] for v in journal()))
    def exact_input():
        inputs = [v for v in frames('adapter_frame') if v.get('type') == 'user']
        replays = [v for v in frames('native_frame') if v.get('type') == 'user' and v.get('isReplay') is True]
        return (len(inputs) == len(replays) == 1 and inputs[0]['uuid'] == replays[0]['uuid'] == tid() and
            replays[0]['session_id'] == pair()[0])
    e.check('one_input_native_acceptance', exact_input)
    def lookup_matches():
        h = history()
        if (h['schema'] != 'buddy.claude-history-lookup.v1' or h['sdk_version'] != r['sdk']['version'] or
            h['session_id'] != pair()[0] or h['worktree'] != r['sessions'][ROLE]['worktree']): return False
        # Native acceptance precedes durable history at the approval seam.
        # An early crash can leave no SDK-visible chain. Require availability
        # in the terminal controls, and label this measured absence separately.
        if not h['messages'] or not h['system_messages']:
            return mode.startswith('crash_') and h['messages'] == h['system_messages'] == []
        raw = next(v for v in frames('adapter_frame') if v.get('type') == 'user')
        for rows in (h['messages'], h['system_messages']):
            matches = [v for v in rows if v['uuid'] == tid()]
            if (len(matches) != 1 or matches[0]['type'] != 'user' or matches[0]['message'] != raw['message'] or
                any(v['session_id'] != pair()[0] for v in rows)): return False
        return True
    e.check('SDK_input_correlation_or_observed_absence', lookup_matches)
    e.check('SDK_history_has_no_terminal_receipt', lambda: all(
        v['type'] in ('user', 'assistant', 'system') and
        not any(k in v for k in ('terminal_reason', 'user_message_uuid', 'user_message_uuids', 'queued_turn_count'))
        for rows in (history()['messages'], history()['system_messages']) for v in rows))
    e.check('missing_history_and_UUID_are_empty', lambda: r['missing_id'] != pair()[0] and
        history()['missing_id'] == r['missing_id'] and history()['missing_messages'] == [] and
        read(STATE + 'missing-history.json')['session_id'] == pair()[0] and
        all(read(STATE + 'missing-history.json')[k] == [] for k in ('messages', 'system_messages', 'missing_messages')))
    e.check('lookup_read_only_no_native_launch', lambda: r['lookup_before'] == r['lookup_after'] and
        bool(r['lookup_before']['native']) and r['lookup_before']['empty_native'] == {} and
        e.path(r['before_lookup']).read_bytes() == e.path(r['after_lookup']).read_bytes() and
        e.path(STATE + 'history.stderr').read_text() == e.path(STATE + 'missing-history.stderr').read_text() == '')
    def terminal_control():
        raw = native_results()
        if mode in ('completed', 'lost_result', 'lost_ack'):
            if len(raw) != 1: return False
            raw = raw[0]
            return (raw['session_id'] == pair()[0] and raw['user_message_uuid'] == tid() and
                raw['user_message_uuids'] == [tid()] and raw['terminal_reason'] == 'completed' and
                raw['queued_turn_count'] == 0 and raw['subtype'] == 'success' and raw['is_error'] is False and
                any(v['type'] == 'assistant' and v['message'].get('stop_reason') == 'end_turn'
                    for v in history()['system_messages']))
        return not raw and (not history()['system_messages'] or
            any(v['type'] == 'assistant' and v['message'].get('stop_reason') == 'tool_use'
                for v in history()['system_messages']))
    e.check('native_terminal_vs_conversation_control', terminal_control)
    def dropped():
        raw = frames('dropped_frame')
        expected = (['result'] if mode == 'lost_result' else ['user', 'result'] if mode == 'lost_ack' else [])
        return [v['type'] for v in raw] == expected and all(v in frames('native_frame') for v in raw)
    e.check('exact_output_fault', dropped)
    def acceptance():
        rows = [v for v in journal() if v.get('message_id') == mid() and v.get('wake_delivery')]
        if not rows or rows[0]['wake_delivery'] != {'state': 'unknown'}: return False
        if mode == 'lost_ack':
            return len(rows) == 1 and 'wake' not in r
        return (r['wake'] in rows and r['wake']['wake_delivery'] == {'state': 'delivered', 'turn_id': tid()} and
            r['wake'] in r['restart']['before']['events'])
    e.check('journal_acceptance_distinct_from_history', acceptance)
    def seam():
        if mode not in ('crash_active', 'crash_approval'): return True
        obs = r['approval']['native_event']['approval']
        if (r['approval'] not in journal() or obs['native_session_id'] != pair()[0] or obs['turn_id'] != tid() or
            not any(v.get('type') == 'control_request' and v.get('request_id') == obs['id'] and
                v.get('request') == obs['detail'] for v in frames('native_frame'))): return False
        if mode == 'crash_active':
            return (r['provider_barrier'] is True and r['response_released_at_fault'] is False and
                r['approved'] in journal() and r['approved']['approval_delivery']['state'] == 'submitted' and
                e.path(ROLE + '/.fixture/marker.txt').read_text() == 'once\n' and
                len(r['script']['results']) == 1 and r['script']['results'][0]['exit_code'] == 0)
        return not r['script']['results'] and 'approved' not in r and not (e.root / ROLE / '.fixture/marker.txt').exists()
    e.check('crash_seam_and_effect_control', seam)
    def journal_terminal():
        results = [v for v in journal() if v.get('native_event', {}).get('kind') in ('result', 'idle')]
        if mode != 'completed': return not results
        return (len(results) == 2 and [v['native_event']['kind'] for v in results] == ['result', 'idle'] and
            all(v['native_event']['turn_id'] == tid() and v in events() for v in results) and
            results[0]['native_event']['result_ref'] == native_results()[0]['uuid'])
    e.check('terminal_journal_matches_delivered_frames', journal_terminal)
    if mode == 'completed':
        e.check('completed_positive_resume_and_dedup', lambda: not r['restart']['resume'].get('error') and
            r['restart']['resume']['identity'] == r['sessions'][ROLE]['identity'] and r['restart']['resume'] in journal() and
            r['restart']['resume']['state']['state'] == r['restart']['resume']['pending_approval']['state'] == 'unknown' and
            r['duplicate']['wake_delivery'] == r['wake']['wake_delivery'] and
            r['requests_after_duplicate'] == r['requests_after_resume'] == r['lookup_after']['requests'])
    else:
        e.check('uncertain_resume_refuses_before_effects', lambda: 'unfinished or uncertain' in r['restart']['resume']['error'] and
            r['refusal_exit'] != 0 and r['requests_after_resume'] == r['lookup_after']['requests'] and
            r['journal_after_refusal'] == r['lookup_after']['journal'] == digest(STATE + 'identity.json.events') and
            r['wire_after_refusal'] == r['lookup_after']['wire'] == digest(STATE + 'native-observations.jsonl') and
            e.path(r['after_lookup']).read_bytes() == e.path(r['after_resume']).read_bytes())
    def launches():
        args = [v['args'] for v in wire() if v['type'] == 'buddy_fixture_native_process' and '--print' in v['args']]
        return (len(args) == (2 if mode == 'completed' else 1) and args[0][args[0].index('--session-id')+1] == pair()[0] and
            (mode != 'completed' or args[1][args[1].index('--resume')+1] == pair()[0]))
    e.check('only_owned_expected_launches', launches)
    e.check('logical_identity_survives', lambda: all(e.one(r[k],
        'SELECT incarnation,ended FROM sessions WHERE session_id=?', (pair()[0],)) ==
        {'incarnation': pair()[1], 'ended': None} for k in STAGES))
    for name, query in (('claims', 'SELECT * FROM claims ORDER BY claim_id'),
        ('READY', 'SELECT * FROM session_waits ORDER BY session_id'),
        ('READY_targets', 'SELECT * FROM session_wait_targets ORDER BY decl,ord')):
        e.check(name + '_preserved', lambda query=query: bool(e.rows(r['before_fault'], query)) and
            all(e.rows(r['before_fault'], query) == e.rows(r[k], query) for k in STAGES[1:]))
    e.check('READY_exact_commit', lambda: base.SHA.fullmatch(r['ready_sha']) and
        base.git(e.path(r['sessions'][ROLE]['worktree']), 'rev-parse', 'HEAD') == r['ready_sha'] and
        e.one(r['after_resume'], 'SELECT ready_sha,cleared FROM session_waits WHERE session_id=?', (pair()[0],)) ==
        {'ready_sha': r['ready_sha'], 'cleared': None})
    e.check('original_body_stays_queued', lambda: all(not e.rows(r[k],
        'SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?', (mid(), pair()[0])) for k in STAGES))
    e.check('no_history_promoted_to_work_or_approval', lambda: all(v['completion']['state'] == v['acknowledgment']['state'] == 'unknown'
        and v['capabilities']['reconcile']['support'] == 'unsupported' and v['capabilities']['telemetry']['support'] == 'unknown'
        for v in events()) and not any(v.get('turn_observation') for v in journal()))
    e.check('shutdown_release_and_cleanup', lambda: not r['cleanup_errors'] and not r.get('forced_cleanup_groups') and
        r['restart']['before']['exit'] == (0 if mode == 'completed' else -9) and not r['restart']['before']['unparsed'] and
        r['restart']['before']['children']['status'] == r['final_child_exit']['status'] == 'observed_absent' and
        r['sessions'][ROLE]['adapter_exit'] == (0 if mode == 'completed' else 1) and not r['sessions'][ROLE]['unparsed'] and
        r['sessions'][ROLE]['native_child_exit']['status'] == 'observed_absent' and
        not e.rows(r['ledger'], "SELECT claim_id FROM claims WHERE state='open'") and
        not read(ROLE + '-provider.json')['errors'] and
        all(v['error_type'] == 'connection_closed' for v in r.get('fault_provider_errors', [])))
    # This is reported alongside the checks, never used to decide admission.
    h = history()
    return dict(passed=all(e.checks.values()), checks=e.checks, errors=e.errors,
        history_input='observed' if any(v.get('uuid') == tid() for v in h['system_messages']) else 'unknown',
        history_terminal='unknown')


def validate(r, overrides=None):
    if r.get('schema') != 'buddy.claude-history-suite.v1' or set(r.get('cases', {})) != set(MODES):
        return dict(passed=False, error='require completed, lost-result, lost-ack and both crash cases')
    try:
        cases = {mode: validate_case(r['cases'][mode], (overrides or {}).get(mode)) for mode in MODES}
        # A suite names one measured version pair and build, not five
        # individually plausible cases silently borrowed from different runs.
        # The shared binding adds the run's own root (F-13); this suite has
        # always written every contract field, so all of them are required.
        bound = base.suite_binding(r, MODES, required=('claude_version', 'native_versions', 'sdk',
            'source_manifest_sha256', 'buddy_sha256', 'adapter_sha256', 'fixture_sources'),
            uniform=('codex_version', 'source_head'))
        return dict(passed=all(v['passed'] for v in cases.values()) and bound['same_contract'] and
            bound['same_run'] and all(r['cases'][m]['mode'] == m for m in MODES), cases=cases, **bound)
    except (KeyError, TypeError, ValueError, OSError) as error:
        return dict(passed=False, error=str(error))


def self_test(r):
    if not validate(r)['passed']: raise AssertionError('positive receipt failed')
    ignored = copy.deepcopy(r)
    ignored['passed'] = False
    for case in ignored['cases'].values():
        case.update(passed=False, validation={'passed': False})
    if not validate(ignored)['passed']: raise AssertionError('checker trusted fixture success flags')
    count = 0
    for mode in MODES:
        changes = [(('adapter_sha256',), 'wrong'), (('ready_sha',), 'wrong'), (('cleanup_errors',), ['failed']),
            (('loopback',), '0.0.0.0'), (('sdk', 'version'), 'unchecked'), (('native_input_uuid',), 'wrong'),
            (('final_child_exit', 'status'), 'unknown'), (('after_lookup',), r['cases'][mode]['ledger']),
            (('lookup_after', 'journal'), 'changed'), (('requests_after_resume',), -1),
            (('restart', 'before', 'children', 'status'), 'unknown'), (('missing_id',), 'wrong'),
            (('evidence_sha256', STATE + 'history.json'), 'changed'),
            (('claude_version',), '2.1.292'), (('sessions', ROLE, 'native_version'), '2.1.292 (Claude Code)'),
            (('sessions', ROLE, 'native_sha256'), '0' * 64), (('error',), 'fixture failed'),
            (('sdk', 'sha256', 'sdk.mjs'), '0' * 64),
            (('evidence_sha256', 'native-versions.json'), 'changed'),
            (('evidence_sha256', 'sdk-contract.json'), 'changed')]
        if mode == 'completed':
            changes += [(('restart', 'resume', 'error'), 'failed'), (('requests_after_duplicate',), -1)]
        else:
            changes += [(('restart', 'resume', 'error'), ''), (('journal_after_refusal',), 'changed'),
                (('wire_after_refusal',), 'changed'), (('refusal_exit',), 0)]
        if mode == 'crash_active': changes += [(('provider_barrier',), False), (('response_released_at_fault',), True)]
        for path, value in changes:
            bad = copy.deepcopy(r); target = bad['cases'][mode]
            for key in path[:-1]: target = target[key]
            target[path[-1]] = value
            if validate(bad)['passed']: raise AssertionError('accepted damaged receipt: ' + mode + '.' + '.'.join(path))
            count += 1
        bad = copy.deepcopy(r); del bad['cases'][mode]
        if validate(bad)['passed']: raise AssertionError('accepted missing case')
        count += 1
        # Change decoded copies after verifying artifact hashes to prove the
        # semantic contract guards, without changing saved native evidence.
        e = base.Evidence(r['cases'][mode])
        corruptions = []
        for field, value in (('harness_version', '2.1.292'), ('permission_mode', 'full-access'),
                             ('worktree', '/different'), ('gate_command', '')):
            launch, saved = e.read(STATE + 'launch.json'), e.read(STATE + 'identity.json')
            launch[field] = value
            saved['launch'] = launch
            corruptions.append(({STATE + 'launch.json': launch, STATE + 'identity.json': saved}, 'exact_native_contract'))
        launch, saved = e.read(STATE + 'launch.json'), e.read(STATE + 'identity.json')
        launch['hooks']['mods'] = 'builtin-only-2.1.292'
        saved['launch'] = launch
        corruptions.append(({STATE + 'launch.json': launch, STATE + 'identity.json': saved}, 'exact_native_contract'))
        saved = e.read(STATE + 'identity.json')
        saved['identity']['incarnation'] = 'stale'
        corruptions.append(({STATE + 'identity.json': saved}, 'exact_native_contract'))
        versions = e.read('native-versions.json')
        versions['claude']['version'] = '2.1.292 (Claude Code)'
        corruptions.append(({'native-versions.json': versions}, 'exact_native_contract'))
        for name in ('history.json', 'missing-history.json'):
            h = e.read(STATE + name)
            h['sdk_sha256']['sdk.mjs'] = '0' * 64
            corruptions.append(({STATE + name: h}, 'exact_SDK_contract'))
        for altered, check in corruptions:
            verdict = validate(r, {mode: altered})
            if verdict['passed'] or verdict['cases'][mode]['checks'].get(check) is not False:
                raise AssertionError('damaged evidence did not fail ' + mode + '.' + check)
            count += 1
    bad = copy.deepcopy(r)
    bad['cases']['lost_ack']['fixture_sources']['claude-history.mjs'] = '0' * 64
    verdict = validate(bad)
    if verdict['passed'] or verdict['same_contract']:
        raise AssertionError('accepted suite assembled from different fixture sources')
    count += 1
    with tempfile.TemporaryDirectory(prefix='buddy-suite-splice-') as scratch:
        count += base.suite_splice_self_test(r, MODES, validate, validate_case, scratch, pinned_by='exact_native_contract')
    return dict(positive='passed', rejected_mutations=count)


def synthetic_case(root, mode):
    """One coherent SYNTHETIC case under root (base.Synthetic says what that is)."""
    s = base.Synthetic(root)
    good, crash = mode == 'completed', mode.startswith('crash_')
    version, sid, inc, tid, mid = '2.1.291', '33333333-4444-4555-8666-' + str(MODES.index(mode)) * 12, 'inc-' + mode, 'input-' + mode, 3
    ident = {'schema': 'buddy.lifecycle.v1', 'harness': 'claude', 'session_id': sid, 'incarnation': inc}
    ev = lambda op, **f: base.control(op, ident, **f)
    frame = lambda kind, value: {'type': kind, 'frame': value}
    worktree = str(s.root / ROLE)
    sdk = {'version': '0.3.202', 'sha256': {'sdk.mjs': '1' * 64, 'sdk.d.ts': '2' * 64, 'package.json': '3' * 64}}
    versions = {'claude': {'version': version + ' (Claude Code)', 'executable_sha256': 'a' * 64}}
    launch = {'worktree': worktree, 'harness_version': version, 'permission_mode': 'workspace-write',
              'gate_command': 'fixture gate', 'hooks': {'configured': 'observed', 'observed': 'unknown',
                                                        'mods': 'builtin-only-' + version}}
    message = {'role': 'user', 'content': 'Buddy peer notification: message #3 is queued.'}
    user = {'type': 'user', 'uuid': tid, 'message': message}
    replay = {'type': 'user', 'uuid': tid, 'isReplay': True, 'session_id': sid}
    result = {'type': 'result', 'uuid': 'res-' + mode, 'session_id': sid, 'user_message_uuid': tid,
              'user_message_uuids': [tid], 'terminal_reason': 'completed', 'queued_turn_count': 0,
              'subtype': 'success', 'is_error': False}
    detail = {'subtype': 'can_use_tool', 'tool_name': 'Bash'}
    start, intent = ev('start'), ev('send', message_id=mid, wake_delivery={'state': 'unknown'})
    journal = [start, intent]
    wire = [{'type': 'buddy_fixture_native_process', 'pid': 300, 'args': ['--print', '--session-id', sid]},
            frame('adapter_frame', user), frame('native_frame', replay)]
    r = {'schema': 'buddy.claude-history.v1', 'mode': mode, 'root': str(s.root), 'provider': 'deterministic-loopback',
         'loopback': '127.0.0.1', 'claude_version': version, 'native_versions': versions, 'sdk': sdk,
         'native_input_uuid': tid, 'queue': {'delivery': {'message_id': mid}}, 'missing_id': 'missing-session',
         'cleanup_errors': [], 'final_child_exit': {'status': 'observed_absent'}}
    if mode != 'lost_ack':
        wake = ev('send', message_id=mid, wake_delivery={'state': 'delivered', 'turn_id': tid})
        journal.append(wake)
        r['wake'] = wake
    assistant = lambda stop: {'type': 'assistant', 'uuid': 'a-' + mode, 'session_id': sid, 'message': {'stop_reason': stop}}
    rows = [dict(user, session_id=sid), assistant('tool_use' if crash else 'end_turn')]
    if mode == 'crash_approval':
        rows = []
    if not crash:
        wire.append(frame('native_frame', result))
        if mode == 'lost_result':
            wire.append(frame('dropped_frame', result))
        if mode == 'lost_ack':
            wire += [frame('dropped_frame', replay), frame('dropped_frame', result)]
    else:
        obs = {'id': 'req-1', 'native_session_id': sid, 'turn_id': tid, 'detail': detail}
        approval = ev('event', native_event={'kind': 'approval', 'native_session_id': sid, 'approval': obs})
        journal.append(approval)
        wire.append(frame('native_frame', {'type': 'control_request', 'request_id': 'req-1', 'request': detail}))
        r.update(approval=approval, fault_provider_errors=[{'error_type': 'connection_closed'}])
        if mode == 'crash_active':
            approved = ev('approve', approval_response={'id': 'req-1', 'turn_id': tid}, approval_delivery={'state': 'submitted'})
            journal.append(approved)
            s.write(ROLE + '/.fixture/marker.txt', 'once\n')
            r.update(approved=approved, provider_barrier=True, response_released_at_fault=False,
                     script={'results': [{'exit_code': 0}]})
        else:
            r['script'] = {'results': []}
    if good:
        terminal = ev('event', native_event={'kind': 'result', 'native_session_id': sid, 'turn_id': tid,
                                             'result_ref': result['uuid'], 'result_status': 'completed'})
        idle = ev('event', native_event={'kind': 'idle', 'native_session_id': sid, 'turn_id': tid})
        journal += [terminal, idle]
    before_events = list(journal)
    if good:
        resume = ev('resume', pending_approval={'state': 'unknown'})
        journal.append(resume)
        wire.append({'type': 'buddy_fixture_native_process', 'pid': 301, 'args': ['--print', '--resume', sid]})
        after_events = [resume]
    else:
        resume = ev('resume', error='refused: the last turn is unfinished or uncertain')
        after_events = []
    lookup = lambda: {'schema': 'buddy.claude-history-lookup.v1', 'sdk_version': sdk['version'], 'sdk_sha256': sdk['sha256'],
                      'session_id': sid, 'worktree': worktree}
    s.write(STATE + 'history.json', dict(lookup(), messages=rows, system_messages=rows,
                                         missing_id=r['missing_id'], missing_messages=[]))
    s.write(STATE + 'missing-history.json', dict(lookup(), messages=[], system_messages=[], missing_messages=[]))
    s.write(STATE + 'history.stderr', '')
    s.write(STATE + 'missing-history.stderr', '')
    s.jsonl(STATE + 'identity.json.events', journal)
    s.jsonl(STATE + 'native-observations.jsonl', wire)
    s.write(STATE + 'launch.json', launch)
    s.write(STATE + 'identity.json', {'schema': 'buddy.control.v1', 'identity': ident, 'launch': launch})
    s.write('native-versions.json', versions)
    s.write('sdk-contract.json', sdk)
    s.write(ROLE + '-provider.json', {'errors': [], 'requests': [{}, {}]})
    ready = s.repo(ROLE)
    s.write(ROLE + '/claude.txt', 'allowed\n')
    s.write(ROLE + '/codex.txt', 'original\n')
    journal_digest, wire_digest = s.digest(STATE + 'identity.json.events'), s.digest(STATE + 'native-observations.jsonl')
    state = {'native': {'pid': 300}, 'empty_native': {}, 'requests': 2, 'journal': journal_digest, 'wire': wire_digest}
    r.update(lookup_before=state, lookup_after=copy.deepcopy(state), requests_after_resume=2, ready_sha=ready,
             restart={'resume': resume, 'before': {'exit': 0 if good else -9, 'unparsed': [], 'events': before_events,
                                                   'children': {'status': 'observed_absent'}}})
    if good:
        r.update(duplicate={'message_id': mid, 'wake_delivery': r['wake']['wake_delivery']}, requests_after_duplicate=2)
    else:
        r.update(refusal_exit=1, journal_after_refusal=journal_digest, wire_after_refusal=wire_digest)
    claims = [('c1', 'run-history', 'owner', 'owner-inc', 'open', ''), ('c2', 'worker', sid, inc, 'open', '')]
    first = Path(s.ledger(STAGES[0], sessions=[(sid, inc, None)], claims=claims, waits=[(sid, inc, 'd1', ready, None)],
                          targets=[('d1', sid, inc, 0, 'c1', 'run-history')]))
    r[STAGES[0]] = str(first)
    for stage in STAGES[1:]:
        # Byte-identical copies: the lookup must leave the ledger's bytes alone.
        (s.root / (stage + '.db')).write_bytes(first.read_bytes())
        r[stage] = str(s.root / (stage + '.db'))
    r['ledger'] = s.ledger('final', sessions=[(sid, inc, None)], claims=[c[:4] + ('released', 'pass') for c in claims])
    r['sessions'] = {ROLE: {'identity': ident, 'worktree': worktree, 'native_version': versions['claude']['version'],
                            'native_sha256': versions['claude']['executable_sha256'], 'events': after_events,
                            'adapter_exit': 0 if good else 1, 'unparsed': [], 'native_child_exit': {'status': 'observed_absent'}}}
    s.built(r)
    base.synthetic_contract(r, r['sessions'].values())
    r['fixture_sources'] = {'claude-history.mjs': 'c' * 64, 'qualify-claude-history.py': 'd' * 64}
    return s.seal(r, evidence_paths(r))


def synthetic_suite(top):
    Path(top).mkdir(parents=True, exist_ok=True)
    top = Path(top).resolve(strict=True)
    return {'schema': 'buddy.claude-history-suite.v1', 'root': str(top),
            'cases': {mode: synthetic_case(top / mode, mode) for mode in MODES}}


def synthetic_self_test():
    """Every receipt mutation, contract corruption and splice above, against synthetic evidence."""
    with tempfile.TemporaryDirectory(prefix='buddy-claude-history-synthetic-') as tmp:
        r = synthetic_suite(Path(tmp) / 'suite')
        out = self_test(r)
        wire, journal = STATE + 'native-observations.jsonl', STATE + 'identity.json.events'
        for mode in MODES:
            plans = [(wire, lambda rows: rows.append(copy.deepcopy(next(v for v in rows if v['type'] == 'adapter_frame'))),
                      'one_input_native_acceptance'),
                     (wire, lambda rows: rows.append({'type': 'buddy_fixture_native_process', 'pid': 9, 'args': ['--print', '--resume', 'x']}),
                      'only_owned_expected_launches'),
                     (journal, lambda rows: rows.append(dict(copy.deepcopy(rows[0]), turn_observation={'source': 'history'})),
                      'no_history_promoted_to_work_or_approval')]
            if mode == 'completed':
                plans.append((journal, lambda rows: rows.pop(), 'completed_positive_resume_and_dedup'))
            else:
                plans.append((wire, lambda rows: rows.append({'type': 'dropped_frame', 'frame': {'type': 'system'}}),
                              'exact_output_fault'))
            # launch.json pins the absolute worktree (exact_native_contract), so
            # no relocated copy can pass: forge a privately built case instead.
            with tempfile.TemporaryDirectory(prefix='buddy-forged-') as scratch:
                case = synthetic_case(Path(scratch) / mode, mode)
                out['rejected_mutations'] += base.forged_jsonl_self_test(case, validate_case, plans, scratch, relocate=False)
                out['rejected_mutations'] += base.hash_binding_self_test(case, validate_case, evidence_paths, scratch, relocate=False)
        out['evidence'] = 'synthetic: tests the checker, qualifies no native harness'
        return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('receipt', type=Path, nargs='?')
    parser.add_argument('--self-test', action='store_true',
                        help='with no receipt, run against synthetic evidence built in a temp dir (hermetic)')
    args = parser.parse_args()
    if args.receipt is None:
        if not args.self_test:
            parser.error('a receipt is required unless --self-test')
        print(json.dumps(synthetic_self_test(), indent=2))
        return 0
    r = json.loads(args.receipt.read_text())
    out = self_test(r) if args.self_test else validate(r)
    print(json.dumps(out, indent=2))
    return 0 if args.self_test or out['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
