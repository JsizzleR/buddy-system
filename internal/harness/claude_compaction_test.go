package harness

// Native 2.1.290 and 2.1.291 manual compaction emits a boundary and a local-command
// result without terminal_reason. Neither observation alone is completion.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const compactSession = "00000000-0000-4000-8000-000000000001"
const compactBoundary = "00000000-0000-4000-8000-000000000002"

func compactFrame() map[string]any {
	return map[string]any{"type": "system", "subtype": "compact_boundary", "session_id": compactSession,
		"uuid": compactBoundary, "compact_metadata": map[string]any{"trigger": "manual", "pre_tokens": 101}}
}
func compactResult() map[string]any {
	return map[string]any{"type": "result", "subtype": "success", "session_id": compactSession,
		"uuid": "result-compact", "local_command": "compact", "num_turns": 0, "is_error": false,
		"user_message_uuid": "input", "user_message_uuids": []string{"input"}, "queued_turn_count": 0}
}
func sendCompactFrame(t *testing.T, b *claudeBackend, v map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b.frame(raw)
}

func TestClaudeCompactionCapabilityRequiresExactContract(t *testing.T) {
	for _, version := range []string{"", "2.1.289", "2.1.290", "2.1.291", "2.1.292", "2.1.290-extra", "2.1.291-extra"} {
		t.Run(version, func(t *testing.T) {
			b := newClaude("")
			b.version = version
			want := "unsupported"
			if version == "2.1.290" || version == "2.1.291" {
				want = "experimental"
			}
			if got := b.Capabilities()["compaction_observation"].Support; got != want {
				t.Fatalf("compaction capability = %q, want %q", got, want)
			}
		})
	}
}

