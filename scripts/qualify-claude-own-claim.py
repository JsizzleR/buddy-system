#!/usr/bin/env python3
"""Opt-in native Claude own-claim WARN qualification (D-235), entirely disposable.

WHAT IT MEASURES. A worker buddy-adapter launches with `"own_claim": "warn"`
gets the gate's D-228 warn document for an edit no claim of its own covers
while a peer holds a claim: `{"hookSpecificOutput":{"hookEventName":
"PreToolUse","additionalContext":"BUDDY UNCLAIMED: ..."}}` and NO
permissionDecision. D-229 measured Claude Code delivering it in AUTO mode
only; the adapter runs its workers in MANUAL stream-json mode with stdio
permission prompts (internal/harness/claude.go), and where the line lands in
the Messages API request there is unmeasured. So the loopback provider records
every occurrence of the lead ANYWHERE in each request, not only where we hope
it is, and the checker decides from those positions whether it is bound to the
tool result it belongs to.

WHY FOUR WRITES, in this order (Codex design F3, lens c of D-235's panel):
  0 peer.txt              held by the peer: the gate's holder DENY, no line,
                          and the operator is never asked;
  1 covered.txt           covered by the worker's own claim: no line, allowed;
  2 uncovered-allowed.txt uncovered: the line, then the operator allows;
  3 uncovered-denied.txt  uncovered: the line, then the operator DENIES.
An allow-only leg passes even when the write did not wait for the answer; the
deny leg proves the warn left the decision to the operator. The peer claim is
also what ARMS the own-claim arm (a lone session is never warned). This is a
SEPARATE adapter launch from qualify-claude.py's: that suite's checks pin three
approvals and a four-call shape, and one more Write there breaks them.

HOOK OUTCOMES. The adapter's stdout surfaces no hook event (claude.go passes
--include-hook-events to the native but translates none of those frames), so
"the hook ran, and succeeded, before each approval" is read from a metadata
observer between the native's stdout and the adapter: base.native_observer's
projection plus, for hook frames, their event, name, outcome, exit code and
bounded stdout, and for can_use_tool requests, their tool id and path. That is
recorded as a measured fact in the receipt (`measured.hook_outcome_source`),
not dressed as an adapter observation.

All provider responses come from the deterministic loopback fixture: no real
inference, no plugin installation, no host policy or configuration change, no
provider credentials. Every attempt leaves result.json under /private/tmp.

Run: python3 scripts/qualify-claude-own-claim.py [--claude PATH] [--buddy PATH --adapter PATH]
It LAUNCHES claude: operator-authorized only (CLAUDE.md, Commands).
"""

import argparse
import datetime
import http.server
import importlib.util
import io
import json
import os
import pathlib
import shutil
import sys
import tempfile
import threading
import time
import traceback


REPO = pathlib.Path(__file__).resolve().parent.parent
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("claude_fixture", REPO / "scripts/qualify-claude.py")
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)

SCHEMA = "buddy.claude-own-claim.v1"
LEAD = "BUDDY UNCLAIMED:"  # internal/cli/ownclaim.go's ownClaimLead
LINE_CAP = 2048            # bytes of the one line holding the lead
STDOUT_CAP = 8192          # characters of a hook frame's stdout the observer keeps
# (file, content, operator decision). None: the gate denies, nobody is asked.
CALLS = (("peer.txt", "peer-write\n", None), ("covered.txt", "covered-control\n", "allow"),
         ("uncovered-allowed.txt", "uncovered-allowed\n", "allow"), ("uncovered-denied.txt", "must-not-exist\n", "deny"))
REPLIES = ("operator-allow-covered", "operator-allow-uncovered", "operator-deny-uncovered")
# The files check-claude-own-claim.py reads, hashed into result.json (F-37).
# uncovered-denied.txt is absent on a pass and so is never hashed.
EVIDENCE = ("adapter-events.json", "adapter.stderr", "launch.json", "settings.json", "source-manifest.json",
            "worker.json", "worker.json.events", "worker.json.proc", "provider-observations.json",
            "ledger-inspection.json", "unparsed-output.json", "native-observations.jsonl",
            "bin/claude-observer", "work/peer.txt", "work/covered.txt", "work/uncovered-allowed.txt")
