#!/usr/bin/env python3
"""Check captured endpoint/peer evidence without launching a harness or Buddy.

The fixture's passed/checks booleans are deliberately ignored. Recompute the
contract from native, provider and ledger projections, including exact notice
correlation: SendMessage's generic warning contains 'refuses' even on success.
Hashes bind saved observations, not a tamper-proof log or production readiness.
--self-test corrupts copies in memory, leaving native receipts unchanged.
"""

import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile


class Evidence:
    def __init__(self, receipt, overrides=None):
        self.receipt = receipt
        self.root = Path(receipt["root"]).resolve(strict=True)
        self.overrides = overrides or {}
        self.checks, self.errors = {}, []

    def path(self, name):
        path = (self.root / name).resolve(strict=True)
        if not path.is_relative_to(self.root) or path == self.root:
            raise ValueError("evidence path leaves artifact root")
        return path

    def read(self, name):
        data = self.path(name).read_bytes()
        if hashlib.sha256(data).hexdigest() != self.receipt["evidence_sha256"][name]:
            raise ValueError("changed evidence: " + name)
        # Overrides exercise semantic guards independently of the hash guard.
        if name in self.overrides:
            return copy.deepcopy(self.overrides[name])
        if name.endswith(".jsonl"):
            return [json.loads(line) for line in data.splitlines()]
        return json.loads(data)

    def check(self, name, predicate):
        try:
            if not predicate():
                raise ValueError("required evidence does not match")
            self.checks[name] = True
        except (KeyError, IndexError, StopIteration, TypeError, ValueError, OSError) as exc:
            self.checks[name] = False
            self.errors.append(name + ": " + str(exc))


def pair(identity):
    value = {key: identity[key] for key in ("session_id", "incarnation")}
    if not all(isinstance(v, str) and v for v in value.values()):
        raise ValueError("missing captured identity")
    return value


def queued(mail, identity, message_id):
    messages = mail["messages"]
    if mail["identity"] != pair(identity) or messages["count"] != 1 or len(messages["items"]) != 1:
        return False
    row = messages["items"][0]
    return (row["recipient"] == pair(identity) and row["message_id"] == message_id and
            row["state"] == "queued" and row["provenance"] == "peer" and
            row["acknowledged"]["status"] == row["completed"]["status"] == "unknown")