func TestClaudeCompactionBoundaryIsOnlyAnObservation(t *testing.T) {
	for _, nativeVersion := range []string{"2.1.290", "2.1.291"} {
		t.Run(nativeVersion, func(t *testing.T) {
			for _, name := range []string{"valid", "foreign", "missing-session", "missing-id", "bad-id", "auto", "missing-trigger", "old-version", "new-version"} {
				t.Run(name, func(t *testing.T) {
					b := newClaude("")
					b.nativeID = compactSession
					b.version = nativeVersion
					b.active = "input"
					b.state = "waiting_approval"
					b.approvals["pending"] = claudePermission{turnID: "input"}
					f := compactFrame()
					switch name {
					case "foreign":
						f["session_id"] = "foreign"
					case "missing-session":
						delete(f, "session_id")
					case "missing-id":
						delete(f, "uuid")
					case "bad-id":
						f["uuid"] = "bad"
					case "auto":
						f["compact_metadata"] = map[string]any{"trigger": "auto"}
					case "missing-trigger":
						delete(f, "compact_metadata")
					case "old-version":
						b.version = "2.1.289"
					case "new-version":
						b.version = "2.1.292"
					}
					err := sendCompactFrame(t, b, f)
					if name == "valid" {
						if err != nil {
							t.Fatal(err)
						}
						select {
						case e := <-b.events:
							if e.Kind != "compaction_boundary" || e.ItemID != compactBoundary || e.TurnID != "" || e.ResultRef != "" || e.ResultStatus != "" {
								t.Fatalf("promoted/misidentified boundary: %+v", e)
							}
						default:
							t.Fatal("boundary missing")
						}
					} else {
						select {
						case e := <-b.events:
							t.Fatalf("invalid boundary emitted: %+v", e)
						default:
						}
					}
					if b.active != "input" || b.state != "waiting_approval" || len(b.approvals) != 1 {
						t.Fatal("boundary mutated active work")
					}
				})
			}
		})
	}
}
func TestClaudeLocalCompactionResultRequiresMeasuredShape(t *testing.T) {
	for _, nativeVersion := range []string{"2.1.290", "2.1.291"} {
		t.Run(nativeVersion, func(t *testing.T) {
			for _, name := range []string{"valid", "no-boundary", "old-version", "new-version", "no-command", "other-command", "ordinary-result", "error", "missing-error", "bad-subtype", "nonzero-turns", "missing-turns", "terminal-reason", "wrong-input", "coalesced", "queued", "missing-queue", "no-ack", "pending-approval", "previous-turn-boundary"} {
				t.Run(name, func(t *testing.T) {
					b := newClaude("")
					b.nativeID = compactSession
					b.version = nativeVersion
					b.active = "input"
					b.activeAck = true
					b.state = "busy"
					if name != "no-boundary" {
						if err := sendCompactFrame(t, b, compactFrame()); err != nil {
							t.Fatal(err)
						}

						select {
						case <-b.events:
						default:
							t.Fatal("expected native event missing")
						}
					}
					f := compactResult()
					switch name {
					case "old-version":
						b.version = "2.1.289"
					case "new-version":
						b.version = "2.1.292"
					case "no-command":
						delete(f, "local_command")
					case "other-command":
						f["local_command"] = "clear"
					case "ordinary-result":
						delete(f, "local_command")
						f["terminal_reason"] = "completed"
					case "error":
						f["is_error"] = true
					case "missing-error":
						delete(f, "is_error")
					case "bad-subtype":
						f["subtype"] = "error_during_execution"
					case "nonzero-turns":
						f["num_turns"] = 1
					case "missing-turns":
						delete(f, "num_turns")
					case "terminal-reason":
						f["terminal_reason"] = "unqualified"
					case "wrong-input":
						f["user_message_uuid"] = "other"
					case "coalesced":
						f["user_message_uuids"] = []string{"input", "other"}
					case "queued":
						f["queued_turn_count"] = 1
					case "missing-queue":
						delete(f, "queued_turn_count")
					case "no-ack":
						b.activeAck = false
					case "pending-approval":
						b.approvals["pending"] = claudePermission{turnID: "input"}
					case "previous-turn-boundary":
						b.active = "new-input"
						f["user_message_uuid"] = "new-input"
						f["user_message_uuids"] = []string{"new-input"}
					}
					if err := sendCompactFrame(t, b, f); err != nil {
						t.Fatal(err)
					}
					e := <-b.events
					if name == "valid" {
						if e.ResultStatus != "local_compact_completed" || e.ItemID != compactBoundary || e.TurnID != "input" || e.Err != "" {
							t.Fatalf("local compact not recognized: %+v", e)
						}
					} else if e.ResultStatus == "local_compact_completed" || e.ItemID != "" {
						t.Fatalf("unqualified local result admitted: %+v", e)
					}
				})
			}
		})
	}
}
func TestControllerClaudeBoundaryPreservesApprovalAndJournal(t *testing.T) {
	for _, nativeVersion := range []string{"2.1.290", "2.1.291"} {
		t.Run(nativeVersion, func(t *testing.T) {
			b := &fakeBackend{}
			l := &fakeLedger{queued: true}
			path := filepath.Join(t.TempDir(), "receipt")
			c, err := NewController(b, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx := context.Background()
			if _, err = c.Start(ctx, "claude", Launch{Worktree: t.TempDir(), HarnessVersion: nativeVersion}, "test"); err != nil {
				t.Fatal(err)
			}
			a := Approval{ID: json.RawMessage(`"pending"`), NativeSessionID: l.identity.SessionID, TurnID: "input"}
			before, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a})
			if err != nil {
				t.Fatal(err)
			}
			e := Event{Kind: "compaction_boundary", NativeSessionID: l.identity.SessionID, ItemID: compactBoundary}
			r, err := c.Observe(ctx, e)
			if err != nil {
				t.Fatal(err)
			}
			if r.State != before.State || !reflect.DeepEqual(r.Approvals, before.Approvals) || r.PendingApproval != before.PendingApproval || r.Completion.State != "unknown" || r.BodyDelivery.State != "unknown" || len(l.events) != 0 {
				t.Fatal("boundary changed coordination")
			}
			for _, name := range []string{"stale", "missing-id", "fabricated-turn", "wrong-version", "new-version"} {
				bad := e
				switch name {
				case "stale":
					l.stale = true
				case "missing-id":
					bad.ItemID = ""
				case "fabricated-turn":
					bad.TurnID = "invented"
				case "wrong-version":
					c.saved.Launch.HarnessVersion = "2.1.289"
				case "new-version":
					c.saved.Launch.HarnessVersion = "2.1.292"
				}
				if _, err := c.Observe(ctx, bad); err == nil {
					t.Fatalf("accepted %s", name)
				}
				l.stale = false
				c.saved.Launch.HarnessVersion = nativeVersion
			}
			data, err := os.ReadFile(path + ".events")
			if err != nil {
				t.Fatal(err)
			}
			var found int
			for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
				var row Receipt
				if err := json.Unmarshal(line, &row); err != nil {
					t.Fatal(err)
				}
				if row.NativeEvent != nil && row.NativeEvent.Kind == "compaction_boundary" {
					found++
					if row.Identity != l.identity || *row.NativeEvent != e {
						t.Fatal("journal changed captured boundary identity")
					}
				}
			}
			if found != 1 {
				t.Fatalf("journal has %d boundaries", found)
			}
		})
	}
}