UNQUALIFIED = ["Codex workers (a Codex warn is refused at launch, D-235)",
               "auto mode and every permission mode but the adapter's manual stdio prompts",
               "any Claude build other than the one this run bound",
               "whether the model ACTS on the line (D-228 D10's counts)",
               "real provider inference", "interactive (non stream-json) manual mode",
               "subagents (the adapter hook refuses an agent_id payload)",
               "Edit and NotebookEdit (only Write is exercised)", "a call naming several uncovered paths"]


def lead_positions(request):
    """(occurrences, tool_results) of one Messages request, from the parsed JSON.

    An occurrence is one LINE of one string value holding the lead, wherever it
    is: a message's content (string or block), a tool_result's nested content,
    the system prompt, metadata, a tool description. Each records its JSON path,
    and for a message block its message and block index, the block's type, and
    whether it is inside a tool_result (and that result's tool_use_id). The
    projection is that single line capped at LINE_CAP bytes: fixture-only data,
    it names a disposable path. tool_results is every tool_result block's
    (tool_use_id, message, block), so a sibling block can be bound to the
    result it follows."""
    occurrences, results = [], []
    messages = request.get("messages")
    if isinstance(messages, list):
        for i, message in enumerate(messages):
            content = message.get("content") if isinstance(message, dict) else None
            if isinstance(content, list):
                for j, block in enumerate(content):
                    if isinstance(block, dict) and block.get("type") == "tool_result":
                        results.append({"tool_use_id": block.get("tool_use_id"), "message": i, "block": j})

    def walk(value, path):
        if isinstance(value, dict):
            for k, v in value.items():
                walk(v, path + [k])
        elif isinstance(value, list):
            for k, v in enumerate(value):
                walk(v, path + [k])
        elif isinstance(value, str) and LEAD in value:
            for line in value.split("\n"):
                if LEAD in line:
                    occurrences.append(dict(locate(request, path), path=path, count=line.count(LEAD),
                                            line=line.encode()[:LINE_CAP].decode(errors="ignore")))
    walk(request, [])
    return occurrences, results


def locate(request, path):
    """Where a JSON path sits: message, block, block type, inside a tool_result."""
    out = {"message": None, "role": None, "block": None, "block_type": None, "in_tool_result": False, "tool_use_id": None}
    if len(path) < 3 or path[0] != "messages" or path[2] != "content":
        return out
    message = request["messages"][path[1]]
    out.update(message=path[1], role=message.get("role"))
    if len(path) == 3:
        out["block_type"] = "string"
        return out
    block = message["content"][path[3]]
    out.update(block=path[3], block_type=block.get("type"))
    if block.get("type") == "tool_result":
        out.update(in_tool_result=True, tool_use_id=block.get("tool_use_id"))
    return out


_local = threading.local()


class Recorded(list):
    """server.requests: each entry the base handler appends gets the lead
    positions its own handler thread found (ThreadingHTTPServer: one thread per
    request, and the append happens on it), so an entry is never paired with
    another request's scan."""

    def append(self, entry):
        extra = getattr(_local, "extra", None)
        _local.extra = None
        if extra is not None:
            entry.update(extra)
        super().append(entry)


class OwnClaimFixture(base.MessagesFixture):
    """base.MessagesFixture, plus every lead occurrence and every tool_result id.
    The base fields (path, model, stream, tool_outputs, tools) are kept."""

    def do_POST(self):
        size = int(self.headers.get("Content-Length", "0"))
        if size > 8 << 20:
            self.send_error(413)
            return
        body = self.rfile.read(size)
        _local.extra = None
        try:
            request = json.loads(body)
            occurrences, results = lead_positions(request)
            _local.extra = {"lead_occurrences": occurrences, "tool_results": results,
                            "tool_result_ids": [x["tool_use_id"] for x in results]}
        except ValueError:
            pass  # the base handler refuses it the same way it always has
        self.rfile = io.BytesIO(body)
        super().do_POST()


