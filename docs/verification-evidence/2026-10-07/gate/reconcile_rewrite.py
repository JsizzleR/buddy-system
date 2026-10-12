from pathlib import Path
P=Path('~/Projects/buddy-system/scripts')
helper='''
def reconcile_boundary(response, identity, message_id, delivery, wire):
    """D-077: known-turn history is withdrawn; a lost-turn journal stays unknown.

    Inspect every adapter-written native frame, not receipt offsets. Fixture
    history captures are explicitly separate and never become adapter evidence.
    """
    if (response.get("schema") != "buddy.control.v1" or response.get("operation") != "reconcile" or
            response.get("identity") != identity or response.get("message_id") != message_id or
            response.get("wake_delivery") != delivery or
            response.get("capabilities", {}).get("reconcile", {}).get("support") != "unsupported" or
            any(response.get(key, {}).get("state") != "unknown"
                for key in ("completion", "acknowledgment", "body_delivery")) or
            any(row.get("type") == "adapter_frame" and
                row.get("frame", {}).get("method") in ("thread/read", "thread/turns/list") for row in wire)):
        return False
    if delivery.get("turn_id"):
        return ("Codex reconcile is unsupported" in response.get("error", "") and
                "turn_observation" not in response)
    observation = response.get("turn_observation", {})
    return (not response.get("error") and observation.get("source") == "transport_journal" and
            observation.get("native_session_id") == identity["session_id"] and
            not observation.get("turn_id") and observation.get("status", {}).get("state") == "unknown")

'''
f=P/'check-interrupted.py';s=f.read_text();s=s.replace('\ndef validate(receipt):',helper+'\ndef validate(receipt):',1)
a=s.index('        check("history_does_not_grant_effects"');b=s.index('        check("claim_remains_open"',a)
s=s[:a]+'''        check("reconcile_withdrawn_without_native_query", lambda: reconcile_boundary(
            c["reconcile"], c["identity"], mid, c["wake"]["wake_delivery"],
            [json.loads(line) for line in e.path(role + "-state/native-observations.jsonl").read_text().splitlines()]) and
            c["reconcile"] in e.session(role)["events"])
'''+s[b:]
s=s.replace('check("unfinished_turn_recovers_interrupted",','check("unfinished_turn_remains_unobserved",')
s=s.replace('for v in c["crash"]["events"]) and c["reconcile"]["turn_observation"]["status"]["state"] == "interrupted")','for v in c["crash"]["events"]) and "turn_observation" not in c["reconcile"])')
s=s.replace('c["result"]["native_event"]["turn_id"] == turn and\n                  c["reconcile"]["turn_observation"]["status"]["state"] == "interrupted")','c["result"]["native_event"]["turn_id"] == turn and "turn_observation" not in c["reconcile"])')
s=s.replace('check("dropped_result_recovered_once",','check("dropped_result_is_fixture_evidence_only",')
s=s.replace('c["dropped"]["params"]["turn"]["status"] == c["reconcile"]["turn_observation"]["status"]["state"] == "completed"','c["dropped"]["params"]["turn"]["status"] == "completed" and "turn_observation" not in c["reconcile"]')
s=s.replace('(("reconcile", "turn_observation", "turn_id"), "other"),\n            (("reconcile", "turn_observation", "status", "state"), "unsupported"),','(("reconcile", "error"), ""),\n            (("reconcile", "wake_delivery", "turn_id"), "other"),\n            (("reconcile", "turn_observation"), {"source": "native_thread_read"}),\n            (("reconcile", "capabilities", "reconcile", "support"), "experimental"),')
f.write_text(s)
loader='''spec = importlib.util.spec_from_file_location("codex_reconcile_boundary", Path(__file__).with_name("check-interrupted.py"))
reconcile_checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reconcile_checks)
'''
for name in ['uncertain','compaction','interrupted-compaction']:
 f=P/('check-'+name+'.py');s=f.read_text();needle='spec.loader.exec_module(base)\n';s=s.replace(needle,needle+loader,1)
 if name=='uncertain':
  s=s.replace('c["reconcile"]["message_id"] == mid and not c["reconcile"].get("error"))','c["reconcile"]["message_id"] == mid)')
  point=s.index('        check("claim_preserved"')
  s=s[:point]+'''        check("reconcile_withdrawal_boundary", lambda: reconcile_checks.reconcile_boundary(
            c["reconcile"], c["identity"], mid, c["duplicate"]["wake_delivery"], lines("native-observations.jsonl")) and
            c["reconcile"] in session["events"])
'''+s[point:]
  a=s.index('            expected = "completed"');b=s.index('            if role == "approval_forwarded":',a);s=s[:a]+s[b:]
  s=s.replace('            (("reconcile", "turn_observation", "status", "state"), "unsupported"),\n','            (("reconcile", "capabilities", "reconcile", "support"), "experimental"),\n')
  needle='            changes += [(prefix + path, value) for path, value in (\n'
  s=s.replace(needle,needle+'                (("reconcile", "error"), ""),\n                (("reconcile", "turn_observation"), {"source": "native_thread_read"}),\n',1)
 else:
  a=s.index('    def history():');b=s.index('\n    e.check(',s.index('    e.check(',a)+1)
  s=s[:a]+'''    e.check("reconcile_withdrawn_without_native_query", lambda: reconcile_checks.reconcile_boundary(
        r["reconcile"], r["sessions"][ROLE]["identity"], mid(), r["approval"]["wake"]["wake_delivery"], wire()) and
        r["reconcile"] in events())
'''+s[b:]
  s=s.replace('(("reconcile", "turn_observation", "turn_id"), "wrong"),','(("reconcile", "error"), ""),\n        (("reconcile", "turn_observation"), {"source": "native_thread_read"}),\n        (("reconcile", "capabilities", "reconcile", "support"), "experimental"),')
 f.write_text(s)
for name in ['interrupted','uncertain','compaction','interrupted-compaction']:
 f=P/('qualify-'+name+'.py');s=f.read_text()
 s=s.replace('"reconcile", message_id=mid)','"reconcile", permit_error=True, message_id=mid)')
 if name in ['interrupted','interrupted-compaction']:
  s=s.replace('client = fleet.clients.pop(role)','client = fleet.clients[role]').replace('client = fleet.clients.pop(ROLE)','client = fleet.clients[ROLE]')
  # Only the fault/restart close, not finally close loops.
  pos=s.index('    client.close()');end=pos+len('    client.close()')
  s=s[:end]+'\n    fleet.clients.pop('+('role' if name=='interrupted' else 'ROLE')+')'+s[end:]
 f.write_text(s)
