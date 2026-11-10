package cli

// Unlike adapter inspect, diagnose never opens Buddy's store. A successful
// report can contain incompatibility or uncertainty: exit zero means the report
// was produced, not that hooks enforce, a worker can launch, or work may land.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/harness"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const usageDiagnose = "usage: buddy diagnose [--harness codex|claude] [--launch <absolute-json-path> | --receipt <absolute-saved-receipt>] [--evidence <absolute-json-path>] [--codex-hooks <absolute-hooks-json>]\n" +
	"       Offline JSON compatibility report; no migration, session registration, inbox drain, hooks or native commands. Exit 0 means report produced, never readiness."

type diagnosticReport struct {
	Schema         string                     `json:"schema"`
	ObservedAt     string                     `json:"observed_at"`
	Readiness      string                     `json:"readiness"`
	Repository     string                     `json:"repository"`
	Ledger         store.LedgerDiagnostic     `json:"ledger"`
	ReceiptBinding string                     `json:"receipt_binding"`
	InputSHA256    string                     `json:"input_sha256,omitempty"`
	Harness        *harness.HarnessDiagnostic `json:"harness,omitempty"`
	CodexHooks     *codexHookDiagnostic       `json:"codex_hooks,omitempty"`
}

func cmdDiagnose(args []string, env Env) error {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	kind := fs.String("harness", "", "adapter contract to report")
	launchPath := fs.String("launch", "", "launch configuration; requires harness")
	receiptPath := fs.String("receipt", "", "saved buddy.control.v1 receipt with captured identity")
	evidencePath := fs.String("evidence", "", "optional historical native version/hook capture")
	codexHooks := fs.String("codex-hooks", "", "a hand-wired Codex hooks.json layer to check for the BUDDY_HARNESS=codex marker and gate coverage")
	if help, err := parseFlags(fs, args, usageDiagnose, env); help || err != nil {
		return err
	}
	if err := noStray("diagnose", fs, usageDiagnose); err != nil {
		return err
	}
	if *kind != "" && *kind != "codex" && *kind != "claude" {
		return errors.New("diagnose --harness must be codex or claude")
	}
	if *receiptPath != "" && *launchPath != "" {
		return errors.New("diagnose accepts --launch or --receipt, not both")
	}
	if *launchPath != "" && *kind == "" {
		return errors.New("diagnose --launch requires --harness")
	}
	if *evidencePath != "" && *launchPath == "" && *receiptPath == "" {
		return errors.New("diagnose --evidence requires --launch or --receipt")
	}
	if *codexHooks != "" && *kind == "claude" {
		return errors.New("diagnose --codex-hooks reads a Codex hook layer; it does not apply to --harness claude")
	}
	r := diagnosticReport{Schema: harness.DiagnosticSchema, Readiness: "not_established", Repository: "unknown", ReceiptBinding: "unknown"}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	r.ObservedAt = now.UTC().Format(time.RFC3339Nano)
	var launch *harness.Launch
	var identity store.AdapterIdentity
	var evidence *harness.DiagnosticEvidence
	if *receiptPath != "" {
		var saved harness.Saved
		hash, err := diagnosticJSON(*receiptPath, &saved)
		if err != nil {
			return err
		}
		if saved.Schema != harness.Schema || !saved.Identity.Valid() {
			return errors.New("diagnose requires a valid saved adapter receipt")
		}
		if *kind != "" && *kind != saved.Identity.Harness {
			return errors.New("diagnose harness differs from saved receipt")
		}
		*kind = saved.Identity.Harness
		launch = &saved.Launch
		identity = store.AdapterIdentity{SessionID: saved.Identity.SessionID, Incarnation: saved.Identity.Incarnation}
		r.InputSHA256 = hash
	} else if *launchPath != "" {
		launch = &harness.Launch{}
		hash, err := diagnosticJSON(*launchPath, launch)
		if err != nil {
			return err
		}
		r.InputSHA256 = hash
	}
	if launch != nil && !filepath.IsAbs(launch.Worktree) {
		return errors.New("diagnose launch worktree must be absolute")
	}
	var evidenceHash string
	if *evidencePath != "" {
		evidence = &harness.DiagnosticEvidence{}
		hash, err := diagnosticJSON(*evidencePath, evidence)
		if err != nil {
			return err
		}
		evidenceHash = hash
	}
	if *kind != "" {
		d := harness.Diagnose(*kind, launch, evidence)
		d.EvidenceSHA256 = evidenceHash
		r.Harness = &d
	}
	if *codexHooks != "" {
		d, err := diagnoseCodexHookLayer(*codexHooks)
		if err != nil {
			return err
		}
		r.CodexHooks = d
	}
	// Strict discovery: diagnose reports where the cwd itself stands (D-102).
	rc, err := discoverRepo(env.Cwd)
	switch {
	case errors.Is(err, errNoLedger):
		r.Repository = "absent"
		r.Ledger = store.LedgerDiagnostic{State: "absent", Identity: "unknown"}
	case err != nil:
		r.Ledger = store.LedgerDiagnostic{State: "discovery_failed", Identity: "unknown"}
	default:
		r.Repository = "observed"
		var wt string
		if launch != nil {
			wt = launch.Worktree
			// Never diagnose one worktree's captured identity against a different
			// tree just because they share a common ledger. Aliases resolve before
			// comparison; lifecycle registration stores git's root spelling.
			want, e1 := filepath.EvalSymlinks(wt)
			got, e2 := filepath.EvalSymlinks(rc.top)
			r.ReceiptBinding = "worktree_mismatch"
			if e1 == nil && e2 == nil && want == got {
				r.ReceiptBinding = "worktree_matches"
				wt = rc.top
			} else {
				identity = store.AdapterIdentity{}
			}
		}
		r.Ledger = store.Diagnose(rc.ledger, identity, wt)
	}
	return json.NewEncoder(env.Stdout).Encode(r)
}

