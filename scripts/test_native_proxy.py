#!/usr/bin/env python3
"""Hermetic regression for native qualification proxies; no harness is launched.

Run with python3 scripts/test_native_proxy.py. A fake child exits while the
caller's input pipe stays open, reproducing the shutdown seam behind macOS's
"Python quit unexpectedly" reports. EOF/fragmented-input controls ensure that
stoppable input still forwards every complete and final unterminated frame.

Two proxies also INJECT fixture requests into native stdin from a second
thread (F-38): they are exercised with an injection concurrent with the input
relay, and with a native that stops reading while a large injection is
pending. The history proxy is built per fault mode (F-40): each mode's drops
are checked, and a proxy whose injection target is missing must refuse to
build, naming the unarmed injector.
"""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

sys.dont_write_bytecode = True
SCRIPTS = Path(__file__).resolve().parent
HISTORY = "qualify-claude-history.py"
HISTORY_MODES = ("completed", "lost_result", "lost_ack", "crash_active", "crash_approval")
# (script, history fault mode or None). Every proxy a runner can build.
PROXIES = tuple((name, None) for name in (
    "qualify-claude-resume.py", "qualify-claude-compaction.py",
    "qualify-claude-interrupted-compaction.py", "qualify-compaction.py",
    "qualify-interrupted-compaction.py", "qualify-uncertain.py",
)) + tuple((HISTORY, mode) for mode in HISTORY_MODES)
# Proxies with a second stdin writer: request file -> saved native reply.
INJECTORS = {
    "qualify-compaction.py": ("compact-request.json", "compact-request-taken.json", "compact-reply.json"),
    "qualify-interrupted-compaction.py": ("fixture-request.json", "fixture-compact-taken.json",
                                          "fixture-compact-reply.json"),
}
ECHO = ("import json,sys\nfor line in sys.stdin.buffer:\n"
        " row=json.loads(line)\n print(json.dumps(row),flush=True)\n")
_modules = {}


def load(name):
    if name not in _modules:
        spec = importlib.util.spec_from_file_location("proxy_under_test_" + str(len(_modules)), SCRIPTS / name)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _modules[name] = module
    return _modules[name]


def builder(name, mode):
    module = load(name)
    return module.observer if mode is None else module.observer(mode)


