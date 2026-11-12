package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The fake speaks the inspected native wire. It does not emulate a provider:
// these tests establish routing and refusal behavior, not installed hooks,
// native Claude's peer provenance handling, or real model execution.
const claudeFake = `#!/usr/bin/env python3
import json,sys
if __import__('os').environ.get('DISABLE_AUTOUPDATER')!='1': sys.exit(7)
if '--version' in sys.argv:
 print('2.1.289 (Claude Code)'); sys.exit(0)
def emit(v):
 print(json.dumps(v),flush=True)
sid=sys.argv[sys.argv.index('--session-id')+1]
active=None
for line in sys.stdin:
 m=json.loads(line)
 if m['type']=='control_request':
  body={}
  if m['request']['subtype']=='reload_plugins':
   body={'plugins':[{'name':'cc-plugin-agents-md','path':'builtin','source':'cc-plugin-agents-md@builtin'},{'name':'cc-plugin-plugin-authoring','path':'builtin','source':'cc-plugin-plugin-authoring@builtin'}],'error_count':0,'mcpServers':[]}
  if m['request']['subtype']=='get_settings':
   body={'effective':json.load(open(sys.argv[sys.argv.index('--settings')+1]))}
  emit({'type':'control_response','response':{'subtype':'success','request_id':m['request_id'],'response':body}})
  if m['request']['subtype']=='interrupt':
   emit({'type':'result','subtype':'success','is_error':False,'terminal_reason':'aborted_streaming','uuid':'native-interrupted-result','session_id':sid,'user_message_uuid':active['uuid'],'user_message_uuids':[active['uuid']],'queued_turn_count':0})
 elif m['type']=='user':
  active=m
  assert m['origin']=={'kind':'peer','from':'buddy-adapter'}
  assert m['parent_tool_use_id'] is None
  text=m['message']['content']
  if text=='no-replay': continue
  replay=dict(m,isReplay=True); replay.pop('origin'); emit(replay)
  if text in ('approval','interaction'):
   emit({'type':'control_request','request_id':'native-"approval','request':{'subtype':'can_use_tool','tool_name':'Write','input':{'file_path':'file.txt','content':'hello'},'tool_use_id':'native-tool-id','requires_user_interaction':text=='interaction'}})
  elif text=='complete':
   emit({'type':'result','subtype':'success','is_error':False,'terminal_reason':'completed','uuid':'native-result-ref','session_id':sid,'user_message_uuid':active['uuid'],'user_message_uuids':[active['uuid']],'queued_turn_count':0})
 elif m['type']=='control_response':
  r=m['response']; assert r['request_id']=='native-"approval'
  assert r['response']['toolUseID']=='native-tool-id'
  assert 'updatedPermissions' not in r['response']
  if r['response']['behavior']=='allow':
   assert r['response']['updatedInput']=={'file_path':'file.txt','content':'hello'}
  emit({'type':'result','subtype':'success','is_error':False,'terminal_reason':'completed','uuid':'native-approved-result','session_id':sid,'user_message_uuid':active['uuid'],'user_message_uuids':[active['uuid']],'queued_turn_count':0})
`

// liveBound bounds a step a test expects to SUCCEED (a fake worker's startup
// and initialize, a call the fake answers, an event it emits). It is a wedge
// detector, not a latency claim, so it is sized for the slowest machine that
// runs this suite rather than for the step.
//
// WHY NOT 5 s (#89). claudeFixture had 5 s, and run-i96's full tier went red
// on TestAdmissionMissingRecordRunsNothing's POSITIVE control at load 16
// (7.31 s, "initialize Claude streaming transport: context deadline exceeded";
// 10/10 alone in 1.3 s). The fake is a python3 process: its startup plus the
// -race transport's initialize is the step a loaded box stretches. A red there
// says nothing about the code and costs a whole tier rerun.
//
// MEASURED (2026-11-12, -race, -count=20 over the Claude-backend tests). One
// binary beside 12 busy loops, and four at once at load 250: no red; the
// slowest test 5.1 s. The binary under a background QoS clamp (`taskpolicy -c
// background`, inherited by every python3 fake) beside 12 busy loops at load
// 36-91: 4 of 118 runs red at 5 s, all "context deadline exceeded" on a
// positive step. With this bound, 1 of 60: one version probe stalled past the
// bound while 18 sibling subtests took 1-4 s and one other stalled 49 s and
// passed. The clamp starves without limit, so no bound clears it; it is the
// harshest inducer found, not a machine this suite runs on.
//
// WHY 2 MINUTES. Twenty times the worst unclamped step measured (5.1 s), and
// still well inside go test's default 10 m -test.timeout, so a context-aware
// wedge fails as the named step ("waiting for Claude <kind>", the call's own
// error) instead of as the timeout's panic. It is a budget, not a per-step
// renewal: claudeFixture's one context covers startup and every later call and
// event wait of its test. It bounds nothing that ignores its context; there,
// and when a wedge repeats across several cases until their two minutes add
// up to the package's 10 m, the bound is go test's timeout panic, which ends
// the binary and dumps every goroutine. A wait whose EXPIRY is the assertion (a
// 20-50 ms deadline a test expects to pass, a window in which nothing may
// arrive, a wait that must end before a helper's own timer) keeps its own
// short deadline.
const liveBound = 2 * time.Minute

// wantRefusal fails unless err is the refusal want names. A refusal test that
// accepted any error also passed when its fake stopped answering, on the
// deadline's own error; liveBound made that pass take two minutes instead of
// five seconds, and it was never the refusal under test (#90).
func wantRefusal(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || want == "" || !strings.Contains(err.Error(), want) {
		t.Fatalf("want the refusal %q, got: %v", want, err)
	}
}