func diagnosticJSON(path string, dst any) (string, error) {
	b, err := harness.ReadDiagnosticFile(path)
	if err != nil {
		return "", err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return "", errors.New("diagnostic input must match the documented JSON schema")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return "", errors.New("diagnostic input must contain exactly one JSON object")
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}

// CODEX HOOK MARKERS (F-31). A Codex hook that runs buddy without
// BUDDY_HARNESS=codex is classified as a Claude hook (classifyHook), unless the
// hook process happens to inherit CODEX_THREAD_ID, which was observed in a tool
// shell and is not documented for hooks. A legacy lifecycle verb so classified
// changes a logical session without its captured token (D-069). The docs
// require the marker; nothing checked a hand-wired layer for it.
//
// This reads the operator's own hooks.json layer, offline, and reports which
// buddy commands carry the marker. It parses each command with the shell parser
// the gate uses (mvdan.cc/sh, parse only) and recognizes a simple command whose
// fixed-literal command word is buddy, optionally after `env` and NAME=value
// words. A wrapper (a script, `sh -c`) is not inspected, and a command word
// that is not a fixed literal is reported as unrecognized rather than guessed.
// Marker verdicts are unchanged by coverage findings: a marked command alone
// does not establish a PreToolUse gate, coverage of both write routes, or an
// absolute buddy executable. Keep those findings beside the command, and use
// the native admission matcher contract rather than a second regexp policy.
// Exit 0 still means only that a report was produced: a layer on disk is not
// the effective configuration Codex loaded, trusted or ran.
//
// A GATE LINE THAT FAILS OPEN (Codex report B2, amending D-107's reach). Codex
// lets the call through when a PreToolUse hook fails: a timeout, a spawn
// error, a signal, any exit but 0 and 2, and exit 2 with nothing on stderr
// (codex-rs/hooks events/pre_tool_use.rs; the installed 0.166.0 carries the
// same messages). A bare gate line whose buddy cannot decide (SIGKILL after `cp`
// over the running Mach-O, a corrupt binary's 126, a missing one's 127) was
// reported `marked` with no finding. (A Go panic exits 2 with its trace on
// stderr, which Codex happens to block on; the wrapper makes every death one
// refusal with a fixed reason.) Each PreToolUse gate now draws a finding unless its
// command is USAGE's fail-closed shape, and an `async: true` gate draws one
// because an async hook applies no control effect at all. Recognition is of
// that one shape, narrowly: a respelling that would also fail closed (`if !`,
// `sh -c`, a wrapper script) draws the finding, the safe direction for a report,
// and stays open rather than being guessed. A missing `timeout` draws nothing:
// Codex defaults a PreToolUse hook to 600 s, the gate answers in milliseconds,
// and an expired timeout fails open whichever value is set. The verdict word
// stays the marker verdict: diagnose grants and promotes nothing (D-071..D-081).

type codexHookDiagnostic struct {
	SHA256       string             `json:"sha256"`
	Verdict      string             `json:"verdict"`
	Unmarked     int                `json:"unmarked"`
	Unrecognized int                `json:"unrecognized"`
	Commands     []codexHookCommand `json:"commands"`
	Detail       string             `json:"detail"`
}

type codexHookCommand struct {
	Event   string `json:"event"`
	Verb    string `json:"verb,omitempty"`
	Marker  string `json:"marker,omitempty"`
	Finding string `json:"finding,omitempty"`
}

var buddyHookVerbs = map[string]bool{"gate": true, "hello": true, "beat": true, "busy": true, "idle": true, "bye": true}

func diagnoseCodexHookLayer(path string) (*codexHookDiagnostic, error) {
	b, err := harness.ReadDiagnosticFile(path)
	if err != nil {
		return nil, err
	}
	var layer struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &layer) != nil {
		return nil, errors.New("diagnose --codex-hooks needs a Codex hooks.json layer ({\"hooks\": {\"<Event>\": [{\"hooks\": [{\"type\": \"command\", \"command\": ...}]}]}})")
	}
	sum := sha256.Sum256(b)
	d := &codexHookDiagnostic{SHA256: hex.EncodeToString(sum[:]), Commands: []codexHookCommand{},
		Detail: "the supplied file only: not the effective configuration Codex loaded, its trust, or enforcement; a wrapper script or sh -c is not inspected"}
	events := make([]string, 0, len(layer.Hooks))
	for event := range layer.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	found := 0
	gateFound := false
	for _, event := range events {
		for _, group := range layer.Hooks[event] {
			for _, h := range group.Hooks {
				if h.Type != "command" {
					continue
				}
				for _, c := range codexHookCalls(event, h.Command) {
					if event == "PreToolUse" && c.Verb == "gate" {
						gateFound = true
						if !harness.CodexGateCoversWrites(group.Matcher) {
							c.addFinding("the PreToolUse gate matcher does not satisfy the apply_patch and Bash coverage contract")
						}
						if !failClosedGateLine(h.Command) {
							c.addFinding(gateFailsOpenFinding)
						}
						if h.Async {
							c.addFinding("the gate hook is async: Codex runs it in the background, so it cannot block the call")
						}
					}
					switch {
					case c.Verb == "":
						d.Unrecognized++
					case c.Marker != "codex":
						d.Unmarked++
						found++
					default:
						found++
					}
					d.Commands = append(d.Commands, c)
				}
			}
		}
	}
	if !gateFound {
		d.Commands = append(d.Commands, codexHookCommand{Event: "PreToolUse", Finding: "no PreToolUse buddy gate recognized in this layer"})
	}
	switch {
	case d.Unmarked > 0:
		d.Verdict = "unmarked"
	case d.Unrecognized > 0:
		d.Verdict = "unrecognized"
	case found > 0:
		d.Verdict = "marked"
	default:
		d.Verdict = "no_buddy_command_recognized"
	}
	return d, nil
}

