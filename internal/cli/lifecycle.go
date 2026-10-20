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
//
// `renew` is the one event that is not lifecycle (D-078): buddy-adapter's
// controller sends it when a native completion shows a write of named paths
// finished. It renews the captured incarnation's claims covering those paths
// and touches nothing else, because its batches are drained off the native
// reader and may land after the turn's idle. Its paths are the NATIVE
// spellings (absolute, or relative to the cwd) and are placed the way the gate
// adjudicates them (gateTargets, D-073): a claim covering any spelling a write
// could have reached is renewed, and a path no spelling of which is inside
// this repo renews nothing. A `beat --path` is repo-relative, as before.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const usageLifecycle = "usage: buddy lifecycle begin --session <id> [--harness codex|claude] [--label <text>] [--previous <ended-incarnation>]\n" +
	"       buddy lifecycle refresh --session <id> --incarnation <captured-token> [--harness codex|claude]\n" +
	"       buddy lifecycle beat --session <id> --incarnation <captured-token> [--harness codex|claude] [--path <repo-relative-path>]...\n" +
	"       buddy lifecycle renew --session <id> --incarnation <captured-token> [--harness codex|claude] --path <observed-path>...\n" +
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
	// Renew only: how many claims it renewed, and the repo-relative spellings
	// it placed the observed paths at (none for a path outside this repo).
	RenewedClaims *int     `json:"renewed_claims,omitempty"`
	PlacedPaths   []string `json:"placed_paths,omitempty"`
	// Renew only: observed paths one of whose spellings could not be
	// resolved, so their renewal may be incomplete.
	UnresolvedPaths []string `json:"unresolved_paths,omitempty"`
}

// maxRenewPaths is the patch parser's own path bound, so one renewal can carry
// every path one apply_patch may name.
const maxRenewPaths = 128

// placeObserved maps native path spellings onto this repo the way the gate
// does (gateTargets: lexical and physical, the entry and its target, a dangling
// link's would-be target), keeping each in-repo spelling once, folded. A path
// whose spellings are all outside the repo contributes nothing, and so does a
// spelling that failed to resolve: an unplaceable path renews nothing rather
// than something guessed. Such a path is returned in unresolved (Codex review
// of the code pass, finding 5: the error was discarded and the adapter
// journalled complete coverage), so the caller can say coverage is unknown.
func placeObserved(top, cwd string, paths []string) (out, unresolved []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		targets, err := gateTargets(cwd, p)
		if err != nil {
			unresolved = append(unresolved, p)
		}
		for _, target := range targets {
			rel, _, outside := placeCanonical(top, target)
			if outside || rel == "." || seen[rel] {
				continue
			}
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out, unresolved
}

func cmdLifecycle(args []string, env Env) error {
	if len(args) == 0 {
		return errors.New(usageLifecycle)
	}
	event := args[0]
	switch event {
	case "begin", "refresh", "beat", "renew", "busy", "idle", "end", "hook":
	default:
		return errors.New(usageLifecycle)
	}
	fs := flag.NewFlagSet("lifecycle "+event, flag.ContinueOnError)
	var session, label, previous, incarnation string
	var paths multiFlag
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
	// Repeatable (F-26). It was a plain string flag, so `--path a --path b`
	// kept only b, and the claim covering a went STALE under a worker writing
	// both (TestLifecycleBeatRenewsEveryRepeatedPath).
	switch event {
	case "beat":
		fs.Var(&paths, "path", "affected repo-relative path (repeatable)")
	case "renew":
		fs.Var(&paths, "path", "a path a completed native write named, absolute or cwd-relative (repeatable, at least one)")
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
	var rels []string
	if event == "beat" {
		for _, p := range paths {
			rel, err := store.NormalizeScope(p)
			if err != nil {
				return fencedErr(err)
			}
			rels = append(rels, rel)
		}
	}
	if event == "renew" && (len(paths) == 0 || len(paths) > maxRenewPaths) {
		// The adapter batches at most maxRenewPaths; zero would be a receipt
		// for a renewal of nothing, which a caller could mistake for coverage.
		return fmt.Errorf("lifecycle renew takes 1 to %d --path flags, got %d", maxRenewPaths, len(paths))
	}
	for _, p := range paths {
		if event == "renew" && p == "" {
			// The same mistake one path at a time (Codex review, finding 7).
			return errors.New("lifecycle renew refuses an empty --path")
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
	} else if event == "renew" {
		placed, unresolved := placeObserved(rc.top, env.Cwd, paths)
		n, err := st.RenewLogical(session, incarnation, placed...)
		if err != nil {
			return fencedErr(err)
		}
		// Placed paths are model-influenced text: each one is fenced to one
		// line before it leaves (invariant 9), capped at the path convention.
		fenced := make([]string, len(placed))
		for i, p := range placed {
			fenced[i] = fence.Line(p, 512)
		}
		for i, p := range unresolved {
			unresolved[i] = fence.Line(p, 512)
		}
		return json.NewEncoder(env.Stdout).Encode(lifecycleReceipt{
			Schema: "buddy.lifecycle.v1", Harness: harness, SessionID: session,
			Incarnation: incarnation, Event: event, State: "live", RenewedClaims: &n, PlacedPaths: fenced,
			UnresolvedPaths: unresolved,
		})
	} else if err := applyLifecycle(st, event, session, incarnation, top, rels, env); err != nil {
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
				// A relative path from a vanished cwd renews nothing (D-102).
				if !placeable(h.Cwd, p) {
					continue
				}
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
