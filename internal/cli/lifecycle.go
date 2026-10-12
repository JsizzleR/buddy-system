package cli

// A harness adapter captures a Buddy incarnation before it starts delivering
// events. Native thread ids survive resume, and a shared app-server PID says
// nothing about which logical session ended. Looking up the current token in
// an event handler would simply give an old event permission to change its
// successor. This interface therefore requires the captured token on every
// event and never reads one on the caller's behalf.
//
// This is deliberately an observation/registration interface, not a worker
// launcher or an inbox transport. It neither turns completion into acceptance
// nor drains messages into output intended for the coordinator. The versioned
// receipt is for adapters; human-oriented hello output is not an API.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/store"
)

const usageLifecycle = "usage: buddy lifecycle begin --session <id> [--harness codex|claude] [--label <text>] [--previous <ended-incarnation>]\n" +
	"       buddy lifecycle refresh --session <id> --incarnation <captured-token> [--harness codex|claude]\n" +
	"       buddy lifecycle beat --session <id> --incarnation <captured-token> [--harness codex|claude] [--path <repo-relative-path>]\n" +
	"       buddy lifecycle busy --session <id> --incarnation <captured-token> [--harness codex|claude]\n" +
	"       buddy lifecycle idle --session <id> --incarnation <captured-token> [--harness codex|claude]\n" +
	"       buddy lifecycle end --session <id> --incarnation <captured-token> [--harness codex|claude]\n" +
	"       buddy lifecycle hook --incarnation <captured-token>   (native top-level Codex hook JSON on stdin)\n" +
	"       JSON receipts: buddy.lifecycle.v1; no launch, wake, inbox delivery or permission change"

type lifecycleReceipt struct {
	Schema      string `json:"schema"`
	Harness     string `json:"harness"`
	SessionID   string `json:"session_id"`
	Incarnation string `json:"incarnation"`
	Event       string `json:"event"`
	State       string `json:"state"`
}

func cmdLifecycle(args []string, env Env) error {
	if len(args) == 0 {
		return errors.New(usageLifecycle)
	}
	event := args[0]
	switch event {
	case "begin", "refresh", "beat", "busy", "idle", "end", "hook":
	default:
		return errors.New(usageLifecycle)
	}
	fs := flag.NewFlagSet("lifecycle "+event, flag.ContinueOnError)
	var session, label, previous, incarnation, rel string
	harness := "codex"
	if event != "hook" {
		fs.StringVar(&session, "session", "", "native session id (no directory inference)")
		fs.StringVar(&harness, "harness", "codex", "adapter harness (codex or claude)")
	}
	if event == "begin" {
		fs.StringVar(&label, "label", "", "stable session label")
		fs.StringVar(&previous, "previous", "", "expected ended incarnation for restart")
	} else {
		fs.StringVar(&incarnation, "incarnation", "", "token captured at registration")
	}
	if event == "beat" {
		fs.StringVar(&rel, "path", "", "affected repo-relative path")
	}
	if help, err := parseFlags(fs, args[1:], usageLifecycle, env); help || err != nil {
		return err
	}
	if err := noStray("lifecycle "+event, fs, usageLifecycle); err != nil {
		return err
	}
	if harness != "codex" && harness != "claude" {
		return errors.New("lifecycle --harness must be codex or claude")
	}
	if event != "begin" && incarnation == "" {
		return errors.New("lifecycle events require --incarnation captured at registration")
	}
	if event == "hook" {
		return lifecycleHook(incarnation, env)
	}
	if strings.TrimSpace(session) == "" {
		return errors.New("lifecycle requires an explicit --session native id")
	}
	if rel != "" {
		var err error
		rel, err = store.NormalizeScope(rel)
		if err != nil {
			return fencedErr(err)
		}
	}
	st, rc, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	top := rc.top
	if top == "" {
		top = env.Cwd
	}
	if event == "begin" {
		si, err := st.BeginLogical(session, label, top, previous)
		if err != nil {
			return fencedErr(err)
		}
		incarnation = si.Incarnation
	} else if err := applyLifecycle(st, event, session, incarnation, top, []string{rel}, env); err != nil {
		return fencedErr(err)
	}
	state := "live"
	if event == "end" {
		state = "ended"
	}
	// The receipt describes the operation that committed, not a second read of
	// whichever incarnation happens to be current after it. If writing fails,
	// registration may have committed: do not automatically retry begin.
	return json.NewEncoder(env.Stdout).Encode(lifecycleReceipt{
		Schema: "buddy.lifecycle.v1", Harness: harness, SessionID: session,
		Incarnation: incarnation, Event: event, State: state,
	})
}

func applyLifecycle(st *store.Store, event, session, incarnation, worktree string, rels []string, env Env) error {
	switch event {
	case "refresh":
		_, err := st.RefreshLogical(session, incarnation, "", worktree)
		return err
	case "beat":
		return st.BeatLogical(session, incarnation, rels...)
	case "busy":
		return st.BusyLogical(session, incarnation)
	case "idle":
		// Native hooks have no portable event timestamp. This is explicitly
		// the time Buddy observed Stop, not a reconstructed transcript time.
		return st.IdleLogical(session, incarnation, nowOf(env))
	case "end":
		return st.EndLogical(session, incarnation)
	}
	return errors.New("unsupported lifecycle event")
}

func lifecycleHook(incarnation string, env Env) error {
	// The verb itself is a Codex adapter, so classification does not depend
	// on an undocumented native environment export. Still reject a conflicting
	// marker or native id rather than silently reinterpreting a Claude event.
	if marker := env.getenv(EnvHarness); marker != "" && marker != "codex" {
		return errors.New("lifecycle hook requires the codex harness")
	}
	original := env
	env.Getenv = func(key string) string {
		if key == EnvHarness {
			return "codex"
		}
		return original.getenv(key)
	}
	h, err := readHook(env)
	if err != nil {
		return err
	}
	// Subagents can report their parent's session id. Independent subagent
	// identity has not been qualified and must never end the parent here.
	if h.AgentID != "" || h.ParentSessionID != "" || h.ParentThreadID != "" {
		return errors.New("lifecycle hook does not support subagent identities")
	}
	event := map[string]string{
		"SessionStart": "refresh", "UserPromptSubmit": "busy",
		"PostToolUse": "beat", "Stop": "idle", "SessionEnd": "end",
	}[h.HookEventName]
	if event == "" {
		return errors.New("lifecycle hook requires a supported hook_event_name")
	}
	st, rc, err := openRepo(h.Cwd, env)
	if errors.Is(err, errNoLedger) {
		_, err = fmt.Fprintln(env.Stdout, "{}")
		return err
	}
	if err != nil {
		return err
	}
	defer st.Close()
	// A PostToolUse names the paths its tool touched; those, and only those,
	// renew covering claims (review F-26). The same bounded extraction the gate
	// uses, so a patch renews every file it names. A payload whose paths cannot
	// be read renews nothing and still records the beat: this is an
	// observation, and a claim is never renewed on a guess.
	var rels []string
	if event == "beat" {
		if paths, err := gatePaths(h); err == nil {
			for _, p := range paths {
				if rel, _, outside := placeInRepo(rc.top, h.Cwd, p); !outside {
					rels = append(rels, rel)
				}
			}
		}
	}
	if err := applyLifecycle(st, event, h.SessionID, incarnation, rc.top, rels, env); err != nil {
		return fencedErr(err)
	}
	// Stop requires JSON, and a hook receipt is not an instruction to the
	// model. This adapter never asks a completed turn to keep running.
	_, err = fmt.Fprintln(env.Stdout, "{}")
	return err
}