const gateFailsOpenFinding = "the gate is not wrapped fail-closed: a buddy that is killed or cannot run fails this hook and Codex lets the call through; use USAGE's line, { ... gate; } 2>/dev/null || { echo <reason> >&2; exit 2; } (D-107)"

// failClosedGateLine reports whether a hook command is USAGE's fail-closed
// gate line: exactly one statement, `<gate> || { echo|printf ... >&2; exit 2; }`,
// where <gate> is the buddy gate call alone, bare or as the one statement of a
// brace group, with only stderr redirected. Each clause is a way the line
// fails open, each a case in diagnostic_failopen_test.go: stdout redirected
// loses the deny JSON; anything after the gate inside the group (`exit 0`)
// masks its death; `|| exit 2` alone exits 2 with an empty stderr, which Codex
// does not block on; a negated or backgrounded statement, or a second one,
// decides the hook's status instead.
func failClosedGateLine(command string) bool {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return false
	}
	s := file.Stmts[0]
	if !plainStmt(s) || len(s.Redirs) != 0 {
		return false
	}
	or, ok := s.Cmd.(*syntax.BinaryCmd)
	if !ok || or.Op != syntax.OrStmt {
		return false
	}
	return gateOperand(or.X) && refusalOperand(or.Y)
}

func plainStmt(s *syntax.Stmt) bool {
	return s != nil && !s.Negated && !s.Background && !s.Coprocess
}

