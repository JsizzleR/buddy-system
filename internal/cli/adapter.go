package cli

// Versioned machine data for harness adapters. Human receipts are not parsed,
// and no operation guesses an identity from cwd or recaptures an incarnation.
// Identifiers remain exact JSON strings; peer text rendered into context is
// fenced once, separately from those routing keys. This interface never turns
// a peer message, delivery receipt, READY SHA, or run outcome into authority.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const usageAdapter = "usage: buddy adapter inspect --session <id> --incarnation <captured-token>\n" +
	"       buddy adapter queue --session <sender-id> --incarnation <sender-token> --to <recipient-id> --to-incarnation <recipient-token> --body <text>\n" +
	"       buddy adapter inbox --session <id> --incarnation <captured-token> [--message <id>]\n" +
	"       buddy adapter delivered --session <id> --incarnation <captured-token> --message <id>\n" +
	"       buddy adapter endpoint --session <id> --incarnation <captured-token> [--pid <receiver-pid> --born <receiver-start>]\n" +
	"       JSON: buddy.adapter.v1; inbox fetches only adapter messages and does not mark delivery; endpoint requires matching native Claude session evidence and captures the receiving environment"

func cmdAdapter(args []string, env Env) error {
	if len(args) == 0 {
		return errors.New(usageAdapter)
	}
	op := args[0]
	switch op {
	case "inspect", "queue", "inbox", "delivered", "endpoint":
	default:
		return errors.New(usageAdapter)
	}
	fs := flag.NewFlagSet("adapter "+op, flag.ContinueOnError)
	var identity, recipient store.AdapterIdentity
	var body string
	var id, born int64
	var pid int
	fs.StringVar(&identity.SessionID, "session", "", "captured native session id")
	fs.StringVar(&identity.Incarnation, "incarnation", "", "captured lifecycle token")
	if op == "queue" {
		fs.StringVar(&recipient.SessionID, "to", "", "captured recipient session id")
		fs.StringVar(&recipient.Incarnation, "to-incarnation", "", "captured recipient lifecycle token")
		fs.StringVar(&body, "body", "", "peer message body (never operator authority)")
	}
	if op == "delivered" || op == "inbox" {
		fs.Int64Var(&id, "message", 0, "exact adapter message id")
	}
	if op == "endpoint" {
		fs.IntVar(&pid, "pid", 0, "optional expected receiving process pid")
		fs.Int64Var(&born, "born", 0, "optional expected receiving process start")
	}
	if help, err := parseFlags(fs, args[1:], usageAdapter, env); help || err != nil {
		return err
	}
	if err := noStray("adapter "+op, fs, usageAdapter); err != nil {
		return err
	}
	if strings.TrimSpace(identity.SessionID) == "" || strings.TrimSpace(identity.Incarnation) == "" {
		return errors.New("adapter requires --session and --incarnation from a captured lifecycle receipt")
	}
	messageRequested := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "message" {
			messageRequested = true
		}
	})
	if messageRequested && id <= 0 {
		return errors.New("adapter --message must be a positive message id")
	}
	var proc store.ProcRef
	var socket string
	if op == "endpoint" {
		if harness := env.getenv(EnvHarness); harness != "" && harness != "claude" {
			return errors.New("adapter endpoint requires the receiving Claude harness")
		}
		// A Claude ancestor identifies the process, not the logical session it
		// is currently serving. Requiring receiving-session evidence closes the
		// path where worker A supplied worker B's valid captured receipt while
		// this export was absent. The hook wrapper forwards its payload's native
		// id only after matching that payload against its immutable receipt.
		if native := env.getenv(EnvClaudeSession); native == "" || native != identity.SessionID {
			return errors.New("adapter endpoint requires matching receiving native Claude session evidence")
		}
		anchor, ok := env.anchor()
		if !ok || anchor.Ref.PID <= 1 || anchor.Ref.Born <= 0 {
			return errors.New("adapter endpoint requires a verified receiving Claude process and birth time")
		}
		if (pid != 0 || born != 0) && (pid != anchor.Ref.PID || born != anchor.Ref.Born) {
			return errors.New("adapter endpoint process differs from the receiving Claude process")
		}
		proc, socket = anchor.Ref, env.getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	}
	if op == "queue" {
		// Bound the eventual context representation without silently cutting a
		// message. The raw body stays durable; inbox renders it once.
		if rendered := fence.Line(body, 1<<20); strings.TrimSpace(rendered) == "" || len(rendered) > store.AdapterBodyLimit {
			return errors.New("adapter body must show once fenced and fit within 4096 rendered bytes")
		}
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	enc := json.NewEncoder(env.Stdout)
	switch op {
	case "inspect":
		inspection, err := st.InspectAdapter(identity)
		if err != nil {
			return fencedErr(err)
		}
		inspection.Label = fence.Line(inspection.Label, 128)
		for i := range inspection.RunOutcomes {
			inspection.RunOutcomes[i].Slug = fence.Line(inspection.RunOutcomes[i].Slug, 128)
		}
		return enc.Encode(struct {
			Schema    string `json:"schema"`
			Operation string `json:"operation"`
			store.AdapterInspection
		}{store.AdapterSchema, op, inspection})
	case "queue":
		delivery, err := st.QueueAdapter(identity, recipient, body)
		if err != nil {
			return fencedErr(err)
		}
		return enc.Encode(map[string]any{"schema": store.AdapterSchema, "operation": op, "delivery": delivery})
	case "inbox":
		var messages store.AdapterMessages
		var err error
		if messageRequested {
			messages, err = st.AdapterInboxMessage(identity, id)
		} else {
			messages, err = st.AdapterInbox(identity)
		}
		if err != nil {
			return fencedErr(err)
		}
		for i := range messages.Items {
			messages.Items[i].Body = fence.Line(messages.Items[i].Body, store.AdapterBodyLimit)
		}
		return enc.Encode(map[string]any{"schema": store.AdapterSchema, "operation": op, "identity": identity, "messages": messages})
	case "delivered":
		delivery, err := st.DeliverAdapter(identity, id)
		if err != nil {
			return fencedErr(err)
		}
		return enc.Encode(map[string]any{"schema": store.AdapterSchema, "operation": op, "delivery": delivery})
	case "endpoint":
		endpoint, err := st.RegisterAdapterEndpoint(identity, proc, socket)
		if err != nil {
			return fencedErr(err)
		}
		return enc.Encode(map[string]any{"schema": store.AdapterSchema, "operation": op, "identity": identity, "endpoint": endpoint})
	}
	return fmt.Errorf("unsupported adapter operation")
}
