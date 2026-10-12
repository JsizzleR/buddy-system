package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// A native Codex thread must have one identity in shell commands and hook
// payloads. Tests inject the observed environment contract, never inherit the
// developer's real thread. No test launches a harness or assumes process = thread.
func TestCodexIdentityAcrossCommands(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.env["CODEX_THREAD_ID"] = "sess-a"
	if _, errw, code := f.run(t, f.repo, "", "claim", "native-work", "--desc", "native", "--scope", "pkg/native"); code != 0 {
		t.Fatalf("native claim: %d %s", code, errw)
	}
	if got := f.ownerOf(t, "native-work"); got != "alpha" {
		t.Fatalf("native claim owner %q, want alpha", got)
	}
	if out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--dry-run", "hello"); code != 0 || !strings.Contains(out, "as alpha") {
		t.Fatalf("native message sender: %d %q %s", code, out, errw)
	}
	f.stage(t, f.repo, "pkg/native/file.go")
	if _, errw, code := f.run(t, f.repo, "", "commit-gate", "--deny"); code != 0 || errw != "" {
		t.Fatalf("native own commit must be silent: %d %s", code, errw)
	}
	f.env["CODEX_THREAD_ID"] = "sess-b"
	if _, errw, code := f.run(t, f.repo, "", "commit-gate", "--deny"); code != commitGateDenied {
		t.Fatalf("native peer commit must deny: %d %s", code, errw)
	}
}