def hook_observer(path, native, trace):
    """base.native_observer's shape (check-claude-gate.observer_native parses it),
    with two projections added: a hook frame's event, name, outcome, exit code
    and its stdout up to STDOUT_CAP characters (the gate's own document,
    fixture-only data), and a control_request's subtype, tool, tool_use_id and
    file_path (never its input body). Native stdout is forwarded unchanged."""
    path.write_text("#!" + sys.executable + "\n" + '''import json,subprocess,sys
args=sys.argv[1:]
if args!=['--version']:args=[]+args
p=subprocess.Popen(["claude"]+args,executable=NATIVE,stdin=sys.stdin,stdout=subprocess.PIPE,stderr=sys.stderr)
with open(TRACE,'a') as log:
 log.write(json.dumps({'type':'buddy_fixture_native_process','pid':p.pid})+'\\n');log.flush()
 for line in p.stdout:
  try:
   v=json.loads(line)
   t=v.get('type') if isinstance(v,dict) else None
   if t in ('user','result','system','control_response','assistant','control_request'):
    keys=('type','subtype','session_id','uuid','isReplay','origin','parent_tool_use_id','terminal_reason','is_error','plugins','state','user_message_uuid','user_message_uuids','result_index','queued_turn_count','messaging_socket_path')
    projection={k:v[k] for k in keys if k in v}
    projection['native_keys']=sorted(v)
    content=(v.get('message') or {}).get('content')
    if isinstance(content,list):
     projection['tool_blocks']=[{'type':b.get('type'),'id':b.get('id',b.get('tool_use_id')),'name':b.get('name'),
      'is_error':b.get('is_error','<absent>') if b.get('type')=='tool_result' else None,
      'path':(b.get('input') or {}).get('file_path') if isinstance(b.get('input'),dict) else None}
      for b in content if isinstance(b,dict) and b.get('type') in ('tool_use','tool_result')]
    if t=='system' and str(v.get('subtype','')).startswith('hook_'):
     for k in ('hook_id','hook_name','hook_event','outcome','exit_code'):
      if k in v:projection[k]=v[k]
     if isinstance(v.get('stdout'),str):
      projection['stdout']=v['stdout'][:CAP];projection['stdout_chars']=len(v['stdout'])
    if t=='control_request':
     r=v.get('request') if isinstance(v.get('request'),dict) else {}
     i=r.get('input') if isinstance(r.get('input'),dict) else {}
     projection['request']={'subtype':r.get('subtype'),'tool_name':r.get('tool_name'),'tool_use_id':r.get('tool_use_id'),'path':i.get('file_path')}
    log.write(json.dumps(projection)+'\\n');log.flush()
  except ValueError:pass
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
sys.exit(p.wait())
'''.replace('CAP', str(STDOUT_CAP)).replace('TRACE', repr(str(trace))).replace('NATIVE', repr(str(native))))
    path.chmod(0o700)


def bound_to(occurrence, results):
    """The tool_use_id an occurrence belongs to, or None: inside that result,
    or a later sibling block of the same message with no other tool_result
    between them. A line outside the messages, in a string content, or in a
    block before any result belongs to none, but for D-235's measured
    placement: its own system-role message right after the result's message
    (content a string or block 0's text), prefixed by the Write hook."""
    path = occurrence.get("path", [])
    if occurrence.get("role") == "system":
        prior = [x for x in results if x["message"] == occurrence.get("message", -2) - 1]
        if (path[:1] == ["messages"] and path[2:3] == ["content"] and path[3:] in ([], [0, "text"]) and len(prior) == 1 and
                occurrence["line"].startswith("PreToolUse:Write hook additional context: " + LEAD)):
            return prior[0]["tool_use_id"]
        return None
    if occurrence.get("message") is None or occurrence.get("block") is None:
        return None
    # Only where the model reads it, as the checker's binding() says (Codex
    # code pass F1): a tool_result's content or its text blocks, a text block's text.
    tail = occurrence.get("path", [])[4:]
    if not (tail == ["content"] or (len(tail) == 3 and tail[0] == "content" and isinstance(tail[1], int) and tail[2] == "text")
            if occurrence.get("block_type") == "tool_result" else occurrence.get("block_type") == "text" and tail == ["text"]):
        return None
    if occurrence.get("in_tool_result"):
        return occurrence.get("tool_use_id")
    before = [x for x in results if x["message"] == occurrence["message"] and x["block"] < occurrence["block"]]
    return max(before, key=lambda x: x["block"])["tool_use_id"] if before else None


