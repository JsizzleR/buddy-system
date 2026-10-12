package harness

// Claude recovery requires a known terminal result and subsequent idle. A durable wake
// intent or a pending native prompt must not become fresh-work admission merely
// because a replacement process starts with an empty in-memory active turn.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type resumeCounter struct {
	fakeBackend
	resumes int
}

func (b *resumeCounter) Resume(_ context.Context, _ Launch, id string, auth *LaunchAuthorization) (string, error) {
	b.resumes++
	if err := fakeSpawn(auth); err != nil {
		return "", err
	}
	return id, nil
}

func TestClaudeResumeRequiresTerminalJournal(t *testing.T) {
	for _, name := range []string{"complete", "disconnected", "second-complete", "no-turn", "intent-only", "unknown-wake", "pending-approval", "lost-result", "lost-idle", "aborted-streaming", "aborted-tools", "unknown-abort", "aborted-no-idle", "aborted-no-ref", "result-error", "wrong-turn", "late-busy", "late-approval", "ended"} {
		t.Run(name, func(t *testing.T) {
			id := Identity{"buddy.lifecycle.v1", "claude", "00000000-0000-4000-8000-000000000001", "captured"}
			path := filepath.Join(t.TempDir(), "receipt.json")
			data, _ := json.Marshal(Saved{Schema: Schema, Identity: id, Launch: Launch{Worktree: t.TempDir()}})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			rows := []Receipt{{Schema: Schema, Identity: id, Operation: "start"}}
			wake := func(mid int64, state, turn string) {
				rows = append(rows, Receipt{Schema: Schema, Identity: id, Operation: "send", MessageID: mid, Delivery: &Delivery{State: state, TurnID: turn}})
			}
			event := func(kind, turn, status, errText string) {
				rows = append(rows, Receipt{Schema: Schema, Identity: id, Operation: "event", NativeEvent: &Event{Kind: kind, NativeSessionID: id.SessionID, TurnID: turn, ResultStatus: status, ResultRef: "result-ref", Err: errText}})
			}
			if name != "no-turn" {
				wake(1, "unknown", "")
				if name != "intent-only" {
					state := "delivered"
					if name == "unknown-wake" {
						state = "unknown"
					}
					wake(1, state, "turn-1")
					event("busy", "turn-1", "", "")
					if name == "pending-approval" {
						event("approval", "turn-1", "", "")
					}
					if name != "pending-approval" && name != "lost-result" {
						status, errText, turn := "completed", "", "turn-1"
						if strings.HasPrefix(name, "aborted-") {
							status = "aborted_streaming"
							if name == "aborted-tools" {
								status = "aborted_tools"
								// Pending approval is invalidated by this input's
								// terminal result, not by opening another stream.
								event("approval", "turn-1", "", "")
							}
							errText = "native result: error_during_execution/" + status
						}
						if name == "unknown-abort" {
							status = "aborted_unqualified"
						}
						if name == "result-error" {
							errText = "native error"
						}
						if name == "wrong-turn" {
							turn = "other"
						}
						event("result", turn, status, errText)
						if name == "aborted-no-ref" {
							rows[len(rows)-1].NativeEvent.ResultRef = ""
						}
					}
					if name != "lost-idle" && name != "pending-approval" && name != "aborted-no-idle" {
						event("idle", "turn-1", "", "")
					}
				}
			}
			switch name {
			case "disconnected":
				event("disconnected", "", "", "EOF")
			case "late-busy":
				event("busy", "turn-1", "", "")
			case "late-approval":
				event("approval", "turn-1", "", "")
			case "ended":
				event("ended", "", "", "")
			case "second-complete":
				wake(2, "delivered", "turn-2")
				event("busy", "turn-2", "", "")
				event("result", "turn-2", "completed", "")
				event("idle", "turn-2", "", "")
			}
			f, err := os.Create(path + ".events")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if err := json.NewEncoder(f).Encode(r); err != nil {
					t.Fatal(err)
				}
			}
			f.Close()
			writeGoneOwnership(t, path, id)
			b := &resumeCounter{}
			l := &fakeLedger{identity: id}
			c, err := NewController(b, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r, err := c.Resume(context.Background())
			allowed := name == "complete" || name == "disconnected" || name == "second-complete" || name == "aborted-streaming" || name == "aborted-tools"
			if allowed {
				if err != nil || b.resumes != 1 || !r.Identity.Same(id) || r.Completion.State != "unknown" {
					t.Fatalf("positive control: %+v %v", r, err)
				}
			} else if err == nil || b.resumes != 0 || len(l.events) != 0 {
				t.Fatalf("uncertain Claude recovery touched native/ledger: resumes=%d events=%v err=%v", b.resumes, l.events, err)
			}
		})
	}
}

func TestClaudeResumeExactNativeSession(t *testing.T) {
	b, launch, ctx := claudeFixture(t)
	impl := b.(*claudeBackend)
	fake := strings.Replace(claudeFake, "sid=sys.argv[sys.argv.index('--session-id')+1]", "assert '--session-id' not in sys.argv and '--continue' not in sys.argv and '--fork-session' not in sys.argv\nsid=sys.argv[sys.argv.index('--resume')+1]", 1)
	if err := os.WriteFile(impl.executable, []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	if b.Capabilities()["resume"].Support != "experimental" {
		t.Fatal("completed-turn resume not exposed")
	}
	id, err := b.Resume(ctx, launch, launch.NativeSessionID, testAuthorization(t))
	if err != nil || id != launch.NativeSessionID {
		t.Fatalf("resume: %q %v", id, err)
	}
	if _, err := b.Send(ctx, id, "complete"); err != nil {
		t.Fatal(err)
	}
	claudeEvent(t, ctx, b, "idle")
}

func TestClaudeResumeRechecksAdmission(t *testing.T) {
	for _, name := range []string{"changed-session", "bad-uuid", "version", "permission", "missing-gate", "effective-gate", "mod-inventory", "native-session-mismatch"} {
		t.Run(name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			impl := b.(*claudeBackend)
			id := launch.NativeSessionID
			fake := strings.Replace(claudeFake, "sid=sys.argv[sys.argv.index('--session-id')+1]", "sid=sys.argv[sys.argv.index('--resume')+1]", 1)
			switch name {
			case "changed-session":
				launch.NativeSessionID = "00000000-0000-4000-8000-000000000002"
			case "bad-uuid":
				id = "latest"
			case "version":
				fake = strings.ReplaceAll(fake, claudeContractVersion, "2.1.288")
			case "permission":
				launch.PermissionMode = "bypassPermissions"
			case "missing-gate":
				if err := os.WriteFile(launch.HookConfig, []byte(`{"hooks":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "effective-gate":
				fake = strings.Replace(fake, "body={'effective':json.load(open(sys.argv[sys.argv.index('--settings')+1]))}", "body={'effective':{'hooks':{},'disableAllHooks':True}}", 1)
			case "mod-inventory":
				fake = strings.ReplaceAll(fake, "cc-plugin-agents-md", "unknown-plugin")
			case "native-session-mismatch":
				fake = strings.Replace(fake, "'type':'control_response','response'", "'session_id':'another-session','type':'control_response','response'", 1)
			}
			if err := os.WriteFile(impl.executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Resume(ctx, launch, id, testAuthorization(t)); err == nil {
				t.Fatal("incompatible resume admitted")
			}
		})
	}
}
