#!/usr/bin/env python3
"""Opt-in native mixed fleet, with deterministic loopback providers only.

Three adapter-owned sessions use linked worktrees and one disposable ledger.
The native tools execute fixed local fixture programs: this measures harness
integration, not model planning or real-provider readiness. No configuration,
trust, Git ref, ledger, or credential outside the fresh temporary root changes.
Every attempt retains a failure receipt and closes only its owned children.
"""

import argparse
import datetime
import importlib.util
import json
import os
import pathlib
import shlex
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import traceback
import uuid


REPO = pathlib.Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / "scripts" / filename)
    value = importlib.util.module_from_spec(spec)
    sys.modules[name] = value
    spec.loader.exec_module(value)
    return value


common = module("mixed_claude_fixture", "qualify-claude.py")
codex = module("mixed_codex_fixture", "qualify-codex.py")


def write_json(path, value):
    common.write_json(path, value)


def snapshot(database, path):
    # Called only after awaited idle events and closed CLI invocations, before
    # any next native input. Each Buddy invocation closes/checkpoints its DB.
    # The system sqlite read-only open failed for these fixture ledgers; require
    # the observed checkpointed state and inspect an independent immutable copy.
    # Never ignore a live WAL: a nonempty one makes the observation fail.
    wal = pathlib.Path(str(database) + "-wal")
    if wal.exists() and wal.stat().st_size:
        raise RuntimeError("ledger observation requires a checkpointed database; WAL is nonempty")
    before = database.stat()
    shutil.copyfile(database, path)
    after = database.stat()
    if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns) or (wal.exists() and wal.stat().st_size):
        path.unlink()
        raise RuntimeError("ledger changed during fixture observation")
    return str(path)