func TestCodexIdentityDoesNotFallBack(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	f.env["CODEX_THREAD_ID"] = "unknown-thread"
	if _, errw, code := f.run(t, f.repo, "", "claim", "wrong", "--desc", "d", "--scope", "pkg"); code == 0 || !strings.Contains(errw, "CODEX_THREAD_ID") {
		t.Fatalf("unknown native identity must not infer alpha: %d %s", code, errw)
	}
	f.env["CODEX_THREAD_ID"] = "sess-a"
	if _, errw, code := f.run(t, f.repo, "", "claim", "right", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatalf("live native control: %d %s", code, errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "ended", "--desc", "d", "--scope", "other"); code == 0 || !strings.Contains(errw, "has ended") {
		t.Fatalf("ended native identity must not revive: %d %s", code, errw)
	}
}

func TestCodexNativeIdentityAmbiguity(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.env["CODEX_THREAD_ID"], f.env[EnvClaudeSession] = "sess-a", "sess-b"
	if _, errw, code := f.run(t, f.repo, "", "claim", "ambiguous", "--desc", "d", "--scope", "pkg"); code == 0 || !strings.Contains(errw, "disagree") {
		t.Fatalf("conflicting native identities must refuse: %d %s", code, errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "hello"); code == 0 || !strings.Contains(errw, "disagree") {
		t.Fatalf("ambiguous sender must refuse before sending: %d %s", code, errw)
	}
	// Ordinary assertions keep their established precedence; strict checks do not.
	f.env[EnvSession] = "sess-a"
	if _, errw, code := f.run(t, f.repo, "", "claim", "explicit", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatalf("explicit ordinary identity control: %d %s", code, errw)
	}
	if got := f.ownerOf(t, "explicit"); got != "alpha" {
		t.Fatalf("override owner %q, want alpha", got)
	}
}

func TestCodexWaitOwnThread(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	w.env["CODEX_THREAD_ID"] = "sess-a"
	if out, errw, code := w.run(t, w.repo, "", "wait", "check"); code != 0 || !strings.HasPrefix(out, "STILL WAITING") || strings.Contains(out, "/loop") || strings.Contains(out, "next check in") {
		t.Fatalf("native own check without Claude scheduler assumptions: %d %q %s", code, out, errw)
	}
	w.env[EnvSession] = "sess-b"
	if _, errw, code := w.run(t, w.repo, "", "wait", "check"); code == 0 || !strings.Contains(errw, "harness's own session") {
		t.Fatalf("override cannot consume another wait: %d %s", code, errw)
	}
	delete(w.env, "CODEX_THREAD_ID")
	if _, errw, code := w.run(t, w.repo, "", "wait", "check"); code == 0 || !strings.Contains(errw, "is unset here") {
		t.Fatalf("override alone is not native identity: %d %s", code, errw)
	}
	delete(w.env, EnvSession)
	w.env["CODEX_THREAD_ID"] = "sess-a"
	if out, errw, code := w.run(t, w.repo, "", "wait", "--on", "api-work"); code != 0 || strings.Contains(out, "/loop") || !strings.Contains(out, "Codex") {
		t.Fatalf("native declaration advice: %d %q %s", code, out, errw)
	}
	if _, errw, code := w.run(t, w.repo, "", "release", "api-work", "--session", "sess-b"); code != 0 {
		t.Fatalf("release waiting target: %d %s", code, errw)
	}
	w.env[EnvSession] = "sess-b"
	if _, errw, code := w.run(t, w.repo, "", "wait", "check"); code == 0 || !strings.Contains(errw, "harness's own session") {
		t.Fatalf("override must not consume LANDED: %d %s", code, errw)
	}
	delete(w.env, EnvSession)
	w.env[EnvClaudeSession] = "sess-b"
	if _, errw, code := w.run(t, w.repo, "", "wait", "check"); code == 0 || !strings.Contains(errw, "disagree") {
		t.Fatalf("conflicting native check must not consume LANDED: %d %s", code, errw)
	}
	delete(w.env, EnvClaudeSession)
	if out, errw, code := w.run(t, w.repo, "", "wait", "check"); code != 0 || !strings.HasPrefix(out, "LANDED:") || strings.Contains(out, "/loop") {
		t.Fatalf("completion must remain for its native caller: %d %q %s", code, out, errw)
	}
	if out, errw, code := w.run(t, w.repo, "", "wait", "check"); code != 0 || !strings.HasPrefix(out, "NO WAIT:") {
		t.Fatalf("native completion delivered once: %d %q %s", code, out, errw)
	}
	// A revival receives a new ledger incarnation. The old wait stays old;
	// this does not claim a thread id can distinguish a delayed old shell.
	if _, errw, code := w.run(t, w.repo, "", "wait", "--until", "1h"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := w.run(t, w.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := w.run(t, w.repo, "", "hello"); code != 0 {
		t.Fatalf("manual native revival: %d %s", code, errw)
	}
	if out, errw, code := w.run(t, w.repo, "", "wait", "check"); code != 0 || !strings.HasPrefix(out, "NO WAIT:") {
		t.Fatalf("new incarnation inherited old wait: %d %q %s", code, out, errw)
	}
}

func codexHookJSON(t *testing.T, id, cwd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"session_id": id, "cwd": cwd, "buddy_harness": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCodexHooksDoNotBorrowClaudeProcess(t *testing.T) {
	boundedParallel(t)
	for _, marker := range []string{"native", "json", "environment"} {
		f := newFixture(t)
		f.initAndHello(t)
		f.env["HERDR_PANE_ID"] = "a-claude-parent-pane"
		in := hookJSON("sess-a", f.repo, "", "")
		switch marker {
		case "json":
			in = codexHookJSON(t, "sess-a", f.repo)
		case "environment":
			f.env["BUDDY_HARNESS"] = "codex"
		case "native":
			f.env["CODEX_THREAD_ID"] = "sess-a"
		}
		f.asProcess(100, 10, func() {
			for _, verb := range []string{"hello", "beat", "busy"} {
				if _, errw, code := f.run(t, f.repo, in, verb); code == 0 || !strings.Contains(errw, "unsupported") {
					t.Fatalf("unfenced %s with Codex marker=%v: %s", verb, marker, errw)
				}
			}
		})
		st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		procs, err := st.SessionProcs()
		si, _, siErr := st.SessionByID("sess-a")
		st.Close()
		if err != nil || len(procs["sess-a"]) != 0 || siErr != nil || si.Terminal != "" {
			t.Fatalf("Codex marker=%v borrowed Claude ancestor or pane: %v %v %+v %v", marker, procs, err, si, siErr)
		}
	}
}

func TestCodexHookMarkersRefuseConflicts(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	for _, tc := range []struct{ marker, input string }{
		{"unknown", hookJSON("sess-a", f.repo, "", "")},
		{"claude", codexHookJSON(t, "sess-a", f.repo)},
	} {
		f.env["BUDDY_HARNESS"] = tc.marker
		if _, errw, code := f.run(t, f.repo, tc.input, "hello"); code == 0 {
			t.Fatalf("invalid/conflicting marker %s accepted: %s", tc.marker, errw)
		}
	}
	f.env["BUDDY_HARNESS"] = "codex"
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Bash", ""), "gate"); code != 0 || errw != "" {
		t.Fatalf("valid environment marker control: %d %s", code, errw)
	}
}

func TestCodexHookWaitAdvice(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	w.env["BUDDY_HARNESS"] = "codex"
	if out, errw, code := w.run(t, w.repo, "", "hello", "--session", "sess-a"); code != 0 || !strings.Contains(out, "you are WAITING") || strings.Contains(out, "/loop") {
		t.Fatalf("marked manual Codex hello wait advice: %d %q %s", code, out, errw)
	}
	delete(w.env, "BUDDY_HARNESS")
	w.env["CODEX_THREAD_ID"] = "sess-a"
	if out, errw, code := w.run(t, w.repo, "", "hello"); code != 0 || !strings.Contains(out, "you are WAITING") || strings.Contains(out, "/loop") {
		t.Fatalf("manual native Codex hello wait advice: %d %q %s", code, out, errw)
	}
	if _, errw, code := w.run(t, w.repo, "", "release", "api-work", "--session", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	if out, errw, code := w.run(t, w.repo, "", "wait", "check"); code != 0 || !strings.Contains(out, "LANDED") || strings.Contains(out, "/loop") {
		t.Fatalf("native Codex wait notice: %d %q %s", code, out, errw)
	}
	if out, errw, code := w.run(t, w.repo, "", "wait", "clear"); code != 0 || strings.Contains(out, "/loop") {
		t.Fatalf("native Codex clear notice: %d %q %s", code, out, errw)
	}
	if _, errw, code := w.run(t, w.repo, "", "wait", "--until", "1h"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := w.run(t, w.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	if out, errw, code := w.run(t, w.repo, "", "hello"); code != 0 || !strings.Contains(out, "earlier run") || strings.Contains(out, "/loop") {
		t.Fatalf("Codex predecessor wait advice: %d %q %s", code, out, errw)
	}
}

func TestCodexHooksRefuseUnsafeLifecycle(t *testing.T) {
	boundedParallel(t)
	for _, verb := range []string{"hello", "beat", "busy", "bye", "idle"} {
		f := newFixture(t)
		f.initAndHello(t)
		if _, errw, code := f.run(t, f.repo, codexHookJSON(t, "sess-a", f.repo), verb); code == 0 || !strings.Contains(errw, "unsupported") {
			t.Fatalf("Codex %s needs an incarnation-aware adapter: %d %s", verb, code, errw)
		}
		st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		si, known, err := st.SessionByID("sess-a")
		idle, idleErr := st.IdleSessions()
		st.Close()
		if err != nil || !known || !si.Live() || idleErr != nil || len(idle) != 0 {
			t.Fatalf("refused %s changed lifecycle: %+v %v %v %v", verb, si, idle, err, idleErr)
		}
		// Existing Claude lifecycle remains supported as the positive control.
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), verb); code != 0 {
			t.Fatalf("Claude %s control: %d %s", verb, code, errw)
		}
	}
}

func TestCodexHookIdentityMismatchRefusesBeforeWrite(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.env["CODEX_THREAD_ID"] = "sess-a"
	if _, errw, code := f.run(t, f.repo, codexHookJSON(t, "sess-b", f.repo), "hello"); code == 0 || !strings.Contains(errw, "disagree") {
		t.Fatalf("hook/native mismatch must refuse: %d %s", code, errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "sessions"); strings.Contains(out, "sess-a") || strings.Contains(out, "sess-b") {
		t.Fatalf("refused hook registered a session: %s", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "hello"); code != 0 {
		t.Fatalf("matching manual native registration control: %d %s", code, errw)
	}
}

// --session identifies a manual registration; it must not hide hook input.
// Otherwise an old SessionStart bypasses the raw Codex hook refusal, refreshes
// the replacement, or revives an ended replacement and orphans its claims.
func TestCodexHelloExplicitSessionCannotBypassHookFence(t *testing.T) {
	boundedParallel(t)
	for _, marker := range []string{"json", "environment", "native"} {
		for _, state := range []string{"live", "ended"} {
			t.Run(marker+"/"+state, func(t *testing.T) {
				f, predecessor := lifecycleFixture(t)
				lifecycleOK(t, f, "end", "--session", predecessor.SessionID, "--incarnation", predecessor.Incarnation)
				f.clock = f.clock.Add(time.Minute)
				current := lifecycleOK(t, f, "begin", "--session", predecessor.SessionID, "--previous", predecessor.Incarnation)
				if current.Incarnation == predecessor.Incarnation {
					t.Fatal("control did not create a newer incarnation")
				}
				if _, errw, code := f.run(t, f.repo, "", "claim", "replacement-work", "--session", current.SessionID, "--desc", "new incarnation", "--scope", "pkg/current"); code != 0 {
					t.Fatal(errw)
				}
				if _, errw, code := f.run(t, f.repo, "", "msg", current.SessionID, "assignment for replacement"); code != 0 {
					t.Fatal(errw)
				}
				lifecycleOK(t, f, "idle", "--session", current.SessionID, "--incarnation", current.Incarnation)
				if state == "ended" {
					lifecycleOK(t, f, "end", "--session", current.SessionID, "--incarnation", current.Incarnation)
				}
				st := lifecycleStore(t, f)
				before := lifecycleSnapshot(t, st, current.SessionID)
				if len(before.Claims) != 1 || before.Claims[0].State != "open" || before.Queued != 1 || before.Session.Live() != (state == "live") {
					t.Fatalf("replacement controls not armed: %+v", before)
				}
				extra := map[string]any{}
				switch marker {
				case "json":
					extra["buddy_harness"] = "codex"
				case "environment":
					f.env[EnvHarness] = "codex"
				case "native":
					f.env[EnvCodexThread] = current.SessionID
				}
				f.clock = f.clock.Add(time.Minute)
				out, errw, code := f.run(t, f.repo, lifecycleHookJSON(t, f, "SessionStart", extra), "hello", "--session", current.SessionID, "--label", "delayed-old-label")
				if code == 0 || !strings.Contains(errw, "unsupported") {
					t.Errorf("explicit --session bypassed Codex hook fence: exit=%d stdout=%q stderr=%q", code, out, errw)
				}
				if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
					t.Errorf("delayed hook changed replacement lifecycle/claims/inbox: before=%+v after=%+v", before, got)
				}

				// An actual manual call still has no hook JSON, even when its
				// caller supplies the Codex environment marker or native id.
				if out, errw, code := f.run(t, f.repo, "", "hello", "--session", current.SessionID); code != 0 {
					t.Fatalf("no-input manual registration control: exit=%d stdout=%q stderr=%q", code, out, errw)
				}
				if got := lifecycleSnapshot(t, st, current.SessionID); !got.Session.Live() || !got.Session.LastSeen.Equal(f.clock) || got.Queued != 1 {
					t.Fatalf("manual registration failed or consumed inbox: %+v", got)
				}
			})
		}
	}
}

func TestHelloExplicitSessionMustMatchHookIdentity(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "msg", "sess-b", "Claude hook control mail"); code != 0 {
		t.Fatal(errw)
	}
	st := lifecycleStore(t, f)
	beforeA := lifecycleSnapshot(t, st, "sess-a")
	beforeB := lifecycleSnapshot(t, st, "sess-b")
	f.clock = f.clock.Add(time.Minute)
	input := hookJSON("sess-b", f.wtB, "", "")
	if out, errw, code := f.run(t, f.repo, input, "hello", "--session", "sess-a"); code == 0 {
		t.Errorf("explicit identity overrode native hook identity: stdout=%q stderr=%q", out, errw)
	}
	if got := lifecycleSnapshot(t, st, "sess-a"); !reflect.DeepEqual(got, beforeA) {
		t.Errorf("mismatched hook changed explicit target: before=%+v after=%+v", beforeA, got)
	}
	if got := lifecycleSnapshot(t, st, "sess-b"); !reflect.DeepEqual(got, beforeB) {
		t.Errorf("mismatched hook changed payload target: before=%+v after=%+v", beforeB, got)
	}
	// A matching Claude hook keeps its established process/identity/delivery
	// route. Its payload cwd, rather than the caller's cwd, names the worktree.
	if out, errw, code := f.run(t, f.repo, input, "hello", "--session", "sess-b"); code != 0 || !strings.Contains(out, "Claude hook control mail") {
		t.Fatalf("matching Claude hook control: exit=%d stdout=%q stderr=%q", code, out, errw)
	}
	if got := lifecycleSnapshot(t, st, "sess-b"); got.Queued != 0 || !got.Session.LastSeen.Equal(f.clock) || got.Session.Worktree != canon(f.wtB) {
		t.Fatalf("matching Claude hook did not preserve its native route: %+v", got)
	}
}

func TestHelloExplicitSessionRejectsMalformedHookButAllowsTerminal(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	f.env[EnvCodexThread] = current.SessionID
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	f.clock = f.clock.Add(time.Minute)
	for _, input := range []string{"{", "{}", codexHookJSON(t, current.SessionID, f.repo) + "{}"} {
		if out, errw, code := f.run(t, f.repo, input, "hello", "--session", current.SessionID); code == 0 {
			t.Fatalf("explicit --session ignored malformed hook %q: stdout=%q stderr=%q", input, out, errw)
		}
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("malformed hook changed lifecycle: before=%+v after=%+v", before, got)
		}
	}
	// readHook must take its character-device branch before trying to decode
	// input, so an interactive manual registration remains supported.
	if out, errw, code := f.runStdin(t, f.repo, charDevice(t), "hello", "--session", current.SessionID); code != 0 {
		t.Fatalf("terminal manual hello control: exit=%d stdout=%q stderr=%q", code, out, errw)
	}
	if got := lifecycleSnapshot(t, st, current.SessionID); got.Session.Incarnation != current.Incarnation || !got.Session.LastSeen.Equal(f.clock) {
		t.Fatalf("terminal manual hello did not refresh its live registration: %+v", got)
	}
}