// onlyStderrRedirected: every redirect is exactly `2>/dev/null`, so the gate's
// stdout (its deny JSON) still reaches Codex. Any fd-2 redirect was not enough
// (Codex code pass): `2>&1-` moves stdout onto stderr and closes it, so a deny
// is lost and the `||` never runs.
func onlyStderrRedirected(s *syntax.Stmt) bool {
	for _, r := range s.Redirs {
		if r.Op != syntax.RdrOut || r.N == nil || r.N.Value != "2" || r.Word == nil {
			return false
		}
		if w := decodeWord(r.Word); !w.known || w.text != "/dev/null" {
			return false
		}
	}
	return true
}

func gateOperand(s *syntax.Stmt) bool {
	if !plainStmt(s) || !onlyStderrRedirected(s) {
		return false
	}
	if b, ok := s.Cmd.(*syntax.Block); ok {
		if len(b.Stmts) != 1 {
			return false
		}
		s = b.Stmts[0]
		if !plainStmt(s) || !onlyStderrRedirected(s) {
			return false
		}
	}
	call, ok := s.Cmd.(*syntax.CallExpr)
	return ok && isBuddyGateCall(call)
}

// isBuddyGateCall: `[env] [NAME=value...] <.../buddy> gate` and nothing after
// the verb, fixed literals. The marker and the path are codexHookCalls'
// findings. An argument is refused (Codex code pass): `gate --help` prints the
// usage and exits 0 without deciding anything.
func isBuddyGateCall(call *syntax.CallExpr) bool {
	args := call.Args
	if len(args) > 0 {
		if w := decodeWord(args[0]); w.known && path.Base(w.text) == "env" {
			args = args[1:]
			for len(args) > 0 {
				w := decodeWord(args[0])
				name, _, isAssign := strings.Cut(w.text, "=")
				if !w.known || !isAssign || !validEnvName(name) {
					break
				}
				args = args[1:]
			}
		}
	}
	if len(args) != 2 {
		return false
	}
	cmd, verb := decodeWord(args[0]), decodeWord(args[1])
	return cmd.known && path.Base(cmd.text) == "buddy" && verb.known && verb.text == "gate"
}

// refusalOperand: `{ echo <text> >&2; exit 2; }` and nothing else. Codex blocks
// on exit 2 only when its trimmed stderr is not empty, so the reason is owed and
// must be visibly non-empty (Codex code pass): an empty `printf` and `echo "" >&2` print
// nothing, and `echo x 2>/dev/null >&2` sends the reason to /dev/null, so
// the one redirect allowed is `>&2` and echo needs a word with literal text.
func refusalOperand(s *syntax.Stmt) bool {
	if !plainStmt(s) || len(s.Redirs) != 0 {
		return false
	}
	b, ok := s.Cmd.(*syntax.Block)
	if !ok || len(b.Stmts) != 2 {
		return false
	}
	say, exit := b.Stmts[0], b.Stmts[1]
	if !plainStmt(say) || !plainStmt(exit) || len(exit.Redirs) != 0 {
		return false
	}
	echo, ok := say.Cmd.(*syntax.CallExpr)
	if !ok || len(echo.Args) == 0 {
		return false
	}
	if w := decodeWord(echo.Args[0]); !w.known || w.text != "echo" {
		return false
	}
	text := false
	for _, a := range echo.Args[1:] {
		text = text || wordHasText(a)
	}
	if !text || len(say.Redirs) != 1 {
		return false
	}
	r := say.Redirs[0]
	if r.Op != syntax.DplOut || r.N != nil || r.Word == nil {
		return false
	}
	if w := decodeWord(r.Word); !w.known || w.text != "2" {
		return false
	}
	call, ok := exit.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) != 2 {
		return false
	}
	e, code := decodeWord(call.Args[0]), decodeWord(call.Args[1])
	return e.known && e.text == "exit" && code.known && code.text == "2"
}