def line_checks(requests, work, checks):
    """The runner's reading of the provider log; the checker re-derives it."""
    ids = ["toolu_buddy_%d" % n for n in range(len(CALLS))]
    checks["provider_request_shape"] = ([len(x["tool_outputs"]) for x in requests] == list(range(len(CALLS) + 1)) and
                                        all(x.get("tool_result_ids") == ids[:k] for k, x in enumerate(requests)))
    def named(occ, n):
        return LEAD in occ["line"] and ("covers %s (in %s" % (CALLS[n][0], work)) in occ["line"]
    def after(n):
        # The request right after tool result n is the one carrying n+1 results.
        rows = [o for o in requests[n + 1]["lead_occurrences"] if bound_to(o, requests[n + 1]["tool_results"]) == ids[n]]
        return bool(rows) and all(named(o, n) for o in rows)
    checks["line_after_uncovered_allowed"] = len(requests) > 3 and after(2)
    checks["line_after_uncovered_denied"] = len(requests) > 4 and after(3)
    checks["no_line_before_uncovered"] = all(not x["lead_occurrences"] for x in requests[:3])
    def bound(x):
        for o in x["lead_occurrences"]:
            tid = bound_to(o, x["tool_results"])
            if tid not in (ids[2], ids[3]) or tid not in x["tool_result_ids"] or not named(o, ids.index(tid)):
                return False
        return True
    checks["every_line_bound_to_an_uncovered_result"] = all(bound(x) for x in requests)


