package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// claudeWriteFake speaks the 2.1.291 stream-json frames recorded by the native
// qualification suites (receipts claude-interrupted-compaction, 2026-10-13):
// the main-thread assistant tool_use, the can_use_tool request naming the same
// tool_use_id, and after an allow, a non-replay user frame whose tool_result
// block OMITS is_error and whose tool_use_result is the structured Write
// witness {type, filePath}. A deny gets is_error:true and a string
// tool_use_result, as native does.
const claudeWriteFake = `#!/usr/bin/env python3
import json,sys
if '--version' in sys.argv:
 print('2.1.291 (Claude Code)'); sys.exit(0)
def emit(v):
 print(json.dumps(v),flush=True)
sid=sys.argv[sys.argv.index('--session-id')+1]
target=WORKTREE+'/claimed.txt'
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
 elif m['type']=='user':
  active=m
  replay=dict(m,isReplay=True); replay.pop('origin'); emit(replay)
  emit({'type':'assistant','message':{'content':[{'type':'tool_use','id':'toolu_w','name':'Write','input':{'file_path':target,'content':'x'}}]},'parent_tool_use_id':None,'session_id':sid,'uuid':'assistant-1'})
  emit({'type':'control_request','request_id':'perm-1','request':{'subtype':'can_use_tool','tool_name':'Write','input':{'file_path':target,'content':'x'},'tool_use_id':'toolu_w'}})
 elif m['type']=='control_response':
  r=m['response']['response']
  if r['behavior']=='allow':
   emit({'type':'user','message':{'role':'user','content':[{'tool_use_id':'toolu_w','type':'tool_result','content':'The file has been updated successfully.'}]},'parent_tool_use_id':None,'session_id':sid,'uuid':'tool-result-1','tool_use_result':{'type':'update','filePath':target,'content':'x'}})
  else:
   emit({'type':'user','message':{'role':'user','content':[{'type':'tool_result','content':'Denied','is_error':True,'tool_use_id':'toolu_w'}]},'parent_tool_use_id':None,'session_id':sid,'uuid':'tool-result-1','tool_use_result':'Error: Denied'})
  emit({'type':'result','subtype':'success','is_error':False,'terminal_reason':'completed','uuid':'result-1','session_id':sid,'user_message_uuid':active['uuid'],'user_message_uuids':[active['uuid']],'queued_turn_count':0})
`

// TestControllerRenewsTheCompletedClaudeWrite is the F-26 defect at the adapter:
// a worker launched by buddy-adapter completes a Write of its claimed file and
// nothing renews that claim, because the gate contract has no PostToolUse and
// the controller translated only busy/idle/end. The deny arm is the negative,
// the allow arm its positive control.
func TestControllerRenewsTheCompletedClaudeWrite(t *testing.T) {
	for _, decision := range []string{"allow", "deny"} {
		t.Run(decision, func(t *testing.T) {
			dir := t.TempDir()
			worktree := filepath.Join(dir, "work")
			if err := os.Mkdir(worktree, 0700); err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(dir, "claude")
			source := "#!/usr/bin/env python3\nWORKTREE=" + pyQuote(worktree) + "\n" + claudeWriteFake[len("#!/usr/bin/env python3\n"):]
			if err := os.WriteFile(executable, []byte(source), 0700); err != nil {
				t.Fatal(err)
			}
			gate := "/tmp/buddy-adapter hook --receipt /tmp/receipt --buddy /tmp/buddy"
			settings := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(settings, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write|NotebookEdit","hooks":[{"type":"command","command":"`+gate+`"}]}]}}`), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := NewClaude(executable)
			if err != nil {
				t.Fatal(err)
			}
			l := &fakeLedger{queued: true}
			c, err := NewController(b, l, filepath.Join(dir, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			launch := Launch{Worktree: worktree, NativeSessionID: "00000000-0000-4000-8000-000000000001", PermissionMode: "workspace-write",
				HarnessVersion: "2.1.291", HookConfig: settings, GateCommand: gate, Hooks: Compatibility{Mods: "builtin-only-2.1.291"}}
			if _, err := c.Start(ctx, "claude", launch, "worker"); err != nil {
				t.Fatal(err)
			}
			receipts := pumpEvents(ctx, b, c)
			if _, err := c.Handle(ctx, Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 1}); err != nil {
				t.Fatal(err)
			}
			approval := awaitReceipt(t, ctx, receipts, "approval")
			a := approval.NativeEvent.Approval
			if _, err := c.OperatorApprove(ctx, Request{Schema: Schema, Operation: "approve", Identity: l.identity,
				Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: decision}}); err != nil {
				t.Fatal(err)
			}
			awaitReceipt(t, ctx, receipts, "idle")
			got := l.renewed()
			if decision == "deny" {
				if len(got) != 0 {
					t.Fatalf("a denied Write renewed claims: %+v", got)
				}
				return
			}
			if len(got) != 1 || len(got[0].paths) != 1 || got[0].paths[0] != filepath.Join(worktree, "claimed.txt") || !got[0].identity.Same(l.identity) {
				t.Fatalf("the completed Write of a claimed file reached no renewal under the captured identity: %+v (ledger events %v)", got, l.events)
			}
		})
	}
}

// pumpEvents is buddy-adapter's observeNativeEvents: every native event goes
// through Observe, in order, and its receipt is handed to the test.
func pumpEvents(ctx context.Context, b Backend, c *Controller) <-chan Receipt {
	out := make(chan Receipt, 64)
	go func() {
		for e := range b.Events() {
			r, _ := c.Observe(ctx, e)
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func awaitReceipt(t *testing.T, ctx context.Context, receipts <-chan Receipt, kind string) Receipt {
	t.Helper()
	for {
		select {
		case r := <-receipts:
			if r.NativeEvent != nil && r.NativeEvent.Kind == kind {
				return r
			}
		case <-ctx.Done():
			t.Fatalf("waiting for %s receipt: %v", kind, ctx.Err())
		}
	}
}