func claudeFixture(t *testing.T) (Backend, Launch, context.Context) {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "claude")
	if err := os.WriteFile(executable, []byte(claudeFake), 0700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write|NotebookEdit","hooks":[{"type":"command","command":"/tmp/buddy-adapter hook --receipt /tmp/receipt --buddy /tmp/buddy"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveBound)
	t.Cleanup(cancel)
	b, err := NewClaude(executable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, Launch{Worktree: dir, NativeSessionID: "00000000-0000-4000-8000-000000000001", PermissionMode: "workspace-write",
		HarnessVersion: claudeContractVersion, HookConfig: settings, GateCommand: "/tmp/buddy-adapter hook --receipt /tmp/receipt --buddy /tmp/buddy", Hooks: Compatibility{Mods: "builtin-only-2.1.289"}}, ctx
}

func claudeEvent(t *testing.T, ctx context.Context, b Backend, kind string) Event {
	t.Helper()
	for {
		select {
		case e, ok := <-b.Events():
			if !ok {
				t.Fatalf("Claude event stream closed awaiting %s", kind)
			}
			if e.Kind == kind {
				return e
			}
		case <-ctx.Done():
			t.Fatalf("waiting for Claude %s: %v", kind, ctx.Err())
		}
	}
}

func TestClaudeNativeWireApprovalAndResult(t *testing.T) {
	b, launch, ctx := claudeFixture(t)
	id, err := b.Start(ctx, launch, testAuthorization(t))
	if err != nil {
		t.Fatal(err)
	}
	if id != launch.NativeSessionID {
		t.Fatalf("native identity changed: %q", id)
	}
	delivery, err := b.Send(ctx, id, "approval")
	if err != nil || delivery.State != "delivered" || delivery.TurnID == "" {
		t.Fatalf("delivery: %+v %v", delivery, err)
	}
	ack := claudeEvent(t, ctx, b, "acknowledged")
	if ack.TurnID != delivery.TurnID {
		t.Fatalf("ack lost native input UUID: %+v", ack)
	}
	e := claudeEvent(t, ctx, b, "approval")
	if e.Approval == nil || string(e.Approval.ID) != `"native-\"approval"` || e.Approval.ItemID != "native-tool-id" {
		t.Fatalf("approval identifiers lost: %+v", e.Approval)
	}
	observation, err := b.Inspect(ctx, id)
	if err != nil || observation.State != "waiting_approval" {
		t.Fatalf("pending approval misreported: %+v %v", observation, err)
	}
	d := ApprovalDecision{ID: e.Approval.ID, Decision: "allow", NativeSessionID: id, TurnID: delivery.TurnID}
	wrong := d
	wrong.NativeSessionID = "successor"
	if err := b.Approve(ctx, wrong); err == nil {
		t.Fatal("approval for another session accepted")
	}
	wrong = d
	wrong.TurnID = "successor-turn"
	if err := b.Approve(ctx, wrong); err == nil {
		t.Fatal("approval for another turn accepted")
	}
	if err := b.Approve(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := b.Approve(ctx, d); err == nil {
		t.Fatal("same native approval applied twice")
	}
	result := claudeEvent(t, ctx, b, "result")
	if result.ResultRef != "native-approved-result" || result.TurnID != delivery.TurnID || result.Err != "" {
		t.Fatalf("native result/reference lost: %+v", result)
	}
	claudeEvent(t, ctx, b, "idle")
	observation, err = b.Inspect(ctx, id)
	if err != nil || observation.State != "idle" {
		t.Fatalf("turn result not idle: %+v %v", observation, err)
	}
}

func TestClaudeUnknownDeliveryIsNotRetried(t *testing.T) {
	b, launch, ctx := claudeFixture(t)
	id, err := b.Start(ctx, launch, testAuthorization(t))
	if err != nil {
		t.Fatal(err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	delivery, err := b.Send(sendCtx, id, "no-replay")
	if err == nil || delivery.State != "unknown" || delivery.TurnID == "" {
		t.Fatalf("pipe write claimed delivery: %+v %v", delivery, err)
	}
	if _, err := b.Send(ctx, id, "complete"); err == nil {
		t.Fatal("unreconciled input retransmitted as another turn")
	}
	if err := b.Interrupt(ctx, id, delivery.TurnID); err != nil {
		t.Fatal(err)
	}
	result := claudeEvent(t, ctx, b, "result")
	if result.Err == "" || result.ResultRef != "native-interrupted-result" {
		t.Fatalf("interrupt became successful completion: %+v", result)
	}
	if _, err := b.Send(ctx, id, "complete"); err != nil {
		t.Fatalf("completed interrupt did not free transport: %v", err)
	}
}

func TestClaudeInteractiveApprovalFailsClosed(t *testing.T) {
	b, launch, ctx := claudeFixture(t)
	id, err := b.Start(ctx, launch, testAuthorization(t))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := b.Send(ctx, id, "interaction")
	if err != nil {
		t.Fatal(err)
	}
	e := claudeEvent(t, ctx, b, "approval")
	d := ApprovalDecision{ID: e.Approval.ID, Decision: "allow", NativeSessionID: id, TurnID: delivery.TurnID}
	if err := b.Approve(ctx, d); err == nil {
		t.Fatal("interactive-only request approved without native user surface")
	}
	d.Decision = "deny"
	if err := b.Approve(ctx, d); err != nil {
		t.Fatalf("explicit deny should still work: %v", err)
	}
	claudeEvent(t, ctx, b, "result")
}

func TestClaudeLaunchCompatibility(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Launch)
		want   string
	}{
		{"unknown mods", func(l *Launch) { l.Hooks.Mods = "unknown" }, "unsupported Claude Mod profile"},
		{"missing mod profile", func(l *Launch) { l.Hooks.Mods = "" }, "unsupported Claude Mod profile"},
		{"legacy no mods assertion", func(l *Launch) { l.Hooks.NoMods = "observed_absent" }, "unsupported Claude no_mods assertion"},
		{"old hook version", func(l *Launch) { l.HarnessVersion = "2.1.287" }, "no admission record for claude \"2.1.287\""},
		{"future version", func(l *Launch) { l.HarnessVersion = "2.1.292" }, "no admission record for claude \"2.1.292\""},
		{"missing settings", func(l *Launch) { l.HookConfig = "" }, "requires an explicit absolute hook settings path"},
		{"bypass permissions", func(l *Launch) { l.PermissionMode = "bypassPermissions" }, "unsupported Claude permission mode"},
		{"auto permissions", func(l *Launch) { l.PermissionMode = "auto" }, "unsupported Claude permission mode"},
		{"invalid native ID", func(l *Launch) { l.NativeSessionID = "made-up-id" }, "Claude native session id must be a UUID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			test.change(&launch)
			_, err := b.Start(ctx, launch, testAuthorization(t))
			wantRefusal(t, err, test.want)
		})
	}
	// A caller's version declaration is not evidence of the executable's version.
	b, launch, ctx := claudeFixture(t)
	c := b.(*claudeBackend)
	if err := os.WriteFile(c.executable, []byte("#!/bin/sh\nprintf '2.1.288 (Claude Code)\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := b.Start(ctx, launch, testAuthorization(t))
	if AdmissionCode(err) != "version_mismatch" {
		t.Fatalf("declared version hid the installed mismatch: %v", err)
	}
}

// Each measured version has its own explicit profile. Neither a matching
// profile nor another supported executable version may hide a launch mismatch.
func TestClaudeVersionProfileBinding(t *testing.T) {
	for _, version := range []string{"2.1.289", "2.1.290", "2.1.291"} {
		faults := []struct{ kind, other string }{{"", ""}, {"inventory", ""}}
		for _, other := range []string{"2.1.289", "2.1.290", "2.1.291"} {
			if other != version {
				faults = append(faults, struct{ kind, other string }{"profile", other}, struct{ kind, other string }{"executable", other})
			}
		}
		for _, mode := range []string{"start", "resume"} {
			for _, fault := range faults {
				t.Run(version+"/"+mode+"/"+fault.kind+fault.other, func(t *testing.T) {
					b, launch, ctx := claudeFixture(t)
					launch.HarnessVersion = version
					launch.Hooks.Mods = "builtin-only-" + version
					fake := strings.ReplaceAll(claudeFake, claudeContractVersion, version)
					switch fault.kind {
					case "profile":
						launch.Hooks.Mods = "builtin-only-" + fault.other
					case "executable":
						fake = strings.ReplaceAll(fake, version, fault.other)
					case "inventory":
						fake = strings.ReplaceAll(fake, "cc-plugin-agents-md", "cc-plugin-unqualified")
					}
					if mode == "resume" {
						fake = strings.ReplaceAll(fake, "--session-id", "--resume")
					}
					if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
						t.Fatal(err)
					}
					var err error
					if mode == "resume" {
						_, err = b.Resume(ctx, launch, launch.NativeSessionID, testAuthorization(t))
					} else {
						_, err = b.Start(ctx, launch, testAuthorization(t))
					}
					if (err != nil) != (fault.kind != "") {
						t.Fatalf("fault %q: %v", fault, err)
					}
					switch fault.kind {
					case "profile":
						wantRefusal(t, err, "unsupported Claude Mod profile: require builtin-only-"+version)
					case "executable":
						if AdmissionCode(err) != "version_mismatch" {
							t.Fatalf("fault %q: want version_mismatch, got %v", fault, err)
						}
					case "inventory":
						wantRefusal(t, err, "Claude Mod inventory contains an unqualified plugin identity")
					}
					if err == nil {
						want := "unsupported"
						if version == "2.1.290" || version == "2.1.291" {
							want = "experimental"
						}
						if got := b.Capabilities()["compaction_observation"].Support; got != want {
							t.Fatalf("compaction qualification mismatch: got %s, want %s", got, want)
						}
					}
				})
			}
		}
	}
}

func TestClaudeUnqualifiedExactVersionRefused(t *testing.T) {
	for _, version := range []string{"2.1.288", "2.1.292", "2.1.290-extra", "2.1.291-extra", ""} {
		t.Run(version, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			launch.HarnessVersion = version
			launch.Hooks.Mods = "builtin-only-" + version
			fake := strings.ReplaceAll(claudeFake, claudeContractVersion, version)
			if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := b.Start(ctx, launch, testAuthorization(t))
			wantRefusal(t, err, "no admission record for claude "+strconv.Quote(version))
		})
	}
}

// The native builtin agents-md plugin is itself a Mod. A caller's no-Mod
// assertion therefore hid an unobserved, potentially permission-changing set.
// Require the exact qualified plugin identities on the native reload response,
// before any user input. An enabled but harmless observer still refuses: the
// bounded profile does not infer privileges from an arbitrary plugin's name.
func TestClaudeNativeModProfileBeforeInput(t *testing.T) {
	const baseline = `{'plugins':[{'name':'cc-plugin-agents-md','path':'builtin','source':'cc-plugin-agents-md@builtin'},{'name':'cc-plugin-plugin-authoring','path':'builtin','source':'cc-plugin-plugin-authoring@builtin'}],'error_count':0,'mcpServers':[]}`
	for _, test := range []struct{ name, mutate, want string }{
		{"missing inventory", `body.pop('plugins')`, "differs from the qualified builtin profile"},
		{"null inventory", `body['plugins']=None`, "differs from the qualified builtin profile"},
		{"empty inventory", `body['plugins']=[]`, "differs from the qualified builtin profile"},
		{"missing builtin", `body['plugins'].pop()`, "differs from the qualified builtin profile"},
		{"duplicate builtin", `body['plugins'][1]=body['plugins'][0]`, "unqualified plugin identity"},
		{"unknown builtin", `body['plugins'][0]['name']='cc-plugin-unknown'`, "unqualified plugin identity"},
		{"lookalike path", `body['plugins'][0]['path']='/tmp/builtin'`, "unqualified plugin identity"},
		{"missing source", `body['plugins'][0].pop('source')`, "unqualified plugin identity"},
		{"different source", `body['plugins'][0]['source']='cc-plugin-agents-md@example'`, "unqualified plugin identity"},
		{"unknown plugin field", `body['plugins'][0]['mod']=True`, "differs from the qualified builtin profile"},
		{"extra observer", `body['plugins'].append({'name':'observer','path':'/tmp/observer','source':'observer@fixture'})`, "differs from the qualified builtin profile"},
		{"reload errors", `body['error_count']=1`, "missing, malformed, held or has errors"},
		{"missing error count", `body.pop('error_count')`, "missing, malformed, held or has errors"},
		{"null error count", `body['error_count']=None`, "missing, malformed, held or has errors"},
		{"negative error count", `body['error_count']=-1`, "missing, malformed, held or has errors"},
		{"held reload", `body['held']=True`, "missing, malformed, held or has errors"},
		{"unknown MCP state", `body.pop('mcpServers')`, "requires an observed empty native MCP server set"},
		{"plugin MCP", `body['mcpServers']=[{'name':'plugin:observer:server'}]`, "requires an observed empty native MCP server set"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			fake := strings.Replace(claudeFake, "body="+baseline, "body="+baseline+"\n   "+test.mutate, 1)
			if fake == claudeFake {
				t.Fatal("mod inventory mutation did not apply")
			}
			if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := b.Start(ctx, launch, testAuthorization(t))
			wantRefusal(t, err, test.want)
		})
	}
	t.Run("observed builtin profile", func(t *testing.T) {
		b, launch, ctx := claudeFixture(t)
		if _, err := b.Start(ctx, launch, testAuthorization(t)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestClaudeEffectiveSettingsRecheckedAfterPluginReload(t *testing.T) {
	for _, test := range []struct{ mutation, want string }{
		{`body['effective']['disableAllHooks']=True`, "Claude hook compatibility refuses disableAllHooks"},
		{`body['effective']['enabledPlugins']={'override@fixture':True}`, "refuses enabled plugins or mods"},
		{`body['errors']=[{'message':'managed settings were skipped'}]`, "Claude effective settings contain skipped or unknown configuration errors"},
	} {
		mutation := test.mutation
		t.Run(mutation, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			fake := strings.Replace(claudeFake, "active=None", "active=None\nreloaded=False", 1)
			fake = strings.Replace(fake, "if m['request']['subtype']=='reload_plugins':", "if m['request']['subtype']=='reload_plugins':\n   reloaded=True", 1)
			needle := "body={'effective':json.load(open(sys.argv[sys.argv.index('--settings')+1]))}"
			fake = strings.Replace(fake, needle, needle+"\n   if reloaded: "+mutation, 1)
			if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := b.Start(ctx, launch, testAuthorization(t))
			wantRefusal(t, err, test.want)
		})
	}
}

// A safe supplied file can be overridden by managed settings. The native
// pre-input get_settings reply must pass the same bounded gate contract; the
// caller's Mod profile cannot stand in for this configuration check.
func TestClaudeEffectiveSettingsBeforeInput(t *testing.T) {
	for _, test := range []struct {
		name        string
		body        string
		inheritGate bool
		want        string
	}{
		{"missing effective", `{}`, false, "response is missing or malformed"},
		{"null effective", `{"effective":null}`, false, "Claude hook settings must be one JSON object"},
		{"missing gate", `{"effective":{}}`, false, "Claude launch requires configured PreToolUse gate hooks"},
		{"hooks disabled", `{"effective":{"disableAllHooks":true}}`, true, "refuses disableAllHooks"},
		{"only managed hooks", `{"effective":{"allowManagedHooksOnly":true}}`, true, "refuses allowManagedHooksOnly"},
		{"enabled plugin", `{"effective":{"enabledPlugins":{"override@example":true}}}`, true, "refuses enabled plugins or mods"},
		{"unqualified module order", `{"effective":{"prependPlugins":["override"]}}`, true, `unsupported Claude adapter settings key "prependPlugins"`},
		{"wrong gate", `{"effective":{"hooks":{"PreToolUse":[{"matcher":".*","hooks":[{"type":"command","command":"/usr/bin/true"}]}]}}}`, false, "Claude PreToolUse command differs from the captured gate command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			c := b.(*claudeBackend)
			body := test.body
			if test.inheritGate {
				// Native managed suppression leaves flag hooks in effective.
				// Keep a valid gate so a missing gate cannot mask this guard.
				data, err := os.ReadFile(launch.HookConfig)
				if err != nil {
					t.Fatal(err)
				}
				var supplied map[string]json.RawMessage
				var response map[string]map[string]json.RawMessage
				if json.Unmarshal(data, &supplied) != nil || json.Unmarshal([]byte(body), &response) != nil {
					t.Fatal("invalid effective-settings fixture")
				}
				response["effective"]["hooks"] = supplied["hooks"]
				data, err = json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				body = string(data)
			}
			fake := strings.Replace(claudeFake, "body={'effective':json.load(open(sys.argv[sys.argv.index('--settings')+1]))}", "body=json.loads("+strconv.Quote(body)+")", 1)
			if err := os.WriteFile(c.executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := b.Start(ctx, launch, testAuthorization(t))
			wantRefusal(t, err, test.want)
		})
	}
	t.Run("native gate positive control", func(t *testing.T) {
		b, launch, ctx := claudeFixture(t)
		if _, err := b.Start(ctx, launch, testAuthorization(t)); err != nil {
			t.Fatalf("matching effective settings refused: %v", err)
		}
	})
}

func TestClaudeIdentityFramesAndUnknownCapability(t *testing.T) {
	c := newClaude("unused")
	c.nativeID, c.active = "native", "input"
	if err := c.frame(json.RawMessage(`{"type":"result","session_id":"successor","uuid":"x"}`)); err == nil {
		t.Fatal("result for another native identity accepted")
	}
	if err := c.frame(json.RawMessage(`{"type":"result","session_id":"native"}`)); err == nil {
		t.Fatal("result without native reference accepted")
	}
	if c.Capabilities()["resume"].Support != "unknown" || c.Capabilities()["attach"].Support != "unsupported" || c.Capabilities()["telemetry"].Support != "unknown" {
		t.Fatal("unsupported and unknown capability conflated")
	}
	if _, err := c.Resume(context.Background(), Launch{}, "native", testAuthorization(t)); err == nil {
		t.Fatal("resume accepted invalid native identity")
	}
	if err := c.permission(json.RawMessage(`12`), json.RawMessage(`{}`)); err == nil {
		t.Fatal("non-native approval ID accepted")
	}
	if err := c.permission(json.RawMessage(`"x"`), json.RawMessage(`{"subtype":"can_use_tool","tool_use_id":"tool","input":{},"agent_id":"child"}`)); err == nil {
		// No tool_name: malformed, so no correct native deny can be formed.
		// The well-formed subagent case is TestClaudeUnsupportedPermissionGetsNativeDeny.
		t.Fatal("malformed subagent request answered instead of closing admission")
	}
}

func TestClaudeUnrelatedAndDuplicateResults(t *testing.T) {
	c := newClaude("unused")
	c.nativeID, c.active, c.state = "native", "input", "busy"
	// A background task's result has its own native reference, but cannot
	// finish the outstanding adapter input merely because it arrived next.
	if err := c.frame(json.RawMessage(`{"type":"result","subtype":"success","session_id":"native","uuid":"background","origin":{"kind":"task-notification"}}`)); err != nil {
		t.Fatal(err)
	}
	e := <-c.events
	if e.TurnID != "" || e.ResultRef != "background" || c.active != "input" || c.state != "busy" {
		t.Fatalf("background result completed adapter input: %+v state=%s active=%s", e, c.state, c.active)
	}
	result := json.RawMessage(`{"type":"result","subtype":"success","session_id":"native","uuid":"our-result","terminal_reason":"completed","user_message_uuid":"input","user_message_uuids":["input"],"queued_turn_count":0}`)
	if err := c.frame(result); err != nil {
		t.Fatal(err)
	}
	e = <-c.events
	if e.TurnID != "input" || e.ResultStatus != "completed" || c.active != "" || c.state != "idle" {
		t.Fatalf("positive native result failed: %+v state=%s active=%s", e, c.state, c.active)
	}
	<-c.events // idle
	c.active, c.state = "next-input", "waiting_approval"
	if err := c.frame(result); err != nil {
		t.Fatal(err)
	}
	if c.active != "next-input" || c.state != "waiting_approval" {
		t.Fatal("duplicate result completed the next input")
	}
	// Replay duplicates are acceptance duplicates, not a fresh busy event that
	// could erase a pending approval observation.
	c.activeAck = true
	if err := c.frame(json.RawMessage(`{"type":"user","session_id":"native","uuid":"next-input","isReplay":true}`)); err != nil {
		t.Fatal(err)
	}
	if c.state != "waiting_approval" {
		t.Fatal("duplicate replay erased pending approval")
	}
}

// Native origin is absent on the qualified stream. Neither a plausible
// origin nor just one of the two input UUID fields may finish another turn.
// Queued or unknown following work must not manufacture an idle observation.
func TestClaudeResultRequiresExactInputAndEmptyQueue(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"old input", func(m map[string]any) { m["user_message_uuid"] = "old"; m["user_message_uuids"] = []string{"old"} }},
		{"missing single", func(m map[string]any) { delete(m, "user_message_uuid") }},
		{"wrong single", func(m map[string]any) { m["user_message_uuid"] = "old" }},
		{"missing list", func(m map[string]any) { delete(m, "user_message_uuids") }},
		{"wrong list", func(m map[string]any) { m["user_message_uuids"] = []string{"old"} }},
		{"coalesced inputs", func(m map[string]any) { m["user_message_uuids"] = []string{"input", "other"} }},
		{"unknown queue", func(m map[string]any) { delete(m, "queued_turn_count") }},
		{"queued native turn", func(m map[string]any) { m["queued_turn_count"] = 1 }},
		{"invalid queue", func(m map[string]any) { m["queued_turn_count"] = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID, c.active, c.state = "native", "input", "busy"
			m := map[string]any{"type": "result", "subtype": "success", "session_id": "native", "uuid": "result",
				"origin":            map[string]string{"kind": "peer", "from": "buddy-adapter"},
				"user_message_uuid": "input", "user_message_uuids": []string{"input"}, "queued_turn_count": 0}
			test.change(m)
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.frame(raw); err != nil {
				t.Fatal(err)
			}
			e := <-c.events
			if e.Kind != "result" || e.TurnID != "" || c.active != "input" || c.state != "busy" || len(c.events) != 0 {
				t.Fatalf("uncorrelated/queued result completed input: %+v state=%s active=%s", e, c.state, c.active)
			}
		})
	}
}

func TestClaudeHookConfigRefusals(t *testing.T) {
	command := "/tmp/buddy-adapter hook --receipt /tmp/receipt --buddy /tmp/buddy"
	config := func(t *testing.T, mutate func(map[string]any)) string {
		t.Helper()
		settings := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Edit|Write|NotebookEdit", "hooks": []any{map[string]any{"type": "command", "command": command}}}}}}
		if mutate != nil {
			mutate(settings)
		}
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	hook := func(settings map[string]any) map[string]any {
		return settings["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	}
	if err := claudeHookCompatibility(config(t, nil), command); err != nil {
		t.Fatalf("positive gate config rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"disable hooks", func(s map[string]any) { s["disableAllHooks"] = true }},
		{"managed hooks only", func(s map[string]any) { s["allowManagedHooksOnly"] = true }},
		{"enabled mod", func(s map[string]any) { s["enabledPlugins"] = map[string]bool{"mod@market": true} }},
		{"environment overrides", func(s map[string]any) { s["env"] = map[string]string{"CLAUDE_CODE_SAFE_MODE": "1"} }},
		{"lifecycle hook", func(s map[string]any) { s["hooks"].(map[string]any)["SessionStart"] = []any{} }},
		{"missing matcher coverage", func(s map[string]any) {
			s["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)["matcher"] = "^Write$"
		}},
		{"async gate", func(s map[string]any) { hook(s)["async"] = true }},
		{"one-shot gate", func(s map[string]any) { hook(s)["once"] = true }},
		{"echoed gate", func(s map[string]any) { hook(s)["command"] = "echo " + command }},
		{"skipped gate", func(s map[string]any) { hook(s)["command"] = command + " || true" }},
		{"other receipt", func(s map[string]any) {
			hook(s)["command"] = strings.Replace(command, "/tmp/receipt", "/tmp/successor", 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := claudeHookCompatibility(config(t, test.change), command); err == nil {
				t.Fatal("unsafe/incompatible config accepted")
			}
		})
	}
	if _, err := claudeHookWords("'/private/tmp/a directory/buddy-adapter' hook --receipt '/private/tmp/a receipt' --buddy /tmp/buddy"); err != nil {
		t.Fatalf("literal single quoting rejected: %v", err)
	}
	for _, unsafe := range []string{command + "; true", command + " &", "$(echo /tmp/buddy-adapter) hook --receipt /tmp/r --buddy /tmp/b", "\"$HOME/buddy-adapter\" hook --receipt /tmp/r --buddy /tmp/b"} {
		if _, err := claudeHookWords(unsafe); err == nil {
			t.Fatalf("shell execution accepted as literal hook: %q", unsafe)
		}
	}
}

func TestClaudeApprovalCancellation(t *testing.T) {
	c := newClaude("unused")
	c.nativeID, c.active, c.state = "native", "input", "busy"
	if err := c.permission(json.RawMessage(`"native-approval"`), json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"native-tool","input":{}}`)); err != nil {
		t.Fatal(err)
	}
	<-c.events
	if c.state != "waiting_approval" {
		t.Fatal("approval did not park")
	}
	if err := c.frame(json.RawMessage(`{"type":"control_cancel_request","request_id":"native-approval"}`)); err != nil {
		t.Fatal(err)
	}
	e := <-c.events
	if e.Kind != "approval_resolved" || e.ResultStatus != "cancelled" || e.Approval == nil || string(e.Approval.ID) != `"native-approval"` || e.TurnID != "input" {
		t.Fatalf("native cancellation identifiers lost: %+v", e)
	}
	if len(c.approvals) != 0 || c.state != "busy" {
		t.Fatal("cancelled approval remained pending")
	}
}

type claudeFakeProcess struct {
	write func(context.Context, any) error
	close func() error
	lines <-chan json.RawMessage
}

func (p *claudeFakeProcess) Write(v any) error                             { return p.write(context.Background(), v) }
func (p *claudeFakeProcess) WriteContext(ctx context.Context, v any) error { return p.write(ctx, v) }
func (p *claudeFakeProcess) Read() <-chan json.RawMessage                  { return p.lines }
func (p *claudeFakeProcess) Close() error {
	if p.close != nil {
		return p.close()
	}
	return nil
}
func claudeWireObject(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func claudePending(t *testing.T, c *claudeBackend, id, tool string, interaction bool) error {
	t.Helper()
	b, err := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": tool, "tool_use_id": "tool", "input": map[string]any{}, "requires_user_interaction": interaction})
	if err != nil {
		t.Fatal(err)
	}
	return c.permission(json.RawMessage(strconv.Quote(id)), b)
}

func TestClaudeReviewApprovalIdentityTombstones(t *testing.T) {
	for _, resolved := range []string{"pending", "approved", "cancelled"} {
		for _, change := range []string{"tool name", "interaction requirement"} {
			t.Run(resolved+"/"+change, func(t *testing.T) {
				c := newClaude("unused")
				c.nativeID, c.active = "native", "turn"
				c.proc = &claudeFakeProcess{write: func(context.Context, any) error { return nil }}
				if err := claudePending(t, c, "request", "Write", false); err != nil {
					t.Fatal(err)
				}
				d := ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"}
				if resolved == "approved" {
					if err := c.Approve(context.Background(), d); err != nil {
						t.Fatal(err)
					}
				}
				if resolved == "cancelled" {
					if err := c.frame(json.RawMessage(`{"type":"control_cancel_request","request_id":"request"}`)); err != nil {
						t.Fatal(err)
					}
				}
				tool, interaction := "Write", false
				if change == "tool name" {
					tool = "Read"
				} else {
					interaction = true
				}
				if err := claudePending(t, c, "request", tool, interaction); err == nil {
					t.Fatal("native approval ID rebound to a different request")
				}
			})
		}
	}
	t.Run("resolved identical replay stays terminal", func(t *testing.T) {
		c := newClaude("unused")
		c.nativeID, c.active = "native", "turn"
		c.proc = &claudeFakeProcess{write: func(context.Context, any) error { return nil }}
		if err := claudePending(t, c, "request", "Write", false); err != nil {
			t.Fatal(err)
		}
		d := ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"}
		if err := c.Approve(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		if err := claudePending(t, c, "request", "Write", false); err != nil {
			t.Fatal(err)
		}
		if err := c.Approve(context.Background(), d); err == nil {
			t.Fatal("delayed decision approved resolved request twice")
		}
	})
}

func TestClaudeReviewCancelledCallsHaveNoEffects(t *testing.T) {
	for _, op := range []string{"send", "interrupt", "approve"} {
		t.Run(op, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID = "native"
			writes := 0
			c.proc = &claudeFakeProcess{write: func(context.Context, any) error { writes++; return nil }}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			switch op {
			case "send":
				_, err = c.Send(ctx, "native", "peer hint")
			case "interrupt":
				c.active = "turn"
				err = c.Interrupt(ctx, "native", "turn")
			case "approve":
				c.active = "turn"
				if e := claudePending(t, c, "request", "Write", false); e != nil {
					t.Fatal(e)
				}
				err = c.Approve(ctx, ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"})
			}
			if err == nil || writes != 0 {
				t.Fatalf("cancelled %s wrote %d frames, err=%v", op, writes, err)
			}
			if op == "send" && c.active != "" {
				t.Fatal("cancelled send reserved a new active input")
			}
		})
	}
}

func TestClaudeReviewInterruptSerializesNextAdmission(t *testing.T) {
	c := newClaude("unused")
	c.nativeID, c.active = "native", "A"
	interruptEntered, releaseInterrupt, userWritten := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	c.proc = &claudeFakeProcess{write: func(ctx context.Context, v any) error {
		m := claudeWireObject(t, v)
		if m["type"] == "control_request" {
			close(interruptEntered)
			select {
			case <-releaseInterrupt:
			case <-ctx.Done():
				return ctx.Err()
			}
			b, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m["request_id"]}})
			return c.frame(b)
		}
		userWritten <- struct{}{}
		m["isReplay"] = true
		b, _ := json.Marshal(m)
		return c.frame(b)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), liveBound)
	defer cancel()
	interrupted := make(chan error, 1)
	go func() { interrupted <- c.Interrupt(ctx, "native", "A") }()
	select {
	case <-interruptEntered:
	case <-ctx.Done():
		t.Fatal("interrupt did not enter transport")
	}
	if err := c.frame(json.RawMessage(`{"type":"result","subtype":"success","session_id":"native","uuid":"result-A","user_message_uuid":"A","user_message_uuids":["A"],"queued_turn_count":0}`)); err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() { _, err := c.Send(ctx, "native", "B"); sent <- err }()
	early := false
	select {
	case <-userWritten:
		early = true
	case <-time.After(60 * time.Millisecond):
	}
	close(releaseInterrupt)
	if err := <-interrupted; err != nil {
		t.Fatal(err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if early {
		t.Fatal("next input reached native transport before the previous turn's ID-less interrupt was acknowledged")
	}
}

func TestClaudeReviewBlockedApprovalCanCancelAndClose(t *testing.T) {
	c := newClaude("unused")
	c.nativeID, c.active = "native", "turn"
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c.proc = &claudeFakeProcess{write: func(ctx context.Context, _ any) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-unblock:
			return nil
		}
	}, close: func() error { once.Do(func() { close(unblock) }); return nil }}
	if err := claudePending(t, c, "request", "Write", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	approved := make(chan error, 1)
	go func() {
		approved <- c.Approve(ctx, ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"})
	}()
	// Approve checks its context before it reaches the write, and the 30 ms
	// deadline is the stimulus this test cancels with, so it is not liveBound.
	// A starved goroutine reaching Approve after that deadline returns
	// ErrNotSubmitted without ever writing; a bare <-entered then waited for
	// an entry that never comes, until go test's 10 m panic (#91). The write's
	// entry is the precondition, so its absence fails here as itself. The
	// cancel-after-entry alternative would remove that load red but swap the
	// deadline for a cancel, and the real transport reads the deadline
	// (process.go's WriteContext); the 30 ms deadline stays the stimulus.
	// A descheduled test goroutine can find both ready (the write entered, then
	// honoured its deadline), and select picks either: entered is checked again
	// before failing, and the result goes back for the promptness wait below.
	select {
	case <-entered:
	case err := <-approved:
		select {
		case <-entered:
			approved <- err
		default:
			t.Fatalf("Approve returned before entering its native write, so the blocked-write path never ran (a deadline that expired first, under load, does this): %v", err)
		}
	case <-time.After(liveBound):
		t.Fatal("waiting for Approve to enter its native write")
	}
	returned := false
	select {
	case <-approved:
		returned = true
	case <-time.After(100 * time.Millisecond):
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	closedPromptly := false
	select {
	case <-closed:
		closedPromptly = true
	case <-time.After(100 * time.Millisecond):
	}
	// Free the old implementation as well, so a regression fails cleanly
	// instead of wedging the whole test binary and hiding the actual finding.
	// The drains are bounded too: a regression that blocks on something other
	// than unblock would otherwise wedge here instead of reporting below.
	once.Do(func() { close(unblock) })
	if !returned {
		select {
		case <-approved:
		case <-time.After(liveBound):
			t.Fatalf("blocked approval prevented context cancellation, and Approve still had not returned after the write was unblocked")
		}
	}
	if !closedPromptly {
		select {
		case <-closed:
		case <-time.After(liveBound):
			t.Fatalf("blocked approval prevented Close, and Close still had not returned after the write was unblocked")
		}
	}
	if !returned || !closedPromptly {
		t.Fatalf("blocked approval prevented context cancellation=%v or Close=%v", !returned, !closedPromptly)
	}
	if _, ok := c.approvals["request"]; ok {
		t.Fatal("uncertain approval write became retryable")
	}
}

func TestClaudeReviewTerminalAdmissionBeforeNotification(t *testing.T) {
	for _, kind := range []string{"eof", "foreign frame", "approval collision"} {
		t.Run(kind, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID, c.active = "native", "turn"
			frames := make(chan json.RawMessage, 1)
			closed := make(chan struct{})
			var once sync.Once
			var writes atomic.Int32
			c.proc = &claudeFakeProcess{lines: frames, write: func(context.Context, any) error { writes.Add(1); return nil }, close: func() error { once.Do(func() { close(closed) }); return nil }}
			defer c.Close()
			if err := claudePending(t, c, "request", "Write", false); err != nil {
				t.Fatal(err)
			}
			for len(c.events) < cap(c.events) {
				c.events <- Event{Kind: "backpressure"}
			}
			switch kind {
			case "eof":
				close(frames)
			case "foreign frame":
				frames <- json.RawMessage(`{"type":"result","session_id":"other-worker","uuid":"foreign"}`)
			case "approval collision":
				frames <- json.RawMessage(`{"type":"control_request","request_id":"request","request":{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"tool","input":{},"requires_user_interaction":true}}`)
			}
			readerDone := make(chan struct{})
			go func() { c.read(); close(readerDone) }()
			closedBeforeNotice := false
			select {
			case <-closed:
				closedBeforeNotice = true
			case <-time.After(100 * time.Millisecond):
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			approvalErr := c.Approve(ctx, ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"})
			c.mu.Lock()
			c.active = ""
			pending := len(c.approvals)
			admissionClosed := c.closed
			c.mu.Unlock()
			_, sendErr := c.Send(ctx, "native", "peer hint after terminal stream")
			_ = c.Close()
			select {
			case <-readerDone:
			case <-time.After(time.Second):
				t.Fatal("Close could not release terminal event backpressure")
			}
			if !closedBeforeNotice || !admissionClosed || pending != 0 || approvalErr == nil || sendErr == nil || writes.Load() != 0 {
				t.Fatalf("terminal %s retained control admission: transport_closed=%v admission_closed=%v pending=%d approval_err=%v send_err=%v native_writes=%d", kind, closedBeforeNotice, admissionClosed, pending, approvalErr, sendErr, writes.Load())
			}
		})
	}
}

func TestClaudeBackpressureProcessHelper(t *testing.T) {
	if os.Getenv("BUDDY_CLAUDE_BACKPRESSURE_HELPER") != "1" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "user", "isReplay": true, "session_id": "native", "uuid": "turn"})
	dec := json.NewDecoder(os.Stdin)
	var closeOutput any
	if dec.Decode(&closeOutput) != nil {
		os.Exit(2)
	}
	// The child keeps reading approval responses after losing stdout. Only
	// the transport reader can observe this EOF while the backend is blocked.
	_ = os.Stdout.Close()
	var approval any
	if dec.Decode(&approval) == nil {
		_ = os.WriteFile(os.Getenv("BUDDY_CLAUDE_BACKPRESSURE_EFFECT"), []byte("approval received"), 0600)
	}
	os.Exit(0)
}

func TestClaudeReviewOrdinaryEventBackpressureCannotHideEOF(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	effect := filepath.Join(dir, "approval-effect")
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), exe, []string{"-test.run=^TestClaudeBackpressureProcessHelper$"}, dir,
		[]string{"BUDDY_CLAUDE_BACKPRESSURE_HELPER=1", "BUDDY_CLAUDE_BACKPRESSURE_EFFECT=" + effect})
	if err != nil {
		t.Fatal(err)
	}
	c := newClaude("unused")
	c.nativeID, c.active, c.proc = "native", "turn", p
	defer c.Close()
	if err := claudePending(t, c, "request", "Write", false); err != nil {
		t.Fatal(err)
	}
	for len(c.events) < cap(c.events) {
		c.events <- Event{Kind: "backpressure"}
	}
	readerDone := make(chan struct{})
	go func() { c.read(); close(readerDone) }()
	deadline := time.Now().Add(liveBound)
	for {
		c.mu.Lock()
		blockedOnReplay := c.activeAck
		c.mu.Unlock()
		if blockedOnReplay {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ordinary native replay did not reach the full event queue")
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.Write(map[string]string{"fixture_control": "close stdout"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.stop:
	case <-time.After(liveBound):
		t.Error("ordinary event backpressure hid native EOF from write admission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = c.Approve(ctx, ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"})
	// The closed transport's refusal, not the 1 s context's (Codex, #90).
	if err == nil || !strings.Contains(err.Error(), "native transport closed") {
		t.Errorf("pending approval reached native input after stdout EOF: %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(liveBound):
		t.Error("native output EOF left the owned child alive")
	}
	if _, err := os.Stat(effect); !os.IsNotExist(err) {
		t.Errorf("native child received approval after stdout EOF: %v", err)
	}
	_ = c.Close()
	select {
	case <-readerDone:
	case <-time.After(liveBound):
		t.Fatal("Close did not release ordinary event backpressure")
	}
}

// F-07: a well-formed request this adapter cannot attribute to the captured
// worker's active input (a subagent's, or one with no active input) used to
// tear the whole transport down, and every teardown ended the worker. It now
// gets the protocol's native deny and the worker stays. Malformed or reused
// ids still close admission.
func TestClaudeUnsupportedPermissionGetsNativeDeny(t *testing.T) {
	request := func(agent string) json.RawMessage {
		m := map[string]any{"subtype": "can_use_tool", "tool_name": "Write", "tool_use_id": "tool-9", "input": map[string]any{"file_path": "x"}}
		if agent != "" {
			m["agent_id"] = agent
		}
		b, _ := json.Marshal(m)
		return b
	}
	for _, name := range []string{"subagent", "no-active-input", "top-level-positive-control"} {
		t.Run(name, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID = "native"
			var writes []map[string]any
			c.proc = &claudeFakeProcess{write: func(_ context.Context, v any) error { writes = append(writes, claudeWireObject(t, v)); return nil }}
			agent := ""
			switch name {
			case "subagent":
				c.active, agent = "turn", "child-agent"
			case "top-level-positive-control":
				c.active = "turn"
			}
			if err := c.permission(json.RawMessage(`"req-9"`), request(agent)); err != nil {
				t.Fatalf("well-formed request tore the transport down: %v", err)
			}
			if name == "top-level-positive-control" {
				if len(writes) != 0 || len(c.approvals) != 1 || len(c.events) != 1 {
					t.Fatalf("control: an attributable request must wait for the operator: writes=%d", len(writes))
				}
				return
			}
			if len(writes) != 1 || c.closed || len(c.approvals) != 0 || len(c.events) != 0 {
				t.Fatalf("unsupported request: writes=%d closed=%v approvals=%d events=%d", len(writes), c.closed, len(c.approvals), len(c.events))
			}
			resp := writes[0]["response"].(map[string]any)
			inner := resp["response"].(map[string]any)
			if writes[0]["type"] != "control_response" || resp["subtype"] != "success" || resp["request_id"] != "req-9" || inner["behavior"] != "deny" || inner["toolUseID"] != "tool-9" {
				t.Fatalf("not the native deny shape: %v", writes[0])
			}
			// A replay of the same request is ignored, not answered twice.
			if err := c.permission(json.RawMessage(`"req-9"`), request(agent)); err != nil || len(writes) != 1 {
				t.Fatalf("replayed request: err=%v writes=%d", err, len(writes))
			}
			// The id stays tombstoned: reused for different work, it closes admission.
			other := strings.Replace(string(request(agent)), `"x"`, `"y"`, 1)
			if err := c.permission(json.RawMessage(`"req-9"`), json.RawMessage(other)); err == nil || !c.closed {
				t.Fatal("a reused request id for different work did not close admission")
			}
		})
	}
	for _, malformed := range []struct{ id, raw string }{
		{`"req"`, `{"subtype":"can_use_tool","tool_name":"Write","input":{},"agent_id":"child"}`},
		{`"req"`, `{"subtype":"can_use_tool","tool_use_id":"t","input":{},"agent_id":"child"}`},
		{`"req"`, `{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"t","input":[]}`},
		{`12`, `{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"t","input":{}}`},
	} {
		c := newClaude("unused")
		c.nativeID = "native"
		writes := 0
		c.proc = &claudeFakeProcess{write: func(context.Context, any) error { writes++; return nil }}
		if err := c.permission(json.RawMessage(malformed.id), json.RawMessage(malformed.raw)); err == nil || writes != 0 {
			t.Fatalf("malformed request answered with a guess: %s %s (writes %d)", malformed.id, malformed.raw, writes)
		}
	}
}