class Fleet:
    def __init__(self, root, result, args):
        self.root, self.result, self.args = root, result, args
        self.env = common.clean_env()
        self.env.update(GIT_AUTHOR_NAME="Buddy fixture", GIT_AUTHOR_EMAIL="fixture@invalid",
                        GIT_COMMITTER_NAME="Buddy fixture", GIT_COMMITTER_EMAIL="fixture@invalid")
        self.clients, self.providers, self.identities, self.worktrees = {}, {}, {}, {}
        self.receipts, self.starts = {}, {}
        self.turn_number = 0
        self.database = root / "repo" / ".git" / "buddy.db"
        self.buddy = root / "bin" / "buddy"
        self.binary = root / "bin" / "buddy-adapter"

    def run(self, argv, cwd=None):
        return common.run(argv, cwd or self.root / "repo", self.env, timeout=120)

    def buddy_call(self, role, *args):
        identity = self.identities[role]
        return json.loads(self.run([self.buddy, "adapter", *args, "--session", identity["session_id"],
                                   "--incarnation", identity["incarnation"]], self.worktrees[role]))

    def inspect(self, role):
        return self.buddy_call(role, "inspect")

    def queue(self, sender, recipient, body):
        target = self.identities[recipient]
        return self.buddy_call(sender, "queue", "--to", target["session_id"],
                               "--to-incarnation", target["incarnation"], "--body", body)

    def preflight(self):
        # PATH upgrades must refuse before builds, providers or native workers.
        # Retained executables are explicit inputs; never substitute a newer
        # version or turn a qualification option into adapter admission.
        versions = {}
        for harness, contract in (("codex", "0.160.0"), ("claude", self.args.claude_version)):
            native = pathlib.Path(getattr(self.args, harness)).resolve(strict=True)
            setattr(self.args, harness, str(native))
            env = dict(self.env, CODEX_HOME=str(self.root / "version-codex"),
                       CLAUDE_CONFIG_DIR=str(self.root / "version-claude"), DISABLE_AUTOUPDATER="1")
            observed = common.run([native, "--version"], self.root, env).strip()
            versions[harness] = {"version": observed.strip(), "executable_sha256": common.digest(native)}
            write_json(self.root / "native-versions.json", versions)
            expected = "codex-cli " + contract if harness == "codex" else contract + " (Claude Code)"
            if observed.strip() != expected:
                raise RuntimeError(harness + " native version differs from the qualification contract: " + observed.strip())
        self.result["native_versions"] = versions

    def prepare(self, roles=None, observer=None, provider_factory=None):
        from mixed_fleet_provider import ScriptedProvider
        (self.root / "bin").mkdir()
        manifest = common.source_manifest(self.env)
        write_json(self.root / "source-manifest.json", manifest)
        buildenv = dict(self.env, GOCACHE="/private/tmp/buddy-codex-go-cache", GOPROXY="off", GOSUMDB="off", GIT_OPTIONAL_LOCKS="0")
        for path, package in ((self.buddy, "./cmd/buddy"), (self.binary, "./cmd/buddy-adapter")):
            common.run(["go", "build", "-o", path, package], REPO, buildenv, timeout=120)
        if common.source_manifest(self.env) != manifest:
            raise RuntimeError("source changed during build; evidence cannot name a stable snapshot")
        self.result.update(source_head=self.run(["git", "--no-optional-locks", "rev-parse", "HEAD"], REPO).strip(),
            source_manifest_sha256=common.digest(self.root / "source-manifest.json"),
            buddy_sha256=common.digest(self.buddy), adapter_sha256=common.digest(self.binary))
        repository = self.root / "repo"
        self.run(["git", "init", "-q", "-b", "main", repository], self.root)
        self.run(["git", "config", "core.hooksPath", "/dev/null"])
        (repository / ".git" / "info" / "exclude").write_text(".fixture/\n")
        for name in ("claude", "codex"):
            (repository / (name + ".txt")).write_text("original\n")
        (repository / "verify.py").write_text(
            "import pathlib\nfor name in ('claude.txt','codex.txt'):\n"
            "    assert pathlib.Path(name).read_text() == 'fixed\\n', name + ' is not fixed'\n"
            "print('both worker changes validated')\n")
        self.run(["git", "add", "claude.txt", "codex.txt", "verify.py"])
        self.run(["git", "commit", "-qm", "isolated mixed fleet baseline"])
        self.run([self.buddy, "init"])
        for role, harness, branch in roles or (("coordinator", "codex", "integration"),
                                               ("claude_worker", "claude", "claude-work"),
                                               ("codex_worker", "codex", "codex-work")):
            work = self.root / role
            self.run(["git", "worktree", "add", "-q", "-b", branch, work])
            (work / ".fixture").mkdir()
            self.worktrees[role] = work
            state = self.root / (role + "-state")
            state.mkdir()
            native_state = state / "native"
            native_state.mkdir()
            (state / "tmp").mkdir()
            receipt = state / "identity.json"
            self.receipts[role] = receipt
            gate = "'" + str(self.binary) + "' hook --receipt '" + str(receipt) + "' --buddy '" + str(self.buddy) + "'"
            provider = (provider_factory or ScriptedProvider)(harness)
            provider.__enter__()
            self.providers[role] = provider
            native_env = dict(self.env, TMPDIR=str(state / "tmp"))
            if harness == "codex":
                native = pathlib.Path(self.args.codex).resolve()
                version = self.run([native, "--version"], work).strip()
                if version != "codex-cli 0.160.0":
                    raise RuntimeError("unsupported Codex native version: " + version)
                codex.CODEX = str(native)
                config = native_state / "config.toml"
                wrapper = state / "unused.py"
                base = codex.config_text(provider.port, wrapper).replace(
                    json.dumps(shlex.quote(codex.PYTHON) + " " + shlex.quote(str(wrapper))), json.dumps(gate))
                config.write_text(base)
                untrusted = codex.list_hooks(native_state, work, native_env)
                write_json(state / "hooks-untrusted.json", untrusted)
                hook = untrusted["data"][0]["hooks"][0]
                if hook["trustStatus"] != "untrusted":
                    raise RuntimeError("fresh fixture hook was unexpectedly trusted")
                config.write_text(base + "\n[hooks.state." + json.dumps(hook["key"]) + "]\ntrusted_hash = " +
                                  json.dumps(hook["currentHash"]) + "\n")
                trusted = codex.list_hooks(native_state, work, native_env)
                write_json(state / "hooks-trusted.json", trusted)
                hook = trusted["data"][0]["hooks"][0]
                if hook["trustStatus"] != "trusted":
                    raise RuntimeError("exact temporary gate hash was not trusted")
                launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(config),
                          "harness_version": "0.160.0", "gate_command": gate, "gate_hash": hook["currentHash"],
                          "hooks": {"configured": "observed", "trusted": "observed", "observed": "unknown"}}
            else:
                native = pathlib.Path(self.args.claude).resolve()
                native_env.update(CLAUDE_CONFIG_DIR=str(native_state),
                    ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
                    ANTHROPIC_BASE_URL=provider.base_url, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
                    DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1",
                    CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")
                contract = getattr(self.args, "claude_version", common.VERSION)
                if contract not in common.QUALIFICATION_VERSIONS:
                    raise RuntimeError("unsupported Claude qualification contract")
                version = common.run([native, "--version"], work, native_env).strip()
                if version != contract + " (Claude Code)":
                    raise RuntimeError("unsupported Claude native version: " + version)
                config = state / "settings.json"
                write_json(config, {"enabledPlugins": {}, "hooks": {"PreToolUse": [{
                    "matcher": "^(Edit|Write|NotebookEdit)$", "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
                launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(config),
                          "harness_version": contract, "gate_command": gate,
                          "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown",
                                    "mods": "builtin-only-" + contract}}
            write_json(state / "launch.json", launch)
            if "native_versions" in self.result and self.result["native_versions"][harness] != {
                    "version": version, "executable_sha256": common.digest(native)}:
                raise RuntimeError("native executable changed after version preflight")
            observer_path = state / "native-observer"
            (observer or common.native_observer)(observer_path, native, state / "native-observations.jsonl")
            client = common.Adapter([self.binary, "--harness", harness, "--executable", observer_path,
                "--buddy", self.buddy, "--launch", state / "launch.json", "--receipt", receipt, "--label", role],
                work, native_env, state)
            self.clients[role] = client
            start = client.wait(lambda x: x.get("operation") == "start")
            self.identities[role] = start["identity"]
            self.starts[role] = start
            self.result["sessions"][role] = {"harness": harness, "identity": start["identity"],
                "worktree": str(work), "native_version": version, "native_sha256": common.digest(native),
                "events": client.events}
            if start["capabilities"]["telemetry"]["support"] != "unknown":
                raise RuntimeError("unsupported native telemetry was not reported unknown")
        write_json(self.root / "identities.json", self.identities)

    def program(self, role, label, code):
        # Programs are fixed fixture code, never generated from inbox bodies.
        # Each runs under a real native tool and reads its launch's saved token.
        work = self.worktrees[role]
        path = work / ".fixture" / (label + ".py")
        prelude = f'''import json,os,pathlib,subprocess,sys
work=pathlib.Path({str(work)!r})
os.chdir(work)
buddy={str(self.buddy)!r}
identity=json.loads(pathlib.Path({str(self.receipts[role])!r}).read_text())["identity"]
identities=json.loads(pathlib.Path({str(self.root / 'identities.json')!r}).read_text())
def command(args,cwd=work,check=True):
    p=subprocess.run([str(x) for x in args],cwd=cwd,text=True,capture_output=True)
    if check and p.returncode:raise RuntimeError(str(args)+" failed: "+p.stderr)
    return p
def cli(*args):return command([buddy,*args,"--session",identity["session_id"]]).stdout
def adapter(*args):
    return json.loads(command([buddy,"adapter",*args,"--session",identity["session_id"],"--incarnation",identity["incarnation"]]).stdout)
def save(name,value):
    (work/".fixture"/name).write_text(json.dumps(value,indent=2)+"\\n")
def queue(role,body):
    target=identities[role]
    return adapter("queue","--to",target["session_id"],"--to-incarnation",target["incarnation"],"--body",body)
'''
        path.write_text(prelude + "\n" + code + "\n")
        return shlex.quote(sys.executable) + " " + shlex.quote(str(path))

    def shell(self, role, label, code, require_approval=False):
        from mixed_fleet_provider import shell
        return shell(self.program(role, label, code), str(self.worktrees[role]),
                     marker="native-" + label + "-" + uuid.uuid4().hex, timeout_ms=20000,
                     require_approval=require_approval)

    def fetch_step(self, role, label, queued):
        mid = queued["delivery"]["message_id"]
        marker = "fetched-" + str(mid) + "-" + uuid.uuid4().hex
        filename = label + "-fetch.json"
        code = f'''inbox=adapter("inbox","--message",{str(mid)!r})
assert inbox["messages"]["count"]==1
delivered=adapter("delivered","--message",{str(mid)!r})
save({filename!r},{{"inbox":inbox,"delivered":delivered,"marker":{marker!r}}})
print({marker!r})'''
        return self.shell(role, label + "-fetch", code), self.worktrees[role] / ".fixture" / filename

    def turn(self, role, label, queued, steps):
        print("TURN=" + role + ":" + label, flush=True)
        self.turn_number += 1
        client, provider = self.clients[role], self.providers[role]
        scripted = provider.queue_turn(label, steps)
        rid = str(self.turn_number) + "-" + label
        request = {"schema": "buddy.control.v1", "id": rid, "operation": "send",
                   "identity": self.identities[role], "message_id": queued["delivery"]["message_id"]}
        client.send(request)
        wake = client.wait(lambda x: x.get("request_id") == rid)
        if wake["wake_delivery"]["state"] != "delivered":
            raise RuntimeError("native wake not delivered")
        result = None
        while True:
            event = client.wait(lambda x: x.get("native_event", {}).get("kind") in ("approval", "result", "idle"), timeout=55)
            kind = event["native_event"]["kind"]
            if kind == "approval":
                approval = event["native_event"]["approval"]
                # Only exact known fixture tool requests may enter the separate
                # operator descriptor. Peer bodies and unknown calls never do.
                detail = approval.get("detail", {})
                if not self.expected_approval(role, detail, scripted):
                    raise RuntimeError("unexpected native permission request: " + json.dumps(approval))
                if event["state"] != {"state": "observed", "detail": "waiting_for_approval"}:
                    raise RuntimeError("approval was not exposed as waiting_for_approval")
                aid = rid + "-approval-" + uuid.uuid4().hex
                client.send({"schema": "buddy.control.v1", "id": aid, "operation": "approve",
                    "identity": self.identities[role], "approval": {"id": approval["id"],
                    "native_session_id": approval["native_session_id"], "turn_id": approval["turn_id"],
                    "decision": "allow"}}, operator=True)
                accepted = client.wait(lambda x: x.get("request_id") == aid)
                if accepted["approval_delivery"]["state"] != "submitted":
                    raise RuntimeError("fixture permission decision was not submitted")
            elif kind == "result":
                result = event
                if event["completion"]["state"] != "unknown":
                    raise RuntimeError("native result claimed work completion")
            else:
                if result is None:
                    # Separate event/receipt producers can reorder stdout; a
                    # missing result is still missing evidence, not success.
                    result = client.wait(lambda x: x.get("native_event", {}).get("kind") == "result")
                break
        scripted.assert_complete()
        output = json.dumps(scripted.results)
        write_json(self.root / (rid + "-provider.json"), {"label": label, "results": scripted.results})
        return wake, output

    def expected_approval(self, role, detail, scripted):
        # Filled from each provider's exact issued tool inputs. The profile
        # remains workspace-write; this grants only individual fixture calls.
        if detail.get("tool_name") == "Write":
            # The intentionally conflicting Write must be refused by the gate,
            # never rescued through the fixture's operator interface.
            if role != "claude_worker" or detail.get("input", {}).get("file_path") != str(self.worktrees[role] / "claude.txt"):
                return False
        return scripted.matches_approval(detail)

    def consume(self, sender, recipient, purpose, queued, extra_steps=(), label=None):
        label = label or purpose + "-" + recipient + "-" + str(queued["delivery"]["message_id"])
        before = self.inspect(recipient)
        fetch, artifact = self.fetch_step(recipient, label, queued)
        wake, output = self.turn(recipient, label, queued, [fetch, *extra_steps])
        fetched = json.loads(artifact.read_text())
        if fetched["marker"] not in output:
            raise RuntimeError("native fetch marker is absent from provider tool results")
        self.result["messages"].append({"purpose": purpose, "sender": sender, "recipient": recipient,
            "queue": queued, "idle_before": before, "inbox": fetched["inbox"], "delivered": fetched["delivered"],
            "wake": wake, "native_fetch": str(artifact), "native_output": output})
        return output

    def artifact(self, role, name):
        return json.loads((self.worktrees[role] / ".fixture" / name).read_text())

    def ready(self, role, run_slug):
        inspect = self.inspect(role)
        sha = inspect["ready_sha"]["value"]
        ledger = snapshot(self.database, self.root / (role + "-" + run_slug + "-ready.db"))
        with sqlite3.connect(pathlib.Path(ledger).as_uri() + "?mode=ro&immutable=1", uri=True) as db:
            claim_id = db.execute("SELECT claim_id FROM claims WHERE slug=? AND state='open'", (run_slug,)).fetchone()[0]
        self.result["ready"].append({"role": role, "sha": sha, "claim_id": claim_id, "inspect": inspect,
            "ledger": ledger})

    def close(self):
        errors = []
        for role, client in self.clients.items():
            try:
                client.close()
                self.result["sessions"][role]["adapter_exit"] = client.process.returncode
                self.result["sessions"][role]["unparsed"] = client.unparsed
                write_json(self.root / (role + "-events.json"), client.events)
                if client.process.returncode != 0 or client.unparsed:
                    errors.append(role + " adapter exit or unparsed stdout")
                # A fallback signal is the fixture ending what the adapter did
                # not: a cleanup failure, never a clean shutdown (F-35).
                if client.forced:
                    self.result["sessions"][role]["forced_cleanup"] = client.forced
                    errors.append(role + " adapter needed fallback " + "+".join(client.forced))
                children = common.observed_child_exit(self.root / (role + "-state") / "native-observations.jsonl")
                self.result["sessions"][role]["native_child_exit"] = children
                if children["status"] != "observed_absent":
                    errors.append(role + " native child exit unknown")
            except Exception as exc:
                errors.append(role + ": " + str(exc))
        for role, provider in self.providers.items():
            try:
                provider.__exit__(None, None, None)
                write_json(self.root / (role + "-provider.json"), {"requests": provider.requests, "errors": provider.errors})
                if provider.errors:
                    errors.append(role + " provider errors")
            except Exception as exc:
                errors.append(role + " provider cleanup: " + str(exc))
        self.result["cleanup_errors"] = errors


def exercise(fleet):
    from mixed_fleet_provider import patch, write
    # Each worker first takes a real native turn, so later observations prove
    # idle delivery rather than merely delivery after process initialization.
    for role in fleet.clients:
        if role == "coordinator":
            code = '''cli("claim","orchestrator","--scope",".buddy/orchestrator","--desc","isolated fixture coordinator")
cli("claim","run-red","--scope",".buddy/slot/run-red","--scope",".buddy/slot/main","--desc","both fixture workers")'''
        else:
            name = "claude" if role == "claude_worker" else "codex"
            code = f'''cli("claim",{role!r},"--scope",{name + '.txt'!r},"--desc","isolated fixture worker")'''
        queued = fleet.queue(role, role, "Local fixture bootstrap; no external actions.")
        fleet.consume(role, role, "bootstrap", queued, [fleet.shell(role, "bootstrap-" + role, code)])
    dispatch = '''save("assignment-claude.json",queue("claude_worker","Authorized local fixture: change only claude.txt; report READY on run-red to coordinator."))
save("assignment-codex.json",queue("codex_worker","Authorized local fixture: change only codex.txt; report READY on run-red to coordinator."))'''
    fleet.consume("coordinator", "coordinator", "dispatch", fleet.queue("coordinator", "coordinator", "Dispatch fixture assignments."),
                  [fleet.shell("coordinator", "dispatch", dispatch)])
    for role in ("claude_worker", "codex_worker"):
        name = "claude" if role == "claude_worker" else "codex"
        work = fleet.worktrees[role]
        edits = ([write(str(work / "codex.txt"), "forbidden\n"), write(str(work / "claude.txt"), "broken\n")]
                 if name == "claude" else [
                     patch("*** Begin Patch\n*** Update File: claude.txt\n@@\n-original\n+forbidden\n*** Add File: collateral.txt\n+forbidden\n*** End Patch"),
                     patch("*** Begin Patch\n*** Update File: codex.txt\n@@\n-original\n+fixed\n*** End Patch")])
        finish = f'''command(["git","add",{name + '.txt'!r}])
command(["git","commit","-qm",{name + ' fixture change'!r}])
sha=command(["git","rev-parse","HEAD"]).stdout.strip()
cli("wait","--on","run-red","--until","10m","--ready","HEAD")
save("report-red.json",queue("coordinator","READY {role} "+sha+" on run-red; native fixture checks pending shared validation."))'''
        queued = fleet.artifact("coordinator", "assignment-" + name + ".json")
        output = fleet.consume("coordinator", role, "assignment", queued,
                               [*edits, fleet.shell(role, "ready-red", finish, require_approval=name == "codex")])
        if (work / ("codex.txt" if name == "claude" else "claude.txt")).read_text() != "original\n":
            raise RuntimeError("cross-harness claim denial failed")
        if (work / "collateral.txt").exists() or "inside scope" not in output:
            raise RuntimeError("whole-patch or native claim-denial evidence missing")
        fleet.ready(role, "run-red")
        fleet.consume(role, "coordinator", "report", fleet.artifact(role, "report-red.json"))
    validation(fleet, "red", "fail")
    failed = fleet.result["validations"][-1]
    # The failed exact SHA is in the queued repair body, not merely in a log
    # the worker never received. Re-declaration attaches READY to the new run.
    for role in ("claude_worker", "codex_worker"):
        name = "claude" if role == "claude_worker" else "codex"
        edits = [write(str(fleet.worktrees[role] / "claude.txt"), "fixed\n")] if name == "claude" else []
        commit = 'command(["git","add","claude.txt"])\ncommand(["git","commit","-qm","repair failed fixture validation"])\n' if name == "claude" else ""
        finish = commit + f'''sha=command(["git","rev-parse","HEAD"]).stdout.strip()
cli("wait","--on","run-green","--until","10m","--ready","HEAD")
save("report-green.json",queue("coordinator","READY {role} "+sha+" on run-green after failed validation {failed['sha']}"))'''
        queued = fleet.artifact("coordinator", "retry-" + name + ".json")
        fleet.consume("coordinator", role, "repair" if name == "claude" else "reready", queued,
                      [*edits, fleet.shell(role, "ready-green", finish)])
        fleet.ready(role, "run-green")
        fleet.consume(role, "coordinator", "report", fleet.artifact(role, "report-green.json"))
    validation(fleet, "green", "pass")
    landed = fleet.artifact("coordinator", "landing.json")
    landed["ledger"] = snapshot(fleet.database, fleet.root / "landing.db")
    fleet.result["landing"] = landed
    for role in ("claude_worker", "codex_worker"):
        name = "claude" if role == "claude_worker" else "codex"
        fleet.consume("coordinator", role, "landed", fleet.artifact("coordinator", "landed-" + name + ".json"),
                      [fleet.shell(role, "close-worker", f'cli("release",{role!r})')])
    fleet.consume("coordinator", "coordinator", "closeout", fleet.queue("coordinator", "coordinator", "Close fixture coordination."),
                  [fleet.shell("coordinator", "close-coordinator", 'cli("release","orchestrator")')])
    fleet.result["ledger"] = snapshot(fleet.database, fleet.root / "final.db")


def validation(fleet, name, expected):
    phase = "run-" + name
    ledger = snapshot(fleet.database, fleet.root / (name + "-before-validation.db"))
    with sqlite3.connect(pathlib.Path(ledger).as_uri() + "?mode=ro&immutable=1", uri=True) as db:
        claim_id = db.execute("SELECT claim_id FROM claims WHERE slug=? AND state='open'", (phase,)).fetchone()[0]
    merges = '["claude-work","codex-work"]' if name == "red" else '["claude-work"]'
    code = f'''for branch in {merges}:command(["git","merge","--no-ff","--no-edit",branch])
sha=command(["git","rev-parse","HEAD"]).stdout.strip()
assert not command(["git","status","--porcelain"]).stdout
check=command([sys.executable,"verify.py"],check=False)
stdout=work/".fixture"/{name + '.stdout'!r};stdout.write_text(check.stdout)
stderr=work/".fixture"/{name + '.stderr'!r};stderr.write_text(check.stderr)
assert (check.returncode==0)=={expected == 'pass'!r}, "unexpected validation outcome"
assert command(["git","rev-parse","HEAD"]).stdout.strip()==sha
assert not command(["git","status","--porcelain"]).stdout
save({name + '-validation.json'!r},{{"outcome":{expected!r},"sha":sha,"exit_code":check.returncode,"claim_id":{claim_id!r},
    "command":[sys.executable,"verify.py"],"stdout":str(stdout),"stderr":str(stderr),"worktree":str(work)}})
'''
    if expected == "fail":
        code += '''cli("release","run-red","--outcome","fail","--note","failed exact commit "+sha)
cli("claim","run-green","--scope",".buddy/slot/run-green","--scope",".buddy/slot/main","--desc","repair and revalidate exact integration commit")
save("retry-claude.json",queue("claude_worker","Validation failed at "+sha+"; repair claude.txt then report READY on run-green."))
save("retry-codex.json",queue("codex_worker","Validation failed at "+sha+"; retain codex.txt and re-declare READY on run-green."))
'''
    else:
        code += f'''landing=pathlib.Path({str(fleet.root / 'repo')!r})
command(["git","merge","--ff-only","integration"],cwd=landing)
assert command(["git","rev-parse","HEAD"],cwd=landing).stdout.strip()==sha
cli("release","run-green","--outcome","pass","--note","landed exact validated commit "+sha)
save("landing.json",{{"sha":sha,"ref":"refs/heads/main","worktree":str(landing)}})
save("landed-claude.json",queue("claude_worker","LANDED "+sha+" after shared validation passed. Fixture assignment closed; no permission to end a session."))
save("landed-codex.json",queue("codex_worker","LANDED "+sha+" after shared validation passed. Fixture assignment closed; no permission to end a session."))
'''
    fleet.consume("coordinator", "coordinator", "validation", fleet.queue("coordinator", "coordinator", "Execute fixed local validation program " + name),
                  [fleet.shell("coordinator", "validate-" + name, code, require_approval=True)])
    value = fleet.artifact("coordinator", name + "-validation.json")
    value["ledger"] = snapshot(fleet.database, fleet.root / (name + "-validation.db"))
    fleet.result["validations"].append(value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default=shutil.which("codex"))
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default=common.VERSION,
                        help="exact native contract to exercise; does not widen adapter admission")
    args = parser.parse_args()
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-mixed-fleet-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.mixed-fleet.v1", "root": str(root), "provider": "deterministic-loopback",
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": False,
        "claude_version": args.claude_version, "codex_version": "0.160.0",
        "sessions": {}, "messages": [], "ready": [], "validations": [],
        "unqualified": ["real-provider inference and model-directed work", "production installation",
            "resume and compaction", "coordinator succession", "native teammates", "read-only diagnostics"]}
    fixture_sources = ["qualify-mixed-fleet.py", "mixed_fleet_provider.py", "check-mixed-fleet.py",
                       "qualify-claude.py", "qualify-codex.py"]
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in fixture_sources
                                 if (REPO / "scripts" / name).exists()}
    fleet = Fleet(root, result, args)
    try:
        if not args.codex or not args.claude:
            raise RuntimeError("both native harness binaries are required")
        fleet.preflight()
        fleet.prepare()
        exercise(fleet)
        if common.source_manifest(fleet.env) != json.loads((root / "source-manifest.json").read_text()):
            raise RuntimeError("source changed during native exercise")
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        fleet.close()
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        result["fixture_sha256"] = common.digest(__file__)
        helper = REPO / "scripts" / "mixed_fleet_provider.py"
        if helper.exists():
            result["provider_fixture_sha256"] = common.digest(helper)
        # Bind saved launch/version observations to this receipt. These hashes
        # identify artifacts, not a tamper-proof audit trail or live readiness.
        names = ["native-versions.json", "source-manifest.json"]
        for role in result["sessions"]:
            names.extend([role + "-state/launch.json", role + "-state/identity.json"])
        result["evidence_sha256"] = {name: common.digest(root / name) for name in names if (root / name).is_file()}
        if "error" not in result:
            try:
                validator = module("mixed_validator", "check-mixed-fleet.py")
                result["validation"] = validator.validate(result)
                result["passed"] = result["validation"]["passed"] and not result["cleanup_errors"]
            except Exception:
                result["error"] = traceback.format_exc()
        write_json(root / "result.json", result)
        print("RECEIPT=" + str(root / "result.json"), flush=True)
        print(json.dumps({"passed": result["passed"], "error": result.get("error"),
                          "validation": result.get("validation"), "cleanup_errors": result.get("cleanup_errors")}, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