// wordHasText: the word carries literal text that is not blank and is not an
// echo option (`-n`, `-e`), so echo prints something whatever it expands to.
func wordHasText(w *syntax.Word) bool {
	var lit strings.Builder
	var walk func(parts []syntax.WordPart)
	walk = func(parts []syntax.WordPart) {
		for _, p := range parts {
			switch p := p.(type) {
			case *syntax.Lit:
				lit.WriteString(p.Value)
			case *syntax.SglQuoted:
				lit.WriteString(p.Value)
			case *syntax.DblQuoted:
				walk(p.Parts)
			}
		}
	}
	walk(w.Parts)
	t := strings.TrimSpace(lit.String())
	return t != "" && !strings.HasPrefix(t, "-")
}

func (c *codexHookCommand) addFinding(finding string) {
	if c.Finding != "" {
		c.Finding += "; "
	}
	c.Finding += finding
}

// codexHookCalls returns one entry per buddy call in a hook command, and one
// unrecognized entry per simple command whose command word is not a fixed
// literal (it could be buddy). Everything else is not buddy and is omitted.
func codexHookCalls(event, command string) []codexHookCommand {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return []codexHookCommand{{Event: event, Finding: "the command does not parse as a shell command, so whether it runs buddy is unknown"}}
	}
	var out []codexHookCommand
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		marker, markerKnown := "", true
		assign := func(name string, value *syntax.Word) {
			if name != EnvHarness {
				return
			}
			marker, markerKnown = "", true
			if value != nil {
				w := decodeWord(value)
				marker, markerKnown = w.text, w.known
			}
		}
		for _, a := range call.Assigns {
			if a.Name != nil {
				assign(a.Name.Value, a.Value)
			}
		}
		args := call.Args
		first := decodeWord(args[0])
		if first.known && path.Base(first.text) == "env" {
			args = args[1:]
			for len(args) > 0 {
				w := decodeWord(args[0])
				name, value, isAssign := strings.Cut(w.text, "=")
				if !w.known || !isAssign || !validEnvName(name) {
					break
				}
				if name == EnvHarness {
					marker, markerKnown = value, true
				}
				args = args[1:]
			}
			if len(args) == 0 {
				return true
			}
			first = decodeWord(args[0])
		}
		if !first.known {
			out = append(out, codexHookCommand{Event: event, Finding: "a command word is not a fixed literal, so whether it runs buddy is unknown"})
			return true
		}
		if path.Base(first.text) != "buddy" || len(args) < 2 {
			return true
		}
		verb := decodeWord(args[1])
		c := codexHookCommand{Event: event, Verb: verb.text, Marker: marker}
		if !verb.known {
			c.Verb = "unknown"
		}
		switch {
		case !markerKnown:
			c.Marker = "unknown"
			c.Finding = "BUDDY_HARNESS is not a fixed literal here, so whether this hook is marked as Codex is unknown"
		case marker == "":
			c.Marker = "missing"
			c.Finding = "runs buddy " + fence.Line(c.Verb, 32) + " without BUDDY_HARNESS=codex: classified as a Claude hook unless the hook process inherits CODEX_THREAD_ID"
			if buddyHookVerbs[c.Verb] && c.Verb != "gate" {
				c.Finding += ", and this lifecycle verb then changes a logical session without its captured token (D-069)"
			}
		case marker != "codex":
			c.Marker = fence.Line(marker, 32)
			c.Finding = "BUDDY_HARNESS names " + c.Marker + " in a Codex hook layer"
		case buddyHookVerbs[c.Verb] && c.Verb != "gate":
			c.Finding = "an unfenced Codex lifecycle hook: buddy refused it by design (D-069); lifecycle goes through the adapter's captured token"
		}
		if !filepath.IsAbs(first.text) {
			c.addFinding("the buddy command word is not an absolute path; executable selection depends on PATH or the working directory")
		}
		out = append(out, c)
		return true
	})
	return out
}

func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r != '_' && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