class Proxy:
    """One proxy over a fake native, in its own process group."""

    def __init__(self, test, root, name, mode, native_source):
        self.root = root
        self.native, proxy, self.trace = (root / p for p in ("native", "proxy", "wire.jsonl"))
        self.native.write_text("#!" + sys.executable + "\n" + native_source)
        self.native.chmod(0o700)
        builder(name, mode)(proxy, self.native, self.trace)
        self.p = subprocess.Popen([str(proxy)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, start_new_session=True)
        test.addCleanup(self.close)

    def wire(self, kind):
        return [v["frame"] for v in map(json.loads, self.trace.read_text().splitlines()) if v["type"] == kind]

    def close(self):
        if self.p.poll() is None:
            os.killpg(self.p.pid, signal.SIGKILL)
            self.p.wait(timeout=5)
        for stream in (self.p.stdin, self.p.stdout, self.p.stderr):
            stream.close()


class NativeProxyShutdown(unittest.TestCase):
    def exercise(self, name, mode, early):
        with tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
            x = Proxy(self, Path(tmp), name, mode, ECHO + (" sys.exit(7)\n" if early else ""))
            p = x.p
            if early:
                rows = [{"fixture": "child exits before caller closes input"}]
                p.stdin.write((json.dumps(rows[0]) + "\n").encode())
                p.stdin.flush()
                # Do not communicate() yet: it closes stdin and hides the bug.
                self.assertEqual(p.wait(timeout=5), 7)
                p.stdin.close()
                out, err = p.stdout.read(), p.stderr.read()
            else:
                rows = [{"fragmented": "x" * 70000}, {"last": "no newline"}]
                data = "\n".join(json.dumps(row) for row in rows).encode()
                p.stdin.write(data[:9])
                p.stdin.flush()
                out, err = p.communicate(data[9:], timeout=5)
                self.assertEqual(p.returncode, 0)
            self.assertEqual(err, b"")
            self.assertEqual([json.loads(line) for line in out.splitlines()], rows)
            for kind in ("adapter_frame", "native_frame"):
                self.assertEqual(x.wire(kind), rows)

    def test_child_exit_with_input_still_open(self):
        for name, mode in PROXIES:
            with self.subTest(proxy=name, mode=mode):
                self.exercise(name, mode, early=True)

    def test_fragmented_input_and_eof(self):
        for name, mode in PROXIES:
            with self.subTest(proxy=name, mode=mode):
                self.exercise(name, mode, early=False)

    def test_injection_concurrent_with_input_relay(self):
        # The injected request reaches the native between two halves of a
        # fragmented adapter frame; its reply is saved, never forwarded.
        for name, (request, taken, reply) in INJECTORS.items():
            with self.subTest(proxy=name), tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
                root = Path(tmp)
                row = {"id": "fixture-compact", "method": "thread/compact/start", "params": {"threadId": "t"}}
                (root / request).write_text(json.dumps(row))
                x = Proxy(self, root, name, None, ECHO)
                rows = [{"fragmented": "y" * 70000}, {"last": "after injection"}]
                data = "\n".join(json.dumps(v) for v in rows).encode()
                x.p.stdin.write(data[:9])
                x.p.stdin.flush()
                deadline = time.monotonic() + 5
                while not (root / taken).exists():
                    self.assertLess(time.monotonic(), deadline, "injection never taken")
                    time.sleep(.01)
                out, err = x.p.communicate(data[9:], timeout=5)
                self.assertEqual((x.p.returncode, err), (0, b""))
                self.assertEqual([json.loads(line) for line in out.splitlines()], rows)
                self.assertEqual(x.wire("adapter_frame"), rows)
                self.assertEqual(x.wire("fixture_frame"), [row])
                self.assertEqual(json.loads((root / reply).read_text()), row)

    def test_native_stops_reading_with_injection_pending(self):
        # F-38's seam: the injector is blocked writing a frame larger than the
        # pipe when the native exits without reading. Before the fix the
        # unjoined daemon either printed a BrokenPipeError traceback or was
        # abandoned holding p.stdin's lock while Python finalized.
        for name, (request, taken, _) in INJECTORS.items():
            with self.subTest(proxy=name), tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
                root = Path(tmp)
                row = {"id": "fixture-compact", "method": "thread/compact/start",
                       "params": {"threadId": "t", "pad": "z" * (1 << 20)}}
                (root / request).write_text(json.dumps(row))
                x = Proxy(self, root, name, None, "import os,time\nos.close(1)\ntime.sleep(.5)\nraise SystemExit(7)\n")
                self.assertEqual(x.p.wait(timeout=10), 7)
                x.p.stdin.close()
                self.assertEqual((x.p.stdout.read(), x.p.stderr.read()), (b"", b""))
                self.assertTrue((root / taken).exists(), "the injection was never attempted")

    def test_history_fault_modes_drop_exactly_their_frames(self):
        load(HISTORY)
        self.assertEqual(HISTORY_MODES, load(HISTORY).MODES, "a history mode this test does not build")
        rows = [{"type": "user", "isReplay": True, "uuid": "u1"}, {"type": "assistant", "n": 1},
                {"type": "result", "uuid": "r1"}, {"type": "system", "n": 2}]
        for mode in HISTORY_MODES:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
                x = Proxy(self, Path(tmp), HISTORY, mode, ECHO)
                out, err = x.p.communicate("".join(json.dumps(v) + "\n" for v in rows).encode(), timeout=5)
                self.assertEqual((x.p.returncode, err), (0, b""))
                dropped = {"lost_result": [rows[2]], "lost_ack": [rows[0], rows[2]]}.get(mode, [])
                self.assertEqual(x.wire("dropped_frame"), dropped)
                self.assertEqual(x.wire("native_frame"), rows)
                self.assertEqual([json.loads(line) for line in out.splitlines()], [v for v in rows if v not in dropped])
                marker = Path(str(x.trace) + ".terminal")
                self.assertEqual(marker.read_text() if marker.exists() else None, "r1" if dropped else None)

    def test_history_injector_refuses_unarmed(self):
        module = load(HISTORY)
        original = module.resume.observer
        for found, respell in ((0, lambda s: s.replace(module.RELAY, "sys.stdout.buffer.write(line)\n sys.stdout.buffer.flush()")),
                               (2, lambda s: s.replace(module.RELAY, module.RELAY + "\n " + module.RELAY))):
            def drifted(path, native, trace, respell=respell):
                original(path, native, trace)
                path.write_text(respell(path.read_text()))
            module.resume.observer = drifted
            try:
                with self.subTest(relay_lines=found), tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
                    with self.assertRaisesRegex(RuntimeError, "injector for mode 'lost_result' is UNARMED.*has %d" % found):
                        module.observer("lost_result")(Path(tmp) / "proxy", Path(tmp) / "native", Path(tmp) / "wire.jsonl")
            finally:
                module.resume.observer = original


class OwnedCleanup(unittest.TestCase):
    """F-35: TERM/HUP reach `finally`, and a fallback kill needs an exact identity."""

    def test_term_and_hup_run_finally(self):
        source = ("import importlib.util,sys,time\n"
                  "s=importlib.util.spec_from_file_location('c',sys.argv[1]);c=importlib.util.module_from_spec(s);s.loader.exec_module(c)\n"
                  "c.exit_on_signals()\n"
                  "try:\n print('ready',flush=True);time.sleep(30)\n"
                  "finally:\n open(sys.argv[2],'w').write('cleaned')\n")
        for signum in (signal.SIGTERM, signal.SIGHUP):
            for script in ("qualify-claude.py", "qualify-codex.py"):
                with self.subTest(signal=signum, module=script), tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
                    marker = Path(tmp) / "marker"
                    p = subprocess.Popen([sys.executable, "-I", "-B", "-c", source, str(SCRIPTS / script), str(marker)],
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                    self.assertEqual(p.stdout.readline(), b"ready\n")
                    p.send_signal(signum)
                    self.assertEqual(p.wait(timeout=10), 128 + signum)
                    p.stdout.close(); p.stderr.close()
                    self.assertEqual(marker.read_text(), "cleaned")

    def test_without_the_handler_finally_never_runs(self):
        # The control: the same runner-shaped child, handler not installed. The
        # signal's default action ends it and `finally` never runs, so the test
        # above observes the handler and not Python's ordinary shutdown.
        source = ("import time,sys\n"
                  "try:\n print('ready',flush=True);time.sleep(30)\n"
                  "finally:\n open(sys.argv[1],'w').write('cleaned')\n")
        for signum in (signal.SIGTERM, signal.SIGHUP):
            with self.subTest(signal=signum), tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
                marker = Path(tmp) / "marker"
                p = subprocess.Popen([sys.executable, "-I", "-B", "-c", source, str(marker)],
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                self.assertEqual(p.stdout.readline(), b"ready\n")
                p.send_signal(signum)
                self.assertEqual(p.wait(timeout=10), -signum)
                p.stdout.close(); p.stderr.close()
                self.assertFalse(marker.exists(), "cleanup ran with no handler installed")

    def spawn_adapter(self):
        # A stand-in adapter: its own session/group, one native child in it.
        p = subprocess.Popen([sys.executable, "-c", "import subprocess,sys,time\n"
                              "c=subprocess.Popen(['sleep','30']);print(c.pid,flush=True);time.sleep(30)\n"],
                             stdout=subprocess.PIPE, start_new_session=True)
        common = load("qualify-claude.py")
        leader = common.owned_process(p.pid)
        child = int(p.stdout.readline())
        mine = [common.owned_process(child), leader]
        def cleanup():
            # The test obeys the rule it tests: identities, not numbers.
            for identity in mine:
                try:
                    if common.still_owned(identity):
                        os.kill(identity["pid"], signal.SIGKILL)
                except ProcessLookupError:
                    pass
            p.wait(timeout=5)
            p.stdout.close()
        self.addCleanup(cleanup)
        return p, leader, child

    def spawn_separate_worker(self):
        # The adapter now uses Setpgid: the observer leads another group,
        # and the real native is its child in that group (F-35 verification).
        worker = ("import subprocess,time\n"
                  "c=subprocess.Popen(['sleep','30']);print(c.pid,flush=True);time.sleep(30)\n")
        source = ("import subprocess,sys,time\n"
                  "p=subprocess.Popen([sys.executable,'-c',sys.argv[1]],stdout=subprocess.PIPE,start_new_session=True)\n"
                  "print(p.pid,int(p.stdout.readline()),flush=True);time.sleep(30)\n")
        p = subprocess.Popen([sys.executable, "-c", source, worker],
                             stdout=subprocess.PIPE, start_new_session=True)
        common = load("qualify-claude.py")
        leader = common.owned_process(p.pid)
        proxy, child = map(int, p.stdout.readline().split())
        identities = [common.owned_process(child), common.owned_process(proxy), leader]

        def cleanup():
            for identity in identities:
                try:
                    if common.still_owned(identity):
                        os.kill(identity["pid"], signal.SIGKILL)
                except ProcessLookupError:
                    pass
            p.wait(timeout=5)
            p.stdout.close()

        self.addCleanup(cleanup)
        return p, leader, proxy, child

    def test_native_ownership_follows_ancestry_across_groups(self):
        common = load("qualify-claude.py")
        with tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
            trace = Path(tmp) / "wire.jsonl"
            p, leader, proxy, child = self.spawn_separate_worker()
            trace.write_text(json.dumps({"type": "buddy_fixture_native_process", "pid": child}) + "\n")
            owned = common.owned_natives(trace, [leader])
            self.assertEqual([v["pid"] for v in owned], [child])
            self.assertEqual(owned[0]["pgid"], proxy)
            self.assertNotEqual(proxy, leader["pgid"])
            stale = dict(leader, start=[leader["start"][0] - 1, leader["start"][1]])
            self.assertEqual(common.owned_natives(trace, [stale]), [])
            trace.write_text(json.dumps({"type": "buddy_fixture_native_process", "pid": os.getpid()}) + "\n")
            self.assertEqual(common.owned_natives(trace, [leader]), [])

    def test_deliberate_crash_ends_both_captured_groups(self):
        common = load("qualify-claude.py")
        with tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
            trace = Path(tmp) / "wire.jsonl"
            p, leader, proxy, child = self.spawn_separate_worker()
            trace.write_text(json.dumps({"type": "buddy_fixture_native_process", "pid": child}) + "\n")
            owned = common.owned_natives(trace, [leader])
            self.assertEqual(len(owned), 1, "new worker group was not captured")
            client = type("FixtureAdapter", (), {"process": p, "owned": leader})()
            groups = common.crash_owned(client, owned)
            self.assertEqual(groups, [leader["pgid"], proxy])
            self.assertEqual(p.returncode, -signal.SIGKILL)
            observed = common.observed_child_exit(trace)
            self.assertEqual(observed["status"], "observed_absent")

    def test_fallback_signals_only_the_exact_owned_identity(self):
        common = load("qualify-claude.py")
        with tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
            trace = Path(tmp) / "wire.jsonl"
            p, leader, child = self.spawn_adapter()
            trace.write_text(json.dumps({"type": "buddy_fixture_native_process", "pid": child}) + "\n")
            owned = common.owned_natives(trace, [leader])
            self.assertEqual([v["pid"] for v in owned], [child])
            self.assertEqual(owned[0]["pgid"], leader["pgid"])
            # Same pid NUMBER, different process: never signaled.
            stale = [dict(owned[0], start=[owned[0]["start"][0] - 1, owned[0]["start"][1]])]
            errors, forced = [], []
            common.end_owned(stale, errors, forced)
            self.assertEqual((errors, forced), ([], []))
            self.assertIsNotNone(common.process_start(child), "a stale identity was signaled")
            # The leader exits on its own: its group can no longer be proven
            # ours, so a NEW snapshot finds nothing to signal.
            os.kill(p.pid, signal.SIGKILL)
            p.wait(timeout=5)
            self.assertEqual(common.owned_natives(trace, [leader]), [])
            # Positive control: the identity read while the leader lived ends
            # the orphan, and the kill is recorded as a cleanup failure.
            common.end_owned(owned, errors, forced)
            self.assertEqual(forced, [leader["pgid"]])
            self.assertEqual(len(errors), 1)
            self.assertIn("fallback SIGKILL", errors[0])
            deadline = time.monotonic() + 5
            while common.process_start(child) is not None:
                # sleep is reparented to launchd, which reaps it.
                self.assertLess(time.monotonic(), deadline, "owned orphan survived its fallback kill")
                time.sleep(.02)


class ForcedCloseIsRecorded(unittest.TestCase):
    """F-35: the Mods and peers close helpers record a fallback signal.

    The native here handles TERM and exits 0, so its exit code alone would
    launder a forced close into a clean one. Each helper must record what it
    sent; the peers receipt carries it to check-claude-delivery, which refuses
    any forced signal. A stand-in native, never a harness.
    """
    STUBBORN = ("import signal,sys,time\n"
                "signal.signal(signal.SIGTERM, lambda *a: sys.exit(0))\n"
                "print('ready',flush=True)\n"
                "while True: time.sleep(1)\n")

    def stubborn(self):
        p = subprocess.Popen([sys.executable, "-I", "-B", "-c", self.STUBBORN], stdin=subprocess.PIPE,
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
        self.assertEqual(p.stdout.readline(), b"ready\n")
        def cleanup():
            if p.poll() is None:  # still unreaped: the group is ours
                os.killpg(p.pid, signal.SIGKILL)
                p.wait(timeout=5)
            p.stdout.close()
        self.addCleanup(cleanup)
        return p

    def idle_thread(self):
        import threading
        t = threading.Thread(target=lambda: None)
        t.start()
        return t

    def test_peers_close_records_forced_signal(self):
        peers = load("qualify-claude-peers.py")
        with tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
            native = object.__new__(peers.Native)
            native.closed, native.process, native.forced = False, self.stubborn(), []
            native.reader, native.err_reader = self.idle_thread(), self.idle_thread()
            native.folder, native.events, native.unparsed, native.stderr_bytes = Path(tmp), [], 0, 0
            native.close()
            cleanup = json.loads((Path(tmp) / "cleanup.json").read_text())
            self.assertEqual(native.process.returncode, 0, "control: the stand-in should exit 0 on TERM")
            self.assertEqual(cleanup["exit"], 0)
            self.assertEqual(cleanup["forced_signals"], ["SIGTERM"])

    def test_mods_close_records_forced_signal(self):
        mods = load("qualify-claude-mods.py")
        with tempfile.TemporaryDirectory(prefix="buddy-proxy-test-") as tmp:
            native = object.__new__(mods.Native)
            native.process, native.forced = self.stubborn(), []
            native.reader, native.stderr = self.idle_thread(), (Path(tmp) / "stderr").open("w")
            native.close()
            self.assertEqual(native.process.returncode, 0, "control: the stand-in should exit 0 on TERM")
            self.assertEqual(native.forced, ["SIGTERM"])


class JobProbeCleanup(unittest.TestCase):
    """A forced adapter exit is failed qualification, even with no live job.

    The probe's criteria are the D-077 narrow contract (2026-10-13): the
    escaped job is recorded in <receipt>.proc, named on one stderr close line
    as not signalled, and still alive after Close; only the pinned native group
    ends. The fake adapter here writes what a conforming adapter writes, and the
    negative cases remove one obligation each.
    """

    JOB = {"pid": 43, "pgid": 7, "start": [1, 2]}
    NATIVE = {"pid": 44, "pgid": 40, "start": [1, 1]}

    def exercise(self, forced, close_error=False, job_alive=True, proc=True, report=True):
        module = load("qualify-claude-jobs.py")
        with tempfile.TemporaryDirectory(prefix="buddy-job-probe-test-") as tmp:
            root = Path(tmp)
            identity = {"session_id": "fixture", "incarnation": "captured"}
            client = mock.Mock(owned={"pid": 42, "pgid": 42, "start": [1, 0]}, process=SimpleNamespace(returncode=0),
                               events=[], forced=[])
            client.wait.side_effect = [
                {"identity": identity}, {},
                {"native_event": {"approval": {"id": "request", "native_session_id": "fixture", "turn_id": "turn"}}}, {},
            ]
            job = dict(self.JOB, start=1000002)
            def close():
                client.forced[:] = forced
                if proc:
                    (root / "worker.json.proc").write_text(json.dumps({"leader": {"pid": 40, "pgid": 40},
                        "descendants": {"alive": [job]}, "close": {"trigger": "close", "captured": [job],
                        "ended": [], "survivors": [job], "unknown": [], "unattributable": "unknown"}}))
                if report:
                    (root / "adapter.stderr").write_text("buddy-adapter: worker closed (close): 1 escaped descendant(s) "
                        "captured, 0 ended, 1 still running (pid 43; not signalled: they left the worker's group), "
                        "0 unknown; descendants never observed: unknown\n")
                if close_error:
                    raise RuntimeError("fixture close failed after forced signal")
            client.close.side_effect = close
            def adapter(*args):
                (root / "work/job.json").write_text('{"pid": 43}')
                return client
            def run(argv, *args):
                if argv[-1] == "--version":
                    return "2.1.291 (Claude Code)"
                return json.dumps({**identity, "delivery": {"message_id": 1}})
            cleaned = []
            common = SimpleNamespace(QUALIFICATION_VERSIONS=("2.1.291",), SCHEMA="buddy.control.v1",
                MessagesFixture=object, exit_on_signals=lambda: None, clean_env=lambda: {}, run=run,
                digest=lambda path: "fixture-hash", native_observer=lambda *args: None,
                Adapter=adapter, owned_process=lambda pid: dict(self.JOB), descendant_of=lambda *args: True,
                owned_natives=lambda *args: [dict(self.NATIVE)],
                still_owned=lambda p: p["pid"] == 43 and job_alive and not cleaned,
                signal_owned_groups=lambda jobs: cleaned.append(jobs[0]["pgid"]) or [jobs[0]["pgid"]],
                process_start=lambda pid: None if cleaned or pid != 43 else [1, 2],
                write_json=module.common.write_json)
            server = mock.Mock(server_address=("127.0.0.1", 1), requests=[])
            args = SimpleNamespace(claude=Path("/fixture/claude"), buddy=Path("/fixture/buddy"),
                                   adapter=Path("/fixture/adapter"), claude_version="2.1.291")
            with contextlib.ExitStack() as stack:
                stack.enter_context(mock.patch.object(module, "common", common))
                stack.enter_context(mock.patch.object(module.argparse.ArgumentParser, "parse_args", return_value=args))
                stack.enter_context(mock.patch.object(module.tempfile, "mkdtemp", return_value=tmp))
                stack.enter_context(mock.patch.object(module.http.server, "ThreadingHTTPServer", return_value=server))
                stack.enter_context(mock.patch.object(module.threading, "Thread"))
                stack.enter_context(mock.patch.object(module.time, "sleep"))
                stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
                status = module.main()
            return status, json.loads((root / "result.json").read_text())

    def test_forced_close_is_not_success(self):
        status, result = self.exercise(["SIGTERM"])
        self.assertEqual(status, 1, "forced SIGTERM was laundered into successful qualification")
        self.assertFalse(result["passed"])
        self.assertEqual(result["adapter_forced_cleanup"], ["SIGTERM"])
        self.assertFalse(result["checks"]["adapter_exited_without_fallback"])

    def test_unforced_close_positive_control(self):
        status, result = self.exercise([])
        self.assertEqual(status, 0, json.dumps(result.get("checks")))
        self.assertTrue(result["passed"])
        self.assertEqual(result.get("adapter_forced_cleanup", []), [])
        self.assertEqual(result["fixture_cleanup_groups"], [7])

    def test_signalled_job_is_not_success(self):
        status, result = self.exercise([], job_alive=False)
        self.assertEqual(status, 1, "an adapter that signalled the escaped job passed the narrow contract")
        self.assertFalse(result["checks"]["job_not_signalled"])

    def test_missing_proc_record_is_not_success(self):
        status, result = self.exercise([], proc=False)
        self.assertEqual(status, 1, "a close with no .proc record passed")
        self.assertFalse(result["checks"]["escaped_job_recorded"])

    def test_missing_close_report_is_not_success(self):
        status, result = self.exercise([], report=False)
        self.assertEqual(status, 1, "a close with no stderr report passed")
        self.assertFalse(result["checks"]["close_report_names_job_not_signalled"])

    def test_close_failure_still_preserves_forced_receipt(self):
        status, result = self.exercise(["SIGTERM"], close_error=True)
        self.assertEqual(status, 1)
        self.assertFalse(result["passed"])
        self.assertEqual(result["adapter_forced_cleanup"], ["SIGTERM"])
        self.assertIn("fixture close failed", result["cleanup_error"])


if __name__ == "__main__":
    unittest.main()