func TestClaudeCompactionRecoveryRequiresBoundaryResultAndIdle(t *testing.T) {
	for _, nativeVersion := range []string{"2.1.290", "2.1.291"} {
		t.Run(nativeVersion, func(t *testing.T) {
			for _, name := range []string{"valid", "no-boundary", "no-result", "no-idle", "wrong-ref", "error", "missing-result-ref", "reused-boundary", "old-version", "new-version", "late-approval", "replayed-boundary"} {
				t.Run(name, func(t *testing.T) {
					id := Identity{"buddy.lifecycle.v1", "claude", compactSession, "captured"}
					var s claudeRecovery
					rows := []Receipt{{Schema: Schema, Identity: id, Operation: "start"},
						{Schema: Schema, Identity: id, Operation: "send", MessageID: 1, Delivery: &Delivery{State: "delivered", TurnID: "input"}}}
					observe := func(e Event) {
						r := Receipt{Schema: Schema, Identity: id, Operation: "event", NativeEvent: &e}
						rows = append(rows, r)
						s.observe(r)
					}
					observe(Event{Kind: "busy", NativeSessionID: compactSession, TurnID: "input"})
					if name != "no-boundary" {
						observe(Event{Kind: "compaction_boundary", NativeSessionID: compactSession, ItemID: compactBoundary})
					}
					e := Event{Kind: "result", NativeSessionID: compactSession, TurnID: "input", ResultStatus: "local_compact_completed", ResultRef: "result", ItemID: compactBoundary}
					if name == "wrong-ref" {
						e.ItemID = "wrong"
					}
					if name == "error" {
						e.Err = "error"
					}
					if name == "missing-result-ref" {
						e.ResultRef = ""
					}
					if name != "no-result" {
						observe(e)
					}
					if name != "no-idle" {
						observe(Event{Kind: "idle", NativeSessionID: compactSession, TurnID: "input"})
					}
					if name == "reused-boundary" || name == "replayed-boundary" {
						if name == "replayed-boundary" {
							observe(Event{Kind: "compaction_boundary", NativeSessionID: compactSession, ItemID: compactBoundary})
						}
						e.TurnID = "other"
						observe(e)
						observe(Event{Kind: "idle", NativeSessionID: compactSession, TurnID: "other"})
					}
					if name == "late-approval" {
						observe(Event{Kind: "approval", NativeSessionID: compactSession, TurnID: "input"})
					}
					version := nativeVersion
					if name == "old-version" {
						version = "2.1.289"
					}
					if name == "new-version" {
						version = "2.1.292"
					}
					got := s.resumable(map[int64]Delivery{1: {State: "delivered", TurnID: "input"}}, version)
					if got != (name == "valid") {
						t.Fatalf("resumable=%v", got)
					}
					// The same incomplete journals must refuse before a native child
					// starts or a ledger lifecycle event refreshes the captured owner.
					path := filepath.Join(t.TempDir(), "receipt")
					data, _ := json.Marshal(Saved{Schema: Schema, Identity: id, Launch: Launch{Worktree: t.TempDir(), HarnessVersion: version}})
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					var journal bytes.Buffer
					for _, row := range rows {
						if err := json.NewEncoder(&journal).Encode(row); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(path+".events", journal.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					writeGoneOwnership(t, path, id)
					b, l := &resumeCounter{}, &fakeLedger{identity: id}
					c, err := NewController(b, l, path)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					_, err = c.Resume(context.Background())
					if name == "valid" {
						if err != nil || b.resumes != 1 {
							t.Fatalf("positive resume: %v", err)
						}
					} else {
						after, readErr := os.ReadFile(path + ".events")
						if err == nil || b.resumes != 0 || len(l.events) != 0 || readErr != nil || !bytes.Equal(after, journal.Bytes()) {
							t.Fatalf("incomplete resume had side effects: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestClaudeReplayedBoundaryCannotQualifyANewInput(t *testing.T) {
	for _, nativeVersion := range []string{"2.1.290", "2.1.291"} {
		t.Run(nativeVersion, func(t *testing.T) {
			b := newClaude("")
			b.nativeID = compactSession
			b.version = nativeVersion
			b.active = "input"
			b.activeAck = true
			if err := sendCompactFrame(t, b, compactFrame()); err != nil {
				t.Fatal(err)
			}

			select {
			case <-b.events:
			default:
				t.Fatal("expected native event missing")
			}
			if err := sendCompactFrame(t, b, compactResult()); err != nil {
				t.Fatal(err)
			}

			select {
			case <-b.events:
			default:
				t.Fatal("expected native event missing")
			}

			select {
			case <-b.events:
			default:
				t.Fatal("expected native event missing")
			}
			b.active = "next"
			b.activeAck = true
			if err := sendCompactFrame(t, b, compactFrame()); err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-b.events:
				t.Fatalf("old boundary re-emitted: %+v", e)
			default:
			}
			f := compactResult()
			f["uuid"] = "next-result"
			f["user_message_uuid"] = "next"
			f["user_message_uuids"] = []string{"next"}
			if err := sendCompactFrame(t, b, f); err != nil {
				t.Fatal(err)
			}
			if e := <-b.events; e.ResultStatus == "local_compact_completed" {
				t.Fatal("old boundary qualified a new input")
			}
		})
	}
}

// The native cancellation probe returned the same zero-turn local success as
// completed /compact, but no boundary. It acknowledged the input only AFTER
// Send timed out. Even complete-looking later events must not silently promote
// that unknown delivery, and an earlier completed turn cannot cover a crash.
func TestClaudeInterruptedCompactionRefusesBeforeSideEffects(t *testing.T) {
	for _, nativeVersion := range []string{"2.1.290", "2.1.291"} {
		t.Run(nativeVersion, func(t *testing.T) {
			for _, name := range []string{"complete", "cancelled", "crash-intent", "lost-boundary", "lost-result", "lost-idle", "late-acceptance", "abort-after-boundary"} {
				t.Run(name, func(t *testing.T) {
					id := Identity{"buddy.lifecycle.v1", "claude", compactSession, "captured"}
					rows := []Receipt{{Schema: Schema, Identity: id, Operation: "start"}}
					wake := func(mid int64, state, turn string) {
						rows = append(rows, Receipt{Schema: Schema, Identity: id, Operation: "send", MessageID: mid, Delivery: &Delivery{State: state, TurnID: turn}})
					}
					observe := func(e Event) {
						e.NativeSessionID = id.SessionID
						rows = append(rows, Receipt{Schema: Schema, Identity: id, Operation: "event", NativeEvent: &e})
					}
					wake(1, "delivered", "original")
					observe(Event{Kind: "result", TurnID: "original", ResultStatus: "completed", ResultRef: "original-result"})
					observe(Event{Kind: "idle", TurnID: "original"})
					wake(2, "unknown", "")
					if name != "crash-intent" {
						state := "delivered"
						if name == "cancelled" || name == "late-acceptance" {
							state = "unknown"
						}
						wake(2, state, "compact")
						observe(Event{Kind: "acknowledged", TurnID: "compact"})
						observe(Event{Kind: "busy", TurnID: "compact"})
						if name != "cancelled" && name != "lost-boundary" {
							observe(Event{Kind: "compaction_boundary", ItemID: compactBoundary})
						}
						if name != "lost-result" {
							e := Event{Kind: "result", TurnID: "compact", ResultStatus: "local_compact_completed", ResultRef: "compact-result", ItemID: compactBoundary}
							if name == "cancelled" || name == "lost-boundary" {
								e.ResultStatus, e.ItemID = "unknown", ""
							}
							if name == "abort-after-boundary" {
								e.ResultStatus, e.ItemID, e.Err = "aborted_streaming", "", "native cancellation"
							}
							observe(e)
							if name != "lost-idle" {
								observe(Event{Kind: "idle", TurnID: "compact"})
							}
						}
					}
					path := filepath.Join(t.TempDir(), "receipt")
					saved, _ := json.Marshal(Saved{Schema: Schema, Identity: id, Launch: Launch{Worktree: t.TempDir(), HarnessVersion: nativeVersion}})
					if err := os.WriteFile(path, saved, 0600); err != nil {
						t.Fatal(err)
					}
					var journal bytes.Buffer
					for _, row := range rows {
						if err := json.NewEncoder(&journal).Encode(row); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(path+".events", journal.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					writeGoneOwnership(t, path, id)
					b, l := &resumeCounter{}, &fakeLedger{identity: id}
					c, err := NewController(b, l, path)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					_, err = c.Resume(context.Background())
					if name == "complete" {
						if err != nil || b.resumes != 1 {
							t.Fatalf("positive resume control: %v", err)
						}
					} else {
						after, readErr := os.ReadFile(path + ".events")
						afterSaved, savedErr := os.ReadFile(path)
						if err == nil || b.resumes != 0 || len(l.events) != 0 || readErr != nil || savedErr != nil ||
							!bytes.Equal(after, journal.Bytes()) || !bytes.Equal(afterSaved, saved) {
							t.Fatalf("uncertain compaction changed captured state: %v", err)
						}
					}
				})
			}
		})
	}
}