def endpoint_case(e, case, version):
    name = case["case"]
    def read(file): return e.read(name + "/" + file)
    def check(key, predicate): e.check(name + "_" + key, predicate)
    def launch_matches():
        launch = read("launch.json")
        return (launch["harness_version"] == version and launch["hooks"]["mods"] == "builtin-only-" + version and
                launch["permission_mode"] == "workspace-write")
    check("exact_launch_contract", launch_matches)

    def endpoint_matches():
        before, after = read("ledger-before.json"), read("ledger-inspection.json")
        endpoint, proc = case["endpoint"], case["native_process"]
        socket = read("socket-observation.json")
        wire = read("native-observations.jsonl")
        workers = [v["pid"] for v in wire if v.get("type") == "buddy_fixture_native_process" and not v["version_probe"]]
        names = [line[1:] for line in socket["stdout"].splitlines() if line.startswith("n")]
        if not (before["identity"] == after["identity"] == pair(case["identity"]) and
                before["endpoint"]["status"] == "unknown" and after["endpoint"] == endpoint and
                workers == [proc["pid"]] and socket["pid"] == endpoint["pid"] == proc["pid"] and
                0 < socket["born_before"] == socket["born_after"] == endpoint["born"] == proc["born"] ==
                case["native_process_birth_after_socket_observation"] and
                socket["exit"] in (0, 1) and not socket["stderr"] and names == case["native_unix_socket_names"]):
            return False
        if name == "absent":
            return endpoint["status"] == "observed_absent" and not endpoint.get("socket") and not names
        return (endpoint["status"] == "observed" and endpoint["socket"] in names and
                Path(endpoint["socket"]).is_relative_to(case["configured_native_tmpdir"]) and
                endpoint["socket"] != f'/tmp/cc-socks/{proc["pid"]}.sock' and
                any(v.get("messaging_socket_path") == endpoint["socket"] and
                    v.get("session_id") == case["identity"]["session_id"] for v in wire))
    check("receiving_endpoint_and_birth", endpoint_matches)

    def native_turn():
        events, wire = read("adapter-events.json"), read("native-observations.jsonl")
        wake = next(v for v in events if v.get("request_id") == "wake")
        turn = wake["wake_delivery"]["turn_id"]
        results = [v for v in events if v.get("native_event", {}).get("kind") == "result"]
        idles = [v for v in events if v.get("native_event", {}).get("kind") == "idle"]
        raw = [v for v in wire if v.get("type") == "result"]
        if len(results) != 1 or len(idles) != 1 or len(raw) != 1:
            return False
        result, idle, native = results[0], idles[0], raw[0]
        return (wake["wake_delivery"]["state"] == "delivered" and
                result["native_event"]["turn_id"] == idle["native_event"]["turn_id"] == native["user_message_uuid"] == turn and
                result["native_event"]["result_ref"] == native["uuid"] and native["subtype"] == "success" and
                native["session_id"] == case["identity"]["session_id"] and native["queued_turn_count"] == 0 and
                events.index(result) < events.index(idle) and
                all(v["identity"] == case["identity"] and v["completion"]["state"] == "unknown" for v in events))
    check("owned_stream_result_is_not_completion", native_turn)

    def gate():
        outputs = {v["tool_use_id"]: v for request in read("provider-observations.json") for v in request["tool_outputs"]}
        return (outputs["toolu_buddy_0"].get("is_error") is True and
                "inside scope" in str(outputs["toolu_buddy_0"]["content"]) and
                not outputs["toolu_buddy_1"].get("is_error") and
                e.path(name + "/work/guarded.txt").read_text() == "original\n" and
                e.path(name + "/work/allowed.txt").read_text() == "control\n")
    check("native_gate_denial_and_positive_control", gate)
    check("body_remains_queued", lambda: queued(read("inbox.json"), case["identity"], case["message_id"]))
    check("owned_child_cleanup", lambda: case["cleanup_observation"]["status"] == "observed_absent" and
          not case["cleanup_observation"]["remaining_or_unknown_pids"] and
          case["native_process"]["pid"] in case["cleanup_observation"]["observed_pids"] and
          not case.get("cleanup_failure"))
    def endpoint_cleanup():
        observed, endpoint = case["endpoint_cleanup"], case["endpoint"]
        expected = [endpoint["born"] // 1000000, endpoint["born"] % 1000000]
        return (observed["pid"] == endpoint["pid"] and observed["born"] == endpoint["born"] and
                observed["sampled_start"] != expected and observed["eligible_delivery"] is False and
                type(observed["socket_file_present"]) is bool)
    check("exact_receiver_ineligible_after_exit", endpoint_cleanup)


def peer_case(e, case):
    mode = case["mode"]
    def read(file): return e.read(mode + "/" + file)
    def check(key, predicate): e.check(mode + "_" + key, predicate)
    for role in ("sender", "receiver"):
        def owned(role=role):
            native = case[role]
            hook = read(role + "/hook.json")
            socket = read(role + "/socket-observation.json")
            settings = read(role + "/settings.json")
            return (read(role + "/endpoint-observation.json") == native and hook == native["hook"] and
                    hook["session_id"] == native["native_session_id"] and hook["event"] == "SessionStart" and
                    socket["pid"] == hook["parent_pid"] == native["pid"] and socket["exit"] == 0 and
                    not socket["stderr"] and "n" + hook["socket"] in socket["stdout"].splitlines() and
                    hook["socket"] == native["socket"] and
                    settings["crossSessionInbound"] == native["effective_crossSessionInbound"] ==
                    (mode if role == "receiver" else "accept"))
        check(role + "_owned_receiving_socket", owned)
        def completed(role=role):
            result = case[role + "_initial_result"]
            return (any(v.get("type") == "result" and all(v.get(k) == x for k, x in result.items())
                        for v in read(role + "/native-observations.json")) and result["subtype"] == "success" and
                    result["is_error"] is False and result["queued_turn_count"] == 0 and
                    result["session_id"] == case[role]["native_session_id"] and bool(result["user_message_uuid"]))
        check(role + "_initial_completed_input", completed)
        check(role + "_clean_exit", lambda role=role: read(role + "/cleanup.json")["exit"] == 0 and
              read(role + "/cleanup.json")["stdout_unparsed_lines"] == 0)

    def delivery():
        requests = [v for v in e.read("provider-observations.json") if v["route"] == mode + "-sender"]
        receiver = [v for v in e.read("provider-observations.json") if v["route"] == mode + "-receiver"]
        address = "uds:" + case["receiver"]["socket"]
        outputs = [v for request in requests for v in request["tool_results"]]
        receipts = [json.loads(block["text"]) for output in outputs for block in output["content"] if block["type"] == "text"]
        if not (outputs and outputs == case["send_tool_results"] and receipts and
                all(v["success"] is True and v["msg_id"] == case["native_message_id"] for v in receipts) and
                any(v.get("recipient") == address and "SendMessage" in v["tool_names"] for v in requests) and receiver):
            return False
        received = any(v["peer_marker_seen"] for v in receiver)
        if received != (mode == "accept") or case["marker_reached_receiver_provider_input"] != received:
            return False
        notices = [n for v in read("sender/native-observations.json") for n in v.get("delivery_notices", [])]
        notices += [n for v in requests for n in v["delivery_notices"]]
        if mode == "accept":
            return case["native_delivery_outcome"] == "receiver_provider_input_observed" and not any(v.get("is_error") for v in outputs)
        outcome = "held" if mode == "hold" else "refused"
        prefix = "[Cross-session delivery notice] Your message to another session was "
        prefix += "held by that session " if mode == "hold" else "refused "
        prefix += "(recipient: " + address + "; msg_id: " + case["native_message_id"] + ")"
        prefix += ". NOT delivered:" if mode == "hold" else ":"
        return (case["native_delivery_outcome"] == outcome and case["post_send_observation_seconds"] >= 3 and
                any(n.startswith(prefix) for n in notices) and sorted(set(notices)) == case["native_delivery_notices"])
    check("native_accept_hold_refuse_evidence", delivery)
    check("body_remains_queued", lambda: read("mail-before.json") == read("mail-after.json") and
          case["buddy_identity"]["session_id"] == case["receiver"]["native_session_id"] and
          queued(read("mail-after.json"), case["buddy_identity"], case["queued"]["delivery"]["message_id"]) and
          case["body_delivery"] == "not_marked_delivered" and case["work_completion"] == "unknown")


def validate(receipt, overrides=None):
    e = Evidence(receipt, overrides)
    version = receipt.get("claude_version")
    kind = receipt.get("schema")
    endpoint = kind == "buddy.claude-endpoint-qualification.v1"
    expected = ["custom", "absent"] if endpoint else ["accept", "hold", "refuse"]
    key = "case" if endpoint else "mode"
    e.check("schema_version_and_cases", lambda: kind in ("buddy.claude-endpoint-qualification.v1", "buddy.claude-peer-qualification.v1") and
            version in ("2.1.289", "2.1.290", "2.1.291") and
            receipt["native_version"] == e.read("native-version.json")["version"] == version + " (Claude Code)" and
            receipt["native_sha256"] == e.read("native-version.json")["executable_sha256"] and
            receipt["provider"] == "deterministic loopback Messages fixture; no real inference" and
            [c[key] for c in receipt["cases"]] == expected and not receipt.get("failure"))
    e.check("source_manifest_hash", lambda: hashlib.sha256(e.path("source-manifest.json").read_bytes()).hexdigest() ==
            receipt["source_manifest_sha256"])
    # A caller may supply previously built artifacts outside the receipt root.
    # Read their bytes only; no receipt-named executable is ever run here.
    e.check("binary_hashes", lambda: all(hashlib.sha256(Path(receipt["binary_paths"][name]).read_bytes()).hexdigest() ==
            receipt[name + "_sha256"] for name in (("buddy", "adapter") if endpoint else ("buddy",))))
    for case in receipt.get("cases", []):
        # Turn absent/malformed legs into a failed check, never a partial pass.
        e.check("case_" + str(case.get(key)), lambda case=case: not case.get("failure") and
                not case.get("cleanup_failures") and
                ((endpoint_case(e, case, version) if endpoint else peer_case(e, case)) is None))
    return {"passed": bool(e.checks) and all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def self_test(receipt):
    if not validate(receipt)["passed"]:
        raise AssertionError("positive receipt failed")
    # A disabled fixture summary must not disable the independent checker.
    summary = copy.deepcopy(receipt)
    summary["passed"], summary["checks"] = False, {}
    for case in summary["cases"]:
        case["passed"], case["checks"] = False, {}
    if not validate(summary)["passed"]:
        raise AssertionError("checker trusted fixture summary")
    mutations = []
    def mutate(path, value, file=None): mutations.append((path, value, file))
    for path, value in ((["claude_version"], "2.1.292"), (["claude_version"], "2.1.289" if receipt["claude_version"] != "2.1.289" else "2.1.291"),
                        (["native_version"], "wrong"), (["native_sha256"], "wrong"), (["buddy_sha256"], "wrong"),
                        (["source_manifest_sha256"], "wrong"),
                        (["evidence_sha256"], {}), (["cases"], [])):
        mutate(path, value)
    endpoint = receipt["schema"] == "buddy.claude-endpoint-qualification.v1"
    for i, case in enumerate(receipt["cases"]):
        if endpoint:
            folder = case["case"] + "/"
            mutate(["cases", i, "identity", "incarnation"], "wrong")
            mutate(["cases", i, "endpoint", "status"], "unknown")
            mutate(["cases", i, "endpoint", "born"], 1)
            mutate(["cases", i, "cleanup_observation", "status"], "unknown")
            mutate(["cases", i, "endpoint_cleanup", "eligible_delivery"], True)
            mutate(["cases", i, "endpoint_cleanup", "sampled_start"],
                   [case["endpoint"]["born"] // 1000000, case["endpoint"]["born"] % 1000000])
            mutate(["hooks", "mods"], "builtin-only-2.1.289" if receipt["claude_version"] != "2.1.289" else "builtin-only-2.1.291", folder + "launch.json")
            mutate(["born_after"], 1, folder + "socket-observation.json")
            mutate(["messages", "items", 0, "state"], "delivered", folder + "inbox.json")
            mutate([], [], folder + "provider-observations.json")
            mutate([], [], folder + "native-observations.jsonl")
            mutate([], [], folder + "adapter-events.json")
        else:
            folder = case["mode"] + "/"
            mutate(["cases", i, "native_message_id"], "wrong")
            mutate(["cases", i, "marker_reached_receiver_provider_input"], case["mode"] != "accept")
            mutate(["cases", i, "receiver", "socket"], "/wrong.sock")
            mutate(["messages", "items", 0, "completed", "status"], "observed", folder + "mail-after.json")
            for role in ("sender", "receiver"):
                mutate(["crossSessionInbound"], "wrong", folder + role + "/settings.json")
                mutate(["exit"], 1, folder + role + "/cleanup.json")
                mutate(["session_id"], "wrong", folder + role + "/hook.json")
                mutate([], [], folder + role + "/native-observations.json")
            if case["mode"] != "accept":
                mutate(["cases", i, "post_send_observation_seconds"], 0)
                mutate(["cases", i, "native_delivery_notices"], ["generic warning about refuses"])
    for path, value, file in mutations:
        bad, overrides = copy.deepcopy(receipt), {}
        target = Evidence(receipt).read(file) if file else bad
        if not path:
            target = value
        else:
            cursor = target
            for key in path[:-1]: cursor = cursor[key]
            cursor[path[-1]] = value
        if file: overrides[file] = target
        if validate(bad, overrides)["passed"]:
            raise AssertionError("accepted damaged evidence: " + str((file, path)))
    rejected = len(mutations)
    if not endpoint:
        # Corrupt both original notice sources and the matching summary. This
        # defeats mere cross-copy equality and exercises the exact prefix and
        # recipient/message guards against the generic transport warning.
        for i, case in enumerate(receipt["cases"]):
            if case["mode"] == "accept":
                continue
            notice = case["native_delivery_notices"][0]
            replacements = ["in that session's inbox, not yet read; a notice follows if it holds or refuses it",
                notice.replace("uds:" + case["receiver"]["socket"], "uds:/wrong.sock"),
                notice.replace(case["native_message_id"], "wrong-message")]
            for replacement in replacements:
                bad, overrides = copy.deepcopy(receipt), {}
                bad["cases"][i]["native_delivery_notices"] = [replacement]
                for file in (case["mode"] + "/sender/native-observations.json", "provider-observations.json"):
                    rows = Evidence(receipt).read(file)
                    for row in rows:
                        if "delivery_notices" in row:
                            row["delivery_notices"] = [replacement if n == notice else n for n in row["delivery_notices"]]
                    overrides[file] = rows
                if validate(bad, overrides)["passed"]:
                    raise AssertionError("accepted unrelated native notice: " + replacement)
                rejected += 1
    return {"positive": "passed", "summary_flags_ignored": True, "rejected_mutations": rejected}


def _base():
    # Loaded only for synthetic evidence: the checker itself stays standalone.
    spec = importlib.util.spec_from_file_location("delivery_synthetic", Path(__file__).with_name("check-mixed-fleet.py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def synthetic(root, kind):
    """A coherent SYNTHETIC endpoint or peer capture under root (check-mixed-fleet.Synthetic says what that is)."""
    base = _base()
    s = base.Synthetic(root)
    version = "2.1.291"
    s.write("native-version.json", {"version": version + " (Claude Code)", "executable_sha256": "a" * 64})
    s.write("source-manifest.json", "{}\n")
    s.write("bin/buddy", "synthetic buddy\n")
    s.write("bin/buddy-adapter", "synthetic adapter\n")
    r = {"root": str(s.root), "claude_version": version, "native_version": version + " (Claude Code)", "native_sha256": "a" * 64,
         "provider": "deterministic loopback Messages fixture; no real inference", "cases": [],
         "source_manifest_sha256": s.digest("source-manifest.json"),
         "binary_paths": {"buddy": str(s.root / "bin/buddy"), "adapter": str(s.root / "bin/buddy-adapter")},
         "buddy_sha256": s.digest("bin/buddy"), "adapter_sha256": s.digest("bin/buddy-adapter")}
    names = ["native-version.json"]
    if kind == "endpoint":
        r["schema"] = "buddy.claude-endpoint-qualification.v1"
        for index, name in enumerate(("custom", "absent")):
            ident = {"session_id": "sid-" + name, "incarnation": "inc-" + name}
            pid, born, mid = 500 + index, 1760000000123456 + index, 30 + index
            tmpdir = str(s.root / name / "tmp")
            socket = tmpdir + "/claude-%d.sock" % pid
            endpoint = ({"status": "observed", "pid": pid, "born": born, "socket": socket} if name == "custom" else
                        {"status": "observed_absent", "pid": pid, "born": born})
            sockets = [socket] if name == "custom" else []
            wire = [{"type": "buddy_fixture_native_process", "pid": 499, "version_probe": True},
                    {"type": "buddy_fixture_native_process", "pid": pid, "version_probe": False},
                    {"type": "result", "uuid": "res-" + name, "user_message_uuid": "turn-" + name, "subtype": "success",
                     "session_id": ident["session_id"], "queued_turn_count": 0}]
            if name == "custom":
                wire.append({"type": "system", "messaging_socket_path": socket, "session_id": ident["session_id"]})
            control = lambda op, ident=ident, **f: dict({"identity": ident, "operation": op, "completion": {"state": "unknown"}}, **f)
            events = [control("send", request_id="wake", wake_delivery={"state": "delivered", "turn_id": "turn-" + name}),
                      control("event", native_event={"kind": "result", "turn_id": "turn-" + name, "result_ref": "res-" + name}),
                      control("event", native_event={"kind": "idle", "turn_id": "turn-" + name})]
            files = {"launch.json": {"harness_version": version, "hooks": {"mods": "builtin-only-" + version}, "permission_mode": "workspace-write"},
                     "ledger-before.json": {"identity": ident, "endpoint": {"status": "unknown"}},
                     "ledger-inspection.json": {"identity": ident, "endpoint": endpoint},
                     "socket-observation.json": {"pid": pid, "born_before": born, "born_after": born, "exit": 0 if sockets else 1,
                                                 "stderr": "", "stdout": "".join("n" + v + "\n" for v in sockets)},
                     "adapter-events.json": events,
                     "provider-observations.json": [{"tool_outputs": [
                         {"tool_use_id": "toolu_buddy_0", "is_error": True, "content": "guarded.txt is inside scope run-endpoint"},
                         {"tool_use_id": "toolu_buddy_1", "is_error": False, "content": "ok"}]}],
                     "inbox.json": {"identity": ident, "messages": {"count": 1, "items": [{
                         "recipient": ident, "message_id": mid, "state": "queued", "provenance": "peer",
                         "acknowledged": {"status": "unknown"}, "completed": {"status": "unknown"}}]}}}
            for file, value in files.items():
                names.append(s.write(name + "/" + file, value))
            names.append(s.jsonl(name + "/native-observations.jsonl", wire))
            s.write(name + "/work/guarded.txt", "original\n")
            s.write(name + "/work/allowed.txt", "control\n")
            r["cases"].append({"case": name, "identity": ident, "endpoint": endpoint, "native_process": {"pid": pid, "born": born},
                               "native_process_birth_after_socket_observation": born, "native_unix_socket_names": sockets,
                               "configured_native_tmpdir": tmpdir, "message_id": mid,
                               "cleanup_observation": {"status": "observed_absent", "remaining_or_unknown_pids": [], "observed_pids": [pid]},
                               "endpoint_cleanup": {"pid": pid, "born": born, "sampled_start": [0, 0], "eligible_delivery": False,
                                                    "socket_file_present": False}})
        return s.seal(r, names)
    r["schema"] = "buddy.claude-peer-qualification.v1"
    requests = []
    for mode in ("accept", "hold", "refuse"):
        native = {}
        for role in ("sender", "receiver"):
            sid, pid = "sid-%s-%s" % (mode, role), 600 + len(native) + 10 * len(requests)
            sock = "/private/tmp/peer-%s-%s.sock" % (mode, role)
            hook = {"session_id": sid, "event": "SessionStart", "parent_pid": pid, "socket": sock}
            native[role] = {"native_session_id": sid, "pid": pid, "socket": sock, "hook": hook,
                            "effective_crossSessionInbound": mode if role == "receiver" else "accept"}
            result = {"type": "result", "subtype": "success", "is_error": False, "queued_turn_count": 0,
                      "session_id": sid, "user_message_uuid": "turn-%s-%s" % (mode, role)}
            native[role + "_initial_result"] = result
            for file, value in (("hook.json", hook), ("endpoint-observation.json", native[role]),
                                ("socket-observation.json", {"pid": pid, "exit": 0, "stderr": "", "stdout": "n" + sock + "\n"}),
                                ("settings.json", {"crossSessionInbound": native[role]["effective_crossSessionInbound"]}),
                                ("cleanup.json", {"exit": 0, "stdout_unparsed_lines": 0})):
                names.append(s.write("%s/%s/%s" % (mode, role, file), value))
        address = "uds:" + native["receiver"]["socket"]
        msg = "msg-" + mode
        receipt = {"type": "text", "text": json.dumps({"success": True, "msg_id": msg})}
        tool_results = [{"content": [receipt], "is_error": False}]
        notice = []
        if mode != "accept":
            notice = ["[Cross-session delivery notice] Your message to another session was " +
                      ("held by that session " if mode == "hold" else "refused ") + "(recipient: " + address + "; msg_id: " + msg + ")" +
                      (". NOT delivered: the session holds peer messages." if mode == "hold" else ": the session refuses peer messages.")]
        requests += [{"route": mode + "-sender", "recipient": address, "tool_names": ["SendMessage"], "tool_results": tool_results,
                      "delivery_notices": notice},
                     {"route": mode + "-receiver", "tool_results": [], "delivery_notices": [], "peer_marker_seen": mode == "accept"}]
        names.append(s.write(mode + "/sender/native-observations.json", [native["sender_initial_result"], {"delivery_notices": notice}]))
        names.append(s.write(mode + "/receiver/native-observations.json", [native["receiver_initial_result"]]))
        buddy = {"session_id": native["receiver"]["native_session_id"], "incarnation": "inc-" + mode}
        mail = {"identity": buddy, "messages": {"count": 1, "items": [{
            "recipient": buddy, "message_id": 40, "state": "queued", "provenance": "peer",
            "acknowledged": {"status": "unknown"}, "completed": {"status": "unknown"}}]}}
        names.append(s.write(mode + "/mail-before.json", mail))
        names.append(s.write(mode + "/mail-after.json", mail))
        r["cases"].append(dict(native, mode=mode, send_tool_results=tool_results, native_message_id=msg,
                               marker_reached_receiver_provider_input=mode == "accept",
                               native_delivery_outcome={"accept": "receiver_provider_input_observed", "hold": "held", "refuse": "refused"}[mode],
                               native_delivery_notices=sorted(set(notice)), post_send_observation_seconds=3,
                               buddy_identity=buddy, queued={"delivery": {"message_id": 40}},
                               body_delivery="not_marked_delivered", work_completion="unknown"))
    names.append(s.write("provider-observations.json", requests))
    return s.seal(r, names)


def synthetic_self_test():
    """Both receipt kinds' mutations and notice forgeries, against synthetic evidence."""
    out = {"positive": "passed", "summary_flags_ignored": True, "rejected_mutations": 0,
           "evidence": "synthetic: tests the checker, qualifies no native harness"}
    with tempfile.TemporaryDirectory(prefix="buddy-delivery-synthetic-") as tmp:
        for kind in ("endpoint", "peer"):
            r = synthetic(Path(tmp) / kind, kind)
            out["rejected_mutations"] += self_test(r)["rejected_mutations"]
            # Saved bytes are bound: one changed byte in any hashed file fails.
            for name in sorted(r["evidence_sha256"]):
                path = Path(r["root"]) / name
                original = path.read_bytes()
                path.write_bytes(original + b"\n")
                try:
                    if validate(r)["passed"]:
                        raise AssertionError("accepted edited evidence: " + name)
                finally:
                    path.write_bytes(original)
                out["rejected_mutations"] += 1
            if not validate(r)["passed"]:
                raise AssertionError("synthetic control did not restore")
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("receipt", type=Path, nargs="?")
    parser.add_argument("--self-test", action="store_true",
                        help="with no receipt, run against synthetic evidence built in a temp dir (hermetic)")
    args = parser.parse_args()
    if args.receipt is None:
        if not args.self_test:
            parser.error("a receipt is required unless --self-test")
        result = synthetic_self_test()
        print(json.dumps(result, indent=2))
        return 0
    try:
        receipt = json.loads(args.receipt.read_text())
        result = self_test(receipt) if args.self_test else validate(receipt)
    except (KeyError, TypeError, ValueError, OSError, AssertionError) as exc:
        result = {"passed": False, "error": str(exc)}
    print(json.dumps(result, indent=2))
    return 0 if result.get("passed") or result.get("positive") == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