def hook_checks(trace, work, checks):
    """From the observer's trace: exactly one PreToolUse hook frame for each
    call, exit 0, after the previous call's result and before this call's
    can_use_tool (or, for the denied peer call, its result); and its stdout
    the expected document. Window bounds do not assume the assistant frame
    precedes the hook (unmeasured): only that calls are sequential."""
    rows = [json.loads(x) for x in trace.read_text().splitlines() if x.strip()]
    def result_at(n):
        return next(i for i, v in enumerate(rows) if v.get("type") == "user" and
                    any(b.get("type") == "tool_result" and b.get("id") == "toolu_buddy_%d" % n for b in v.get("tool_blocks") or []))
    asks = {v["request"].get("tool_use_id"): i for i, v in enumerate(rows) if v.get("type") == "control_request" and
            (v.get("request") or {}).get("subtype") == "can_use_tool"}
    hooks, ordered = [], True
    for n, (name, _, decision) in enumerate(CALLS):
        tid = "toolu_buddy_%d" % n
        start = result_at(n - 1) if n else 0
        end = asks.get(tid) if decision else result_at(n)
        if end is None or (decision is None and tid in asks):
            ordered = False
            hooks.append(None)
            continue
        frames = [v for v in rows[start:end] if v.get("type") == "system" and v.get("subtype") == "hook_response" and
                  v.get("hook_event") == "PreToolUse"]
        ordered = ordered and len(frames) == 1 and frames[0].get("exit_code") == 0 and frames[0].get("outcome", "success") == "success"
        hooks.append(frames[0] if len(frames) == 1 else None)
    checks["hook_succeeded_before_each_approval"] = ordered and sorted(asks) == ["toolu_buddy_1", "toolu_buddy_2", "toolu_buddy_3"]
    def doc(frame):
        return json.loads(frame["stdout"])["hookSpecificOutput"]
    def documents():
        if None in hooks:
            return False
        deny = doc(hooks[0])
        warn = [doc(hooks[n]) for n in (2, 3)]
        return (deny.get("permissionDecision") == "deny" and
                deny.get("permissionDecisionReason", "").startswith('peer.txt is inside scope "peer.txt"') and
                hooks[1]["stdout"].strip() == "" and
                all(set(w) == {"hookEventName", "additionalContext"} and w["hookEventName"] == "PreToolUse" and
                    w["additionalContext"].startswith(LEAD + " no claim of this session covers %s (in %s" % (CALLS[n][0], work))
                    for w, n in zip(warn, (2, 3))))
    try:
        checks["hook_documents_as_expected"] = documents()
    except (ValueError, KeyError, TypeError, AttributeError):
        checks["hook_documents_as_expected"] = False
    return [{k: v.get(k) for k in ("hook_event", "hook_name", "outcome", "exit_code", "stdout_chars")} if v else None
            for v in hooks]


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--claude", default=shutil.which("claude"))
    base.add_claude_version(parser)
    parser.add_argument("--buddy", type=pathlib.Path)
    parser.add_argument("--adapter", type=pathlib.Path)
    args = parser.parse_args()
    base.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-own-claim-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": SCHEMA, "root": str(root), "passed": False,
              "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "checks": {},
              "provider": "deterministic loopback Messages fixture; no real inference",
              "qualification": "own_claim warn through buddy-adapter, manual stream-json with stdio prompts (D-235)",
              "unqualified": UNQUALIFIED, "measured": {
                  "hook_outcome_source": "native trace from a metadata observer between native stdout and the adapter; "
                                         "the adapter's stdout surfaces no hook event"}}
    checks = result["checks"]
    server = None
    server_started = False
    adapter = None
    work = root / "work"
    trace = root / "native-observations.jsonl"
    try:
        if not args.claude:
            raise RuntimeError("claude is not on PATH; provide --claude")
        # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
        result["qualification_candidates"] = sys.modules["buddy_admission_lib"].bind_args(args)
        claude = pathlib.Path(args.claude)
        env = base.clean_env()
        state, bindir = root / "claude", root / "bin"
        for path in (work, state, bindir, root / "tmp"):
            path.mkdir()
        # An explicit inert key prevents account/keychain credentials being used
        # for inference. Native nonessential traffic and updating are disabled.
        native_env = dict(env, CLAUDE_CONFIG_DIR=str(state), TMPDIR=str(root / "tmp"),
                          ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
                          CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
                          DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1",
                          # The NEGATIVE control (Fable cross-check): unset is the
                          # gate's default, warn, so a hook that stopped appending
                          # the receipt's posture would still pass. Inherited off,
                          # every line can come only from the typed warn.
                          BUDDY_OWN_CLAIM="off")
        result["inherited_own_claim"] = native_env["BUDDY_OWN_CLAIM"]
        result["native_version"] = base.run([claude, "--version"], work, native_env).strip()
        if result["native_version"] != args.claude_version + " (Claude Code)":
            raise RuntimeError("installed Claude version does not match this qualification contract")
        result["native_sha256"] = base.digest(claude.resolve())
        result["source_head"] = base.run(["git", "--no-optional-locks", "rev-parse", "HEAD"], REPO, env).strip()
        result["source_status"] = base.run(["git", "--no-optional-locks", "status", "--porcelain"], REPO, env).splitlines()
        buddy = args.buddy.resolve() if args.buddy else bindir / "buddy"
        binary = args.adapter.resolve() if args.adapter else bindir / "buddy-adapter"
        manifest = base.source_manifest(env)
        base.write_json(root / "source-manifest.json", manifest)
        result["source_manifest_sha256"] = base.digest(root / "source-manifest.json")
        result["binary_origins"] = {"buddy": "provided artifact" if args.buddy else "built from captured source manifest",
                                    "adapter": "provided artifact" if args.adapter else "built from captured source manifest"}
        # go build stamps VCS state by running `git status` in the real
        # checkout, which rewrites its index (F-34): no optional locks.
        buildenv = dict(env, GOCACHE="/private/tmp/buddy-claude-go-cache", GOPROXY="off", GOSUMDB="off", GIT_OPTIONAL_LOCKS="0")
        for supplied, target, package in ((args.buddy, buddy, "./cmd/buddy"), (args.adapter, binary, "./cmd/buddy-adapter")):
            if not supplied:
                base.run(["go", "build", "-o", target, package], REPO, buildenv, timeout=120)
        if base.source_manifest(env) != manifest:
            raise RuntimeError("source changed while binaries were prepared; retry from a stable snapshot")
        result["buddy_sha256"], result["adapter_sha256"] = base.digest(buddy), base.digest(binary)
        result["go_version"] = base.run(["go", "version"], REPO, buildenv).strip()
        # The peer's claim ARMS the own-claim arm; its path is the holder-deny control.
        base.run(["git", "init", "-q", work], root, env)
        base.run([buddy, "init"], work, env)
        base.run([buddy, "hello", "--session", "fixture-holder"], work, env)
        base.run([buddy, "claim", "held", "--session", "fixture-holder", "--scope", "peer.txt",
                  "--desc", "isolated own-claim qualification peer"], work, env)
        (work / "peer.txt").write_text("original\n")
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), OwnClaimFixture)
        server.requests, server.unexpected = Recorded(), []
        server.sequence = [("Write", {"file_path": str(work / name), "content": body}) for name, body, _ in CALLS]
        native_env["ANTHROPIC_BASE_URL"] = f"http://127.0.0.1:{server.server_address[1]}"
        receipt = root / "worker.json"
        # main.go captures POSIX single quoting even for uncomplicated paths.
        gate = f"'{binary}' hook --receipt '{receipt}' --buddy '{buddy}'"
        settings = root / "settings.json"
        base.write_json(settings, {"enabledPlugins": {}, "hooks": {"PreToolUse": [{"matcher": "^(Edit|Write|NotebookEdit)$",
                        "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
        result["settings_sha256"] = base.digest(settings)
        launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(settings),
                  "harness_version": args.claude_version, "gate_command": gate, "own_claim": "warn",
                  "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown",
                            "mods": "builtin-only-" + args.claude_version}}
        base.write_json(root / "launch.json", launch)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        server_started = True
        observer = bindir / "claude-observer"
        hook_observer(observer, claude, trace)
        result["observer_sha256"] = base.digest(observer)
        adapter = base.Adapter([binary, "--harness", "claude", "--executable", observer, "--buddy", buddy,
                                "--launch", root / "launch.json", "--receipt", receipt, "--label", "claude-own-claim"],
                               work, native_env, root)
        start = adapter.wait(lambda x: x.get("operation") == "start")
        identity = start["identity"]
        checks["captured_native_identity"] = bool(identity["session_id"] and identity["incarnation"])
        checks["handshake_without_inference"] = len(server.requests) == 0
        saved = json.loads(receipt.read_text())
        checks["receipt_launch_own_claim_warn"] = saved["launch"].get("own_claim") == "warn"
        base.run([buddy, "claim", "worker-covered", "--session", identity["session_id"], "--scope", "covered.txt",
                  "--desc", "own-claim qualification: the covered control"], work, env)
        inspection = json.loads(base.run([buddy, "adapter", "inspect", "--session", identity["session_id"],
                                          "--incarnation", identity["incarnation"]], work, env))
        base.write_json(root / "ledger-inspection.json", inspection)
        checks["gate_honours_warn"] = "warn" in inspection["gate"]["own_claim"]
        sender = json.loads(base.run([buddy, "lifecycle", "begin", "--session", "fixture-coordinator"], work, env))
        queued = json.loads(base.run([buddy, "adapter", "queue", "--session", sender["session_id"], "--incarnation",
                                      sender["incarnation"], "--to", identity["session_id"], "--to-incarnation",
                                      identity["incarnation"], "--body", "Fixture assignment is peer data."], work, env))
        wake_request = {"schema": base.SCHEMA, "id": "wake-1", "operation": "send", "identity": identity,
                        "message_id": queued["delivery"]["message_id"]}
        adapter.send(wake_request)
        wake = adapter.wait(lambda x: x.get("request_id") == "wake-1")
        checks["wake_delivered"] = wake["wake_delivery"]["state"] == "delivered"
        approvals, seen = [], []
        for (name, _, decision), rid in zip(CALLS[1:], REPLIES):
            event = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "approval" and
                                 x["native_event"]["approval"]["id"] not in seen)
            approval = event["native_event"]["approval"]
            seen.append(approval["id"])
            if pathlib.Path(approval["detail"]["input"]["file_path"]).name != name:
                raise RuntimeError("unexpected approval tool or path: " + json.dumps(approval))
            approvals.append({"id": approval["id"], "item_id": approval.get("item_id"),
                              "path": approval["detail"]["input"]["file_path"], "decision": decision})
            checks[rid.replace("-", "_") + "_waiting"] = event["state"] == {"state": "observed", "detail": "waiting_for_approval"}
            adapter.send({"schema": base.SCHEMA, "id": rid, "operation": "approve", "identity": identity,
                          "approval": {"id": approval["id"], "native_session_id": approval["native_session_id"],
                                       "turn_id": approval["turn_id"], "decision": decision}}, operator=True)
            approved = adapter.wait(lambda x: x.get("request_id") == rid)
            checks[rid.replace("-", "_") + "_submitted"] = approved["approval_delivery"]["state"] == "submitted"
        result["approvals"] = approvals
        checks["approvals_bound_in_order"] = ([a["item_id"] for a in approvals] == ["toolu_buddy_1", "toolu_buddy_2", "toolu_buddy_3"] and
                                              [a["path"] for a in approvals] == [str(work / c[0]) for c in CALLS[1:]])
        native_result = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "result")
        idle = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "idle", timeout=10)
        checks["turn_completed"] = (native_result["native_event"].get("turn_id") == wake["wake_delivery"]["turn_id"] and
                                    idle["state"]["detail"] == "idle" and idle["pending_approval"]["state"] == "observed_absent")
        outputs = {x["tool_use_id"]: x for row in server.requests for x in row["tool_outputs"]}
        checks["peer_write_denied"] = ((work / "peer.txt").read_text() == "original\n" and
                                       outputs["toolu_buddy_0"].get("is_error") is True and
                                       'peer.txt is inside scope "peer.txt"' in str(outputs["toolu_buddy_0"].get("content")))
        checks["covered_written"] = (work / "covered.txt").read_text() == CALLS[1][1] and not outputs["toolu_buddy_1"].get("is_error")
        checks["uncovered_allowed_written"] = ((work / "uncovered-allowed.txt").read_text() == CALLS[2][1] and
                                               not outputs["toolu_buddy_2"].get("is_error"))
        checks["uncovered_denied_absent"] = (not (work / "uncovered-denied.txt").exists() and
                                             outputs["toolu_buddy_3"].get("is_error") is True and
                                             "Denied by the operator control interface" in str(outputs["toolu_buddy_3"].get("content")))
        result["provider_request_count"] = len(server.requests)  # the checker's tool_calls reads it (D-235)
        line_checks(server.requests, work, checks)
        result["measured"]["lead_locations"] = [
            [{k: o[k] for k in ("message", "role", "block", "block_type", "in_tool_result", "tool_use_id", "path")}
             for o in x["lead_occurrences"]] for x in server.requests]
        checks["fixture_only_expected_requests"] = not server.unexpected
    except BaseException as exc:
        result["failure"] = str(exc)
        (root / "failure.txt").write_text(traceback.format_exc())
    finally:
        if adapter:
            try:
                adapter.close()
            except BaseException as exc:
                result["cleanup_failure"] = str(exc)
            base.write_json(root / "adapter-events.json", adapter.events)
            base.write_json(root / "unparsed-output.json", adapter.unparsed)
            result["adapter_exit"] = adapter.process.returncode
            result["adapter_forced_cleanup"] = list(adapter.forced)
            checks["adapter_exited_successfully"] = adapter.process.returncode == 0 and not adapter.forced
            checks["adapter_stdout_fully_parsed"] = not adapter.unparsed
            result["cleanup_observation"] = base.observed_child_exit(trace)
            checks["observed_native_children_exited"] = result["cleanup_observation"]["status"] == "observed_absent"
            # Read once the adapter closed, from everything it wrote: a fourth
            # ask arriving after the loop above stopped waiting is counted.
            checks["exactly_three_approvals"] = len([v for v in adapter.events
                                                     if v.get("native_event", {}).get("kind") == "approval"]) == 3
            checks["hook_succeeded_before_each_approval"] = checks["hook_documents_as_expected"] = False
            try:
                result["measured"]["hook_frames"] = hook_checks(trace, work, checks)
            except (OSError, StopIteration, ValueError, KeyError, TypeError) as exc:
                result["hook_trace_failure"] = repr(exc)
                checks["hook_succeeded_before_each_approval"] = checks["hook_documents_as_expected"] = False
        if server:
            # server_close works even if failure happened before serve_forever.
            if server_started:
                server.shutdown()
            server.server_close()
            base.write_json(root / "provider-observations.json", list(server.requests))
        result["fixture_sha256"] = base.digest(__file__)
        result["helper_sha256"] = base.digest(REPO / "scripts/qualify-claude.py")
        # F-37: every file check-claude-own-claim.py reads, bound to this receipt.
        result["evidence_sha256"] = {name: base.digest(root / name) for name in EVIDENCE if (root / name).is_file()}
        result["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        result["passed"] = bool(checks and all(checks.values()) and not result.get("failure") and
                                not result.get("cleanup_failure"))
        base.write_json(root / "result.json", result)
        print(json.dumps({"passed": result["passed"], "checks": checks, "failure": result.get("failure")}, indent=2), flush=True)
        # The runner protocol qualify-release.py reads (receipt_from_log).
        print("RECEIPT=" + str(root / "result.json"), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
