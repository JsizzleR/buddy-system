package cli

// ownclaim.go — the gate catches an edit that no claim of the CALLING session
// covers (D-228).
//
// WHY. A lane edited docs/USAGE.md holding no claim on it; nobody held it, so
// the gate allowed it, and a second lane later claimed the file unaware of the
// first one's edit. The gate adjudicated only a PEER's hold (OwnerOf), and the
// core skill taught "it does NOT stop you editing a path nobody holds". The
// operator decided it is enforced (2026-11-18 06:13Z), warn first. This
// overturns, said out loud, D-012's and charter GIVEN 15's cut of "paths you
// did not claim": that cut rested on a warning that fires on every commit (the
// arming rule below keeps a lone session silent) and on OwnerOf being unable to
// compute it (PathClaims computes it from the scan OwnerOf already runs).
//
// THE RULE. A path a tool call DECLARES (gatePaths: Edit, Write, NotebookEdit,
// any tool carrying file_path or notebook_path, every side of an apply_patch,
// each gateTargets spelling), inside a governed worktree, with no `.git`
// component, that no open claim of the caller covers in any ledger that judges
// the caller, while another session not positively ended holds an open claim in
// the ledger that catches it: by default one model-visible line and the call
// proceeds; under BUDDY_OWN_CLAIM=deny, a refusal.
//
//   - A ledger judges a session it has a row for, live or ended, and the hook
//     cwd's OWN ledger (git's discovery from the cwd, never a D-212-walked one,
//     decided by the ledger, not by the arm that reached it) judges one it has
//     no row for too: a direct Codex session has no lifecycle hook, and a Claude
//     lane's SessionStart `hello` is `2>/dev/null; exit 0`, so without this the
//     unregistered session was freer than the registered one (panel P2-7,
//     Fable's D3 ruling). A cwd elsewhere with no row is not judged: the remedy
//     would be a row there that no SessionEnd of that session ever ends.
//   - Among the ledgers that judge, a covering claim in ANY covers (R/sub/x with
//     ledgers in R and R/sub must not need two claims); with none, the NEAREST
//     armed one (the deepest root) catches and is where the line says to claim.
//     Every spelling must be covered: a link AGENTS.md -> CLAUDE.md with a claim
//     on AGENTS.md alone lands on CLAUDE.md, the spelling named (D6).
//   - Armed (D2): another session, not positively ended, holds an open claim in
//     that ledger (store.OwnClaimFacts). One session alone in an init'ed repo is
//     never told about every file it touches: D-012's "noise, then disabled".
//   - A peer's hold outranks it (D7): every holder deny returns as it did, before
//     anything here speaks, so one call gets one document.
//
// NOT JUDGED, D-005's cooperative register, named: a Bash call declares no path
// (`echo x > f`, `sed -i`, a generator, `git checkout -- f`), so it stays
// pause-only; the commit gate's second section is the one line that sees such
// a write, when pre-commit runs (commitgate.go). This is not a lock.
//
// THE CARRIER. A warn is PreToolUse `additionalContext` with NO
// permissionDecision: "allow" skips the permission prompt and its reason reaches
// only the debug log, so it would approve every uncovered edit and tell the
// model nothing (Claude Code's hooks reference, documented; delivery is O1,
// NOT MEASURED until an installed session shows it). Rejected: beat (it places
// one spelling in the home ledger, so warn and a later deny would disagree on a
// coordinator's int-* edit: D-019's forecast-against-refusal defect),
// systemMessage (the user's pane), ask (stalls an unattended lane). Codex gets
// no warn (D9): its hooks page marks a hook run failed on a field it does not
// support, so a wrong guess is a failed hook on every uncovered patch; `deny`
// is honoured there. An unmarked Codex hook reads as Claude to classifyHook, so
// an apply_patch payload, which only Codex sends, is silent too (panel c5).
//
// THE KNOB. BUDDY_OWN_CLAIM=warn|deny|off, default warn. `off` runs OwnerOf
// exactly as before this arm existed, the same statements (Codex design pass
// F5). Any other value, where the arm would have spoken, DENIES naming the
// value, under every harness: a typo of deny must not pass for enforcement,
// and under Codex a fallback to warn is silence (F4). An error in a read this
// arm adds denies in every posture but off (invariant 2); no ledger stays a
// silent no-op, since the arm lives inside the adjudication a ledger opens
// (invariant 3). dirty_paths is never read (invariant 10).
//
// THE REMEDY, because the panel found the first draft's own words recreating
// the incident: `buddy claim <slug>` with a slug the caller already holds
// REPLACES that claim's scopes (ClaimDeclared: a refresh takes what it is
// given), so a lane answering the line freed its real files with no refusal
// (P1-A). The line says a NEW slug, why, and --shared for a file other lanes
// append to (an exclusive claim on docs/decisions.md taken at a landing refuses
// the next lane's shared bundle whole, P2-1). A scope is printed as a command
// only when it pastes back as itself (fenced unchanged, no comma,
// NormalizeScope's own spelling), else its nearest parent that does (panel c3);
// "run from <top>" when the catching ledger is not the cwd's own, since the
// same words pasted from the cwd would claim in another ledger (F7); `buddy
// hello </dev/null` first when the caller has no live row there (claim refuses
// a session that is not live), never for an id hello itself refuses (D-201).

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// EnvOwnClaim selects the own-claim arm's posture: warn (default), deny, off.
const EnvOwnClaim = "BUDDY_OWN_CLAIM"

// ownClaimLead is the fixed start of every warn line: what D10's evidence counts.
const ownClaimLead = "BUDDY UNCLAIMED:"

type ownPosture int

const (
	ownWarn ownPosture = iota
	ownDeny
	ownOff
	ownBad
)

func readOwnPosture(env Env) (ownPosture, string) {
	switch v := env.getenv(EnvOwnClaim); v {
	case "", "warn":
		return ownWarn, ""
	case "deny":
		return ownDeny, ""
	case "off":
		return ownOff, ""
	default:
		return ownBad, v
	}
}

// ownPostureWords says what a posture does, for hello's one line.
func ownPostureWords(v string) string {
	switch v {
	case "off":
		return "OFF (no line, no refusal)"
	case "deny":
		return "a REFUSAL"
	}
	return "a REFUSAL, since the value is not warn, deny or off"
}

// ownObs is one placement of one target spelling in one ledger that did not
// deny: the ledger, the root it was placed against, and whether a claim of the
// caller covers it there.
type ownObs struct {
	st             *store.Store
	top, rel, show string
	covered        bool
}

type ownFacts struct {
	known, ended, armed bool
	err                 error
}

// ownJudge collects one gate call's placements and renders its one verdict.
// nil when the arm is off or the call names no session: denyIfHeld then runs
// OwnerOf exactly as before.
type ownJudge struct {
	posture ownPosture
	bad     string
	session string
	// own is the hook cwd's own ledger (nil when the home was walked to, or
	// there is none): the one ledger that judges a caller it has no row for.
	own   *store.Store
	cp    string
	order []string
	obs   map[string][]ownObs
	facts map[*store.Store]ownFacts
}

func newOwnJudge(env Env, h hookInput) *ownJudge {
	p, bad := readOwnPosture(env)
	if p == ownOff || h.SessionID == "" {
		return nil
	}
	return &ownJudge{posture: p, bad: bad, session: h.SessionID,
		obs: map[string][]ownObs{}, facts: map[*store.Store]ownFacts{}}
}

// target names the spelling the next placements belong to.
func (j *ownJudge) target(cp string) {
	if j == nil {
		return
	}
	j.cp = cp
	if _, ok := j.obs[cp]; !ok {
		j.obs[cp] = nil
		j.order = append(j.order, cp)
	}
}

func (j *ownJudge) observe(st *store.Store, top, rel, cased string, covered bool) {
	if j == nil || j.cp == "" || gitComponent(rel) {
		return
	}
	show := cased
	if show == "" {
		show = rel
	}
	j.obs[j.cp] = append(j.obs[j.cp], ownObs{st: st, top: top, rel: rel, show: show, covered: covered})
}

// gitComponent reports a path with a component that is exactly `.git` once
// folded, at any depth (D5 as Fable ruled on panel c6): `.git/info/exclude`,
// `vendor/lib/.git/config` in a ledgerless nested repository, a linked
// worktree's `.git` pointer file. Git tracks no such path (2.54.0: `git add
// a/.git/x` adds nothing), so nothing a peer's commit can meet is hidden, and a
// peer's claim over one still refuses: this arm only. `.gitignore`, `.github/`,
// `mirror.git/config` are judged. The first-component rule warned on every one
// of the nested spellings.
func gitComponent(rel string) bool {
	for _, c := range strings.Split(rel, "/") {
		if store.Fold(c) == ".git" {
			return true
		}
	}
	return false
}

func (j *ownJudge) factsFor(st *store.Store) ownFacts {
	if f, ok := j.facts[st]; ok {
		return f
	}
	var f ownFacts
	f.known, f.ended, f.armed, f.err = st.OwnClaimFacts(j.session, st == j.own)
	j.facts[st] = f
	return f
}

// ownFinding is one uncovered spelling and the ledger that caught it.
type ownFinding struct {
	show, top         string
	runFrom, register bool
	// refused: the caller has no row here and its id is one hello refuses
	// (D-201 checks an id only where a row is CREATED, so an ended row with
	// a legacy id is revived by hello like any other: Codex code pass P3).
	refused bool
}

// verdict is the call's own-claim answer once every path has passed every
// holder check: "" and no output, a warn written to stdout, or a deny reason.
func (j *ownJudge) verdict(h hookInput, env Env) (denyReason string, warn string) {
	if j == nil {
		return "", ""
	}
	var found []ownFinding
	seen := map[[2]string]bool{}
	readFailed := func(err error) string {
		return fmt.Sprintf("buddy ledger read failed (%s); refusing %s", fence.Line(err.Error(), 512), fencedTool(h))
	}
	for _, cp := range j.order {
		obs := j.obs[cp]
		// Nearest first: the deepest root holding the spelling. Stable, so two
		// ledgers placed at one root keep the gate's own order.
		sort.SliceStable(obs, func(a, b int) bool { return len(obs[a].top) > len(obs[b].top) })
		var catch *ownObs
		var cf ownFacts
		for i := range obs {
			o := &obs[i]
			if o.covered {
				continue
			}
			f := j.factsFor(o.st)
			if f.err != nil {
				return readFailed(f.err), ""
			}
			if !(f.known || o.st == j.own) || !f.armed {
				// Not judged here (no row, not the cwd's own ledger), or judged
				// and nobody else holds anything: a farther ledger may still
				// catch it (rows 21b, 23).
				continue
			}
			catch, cf = o, f
			break
		}
		if catch == nil {
			continue
		}
		// A covering claim counts only in a ledger that judges the caller (D3):
		// one whose owner has no row there (a hand-edited ledger; no statement
		// deletes a sessions row) covered R/sub/x while R, armed and knowing
		// the caller, caught nothing (Codex code pass P2, row 21c). Read only
		// once a catch is found, so a call covered where it stands pays no
		// facts read.
		covered := false
		for _, o := range obs {
			if !o.covered {
				continue
			}
			f := j.factsFor(o.st)
			if f.err != nil {
				return readFailed(f.err), ""
			}
			if f.known || o.st == j.own {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		k := [2]string{catch.top, catch.rel}
		if !seen[k] {
			seen[k] = true
			found = append(found, ownFinding{show: catch.show, top: catch.top,
				runFrom: catch.st != j.own, register: !cf.known || cf.ended,
				refused: !cf.known && !fence.ValidSessionID(j.session)})
		}
	}
	if len(found) == 0 {
		return "", ""
	}
	switch j.posture {
	case ownBad:
		return fmt.Sprintf("buddy: %s=%s is not warn, deny or off, so the own-claim check cannot tell what was meant; refusing %s, which no claim of this session covers (%s). Set it to one of the three.",
			EnvOwnClaim, strconv.Quote(fence.Line(j.bad, 64)), fencedTool(h), ownPaths(found, maxReportPaths)), ""
	case ownDeny:
		return fmt.Sprintf("%s is inside no claim of this session; refusing %s (%s=deny). %s",
			ownPaths(found, maxReportPaths), fencedTool(h), EnvOwnClaim, j.remedy(found, maxReportPaths)), ""
	}
	if h.Harness == "codex" || h.ToolName == "apply_patch" {
		return "", "" // D9: no warn under Codex; deny above is honoured
	}
	return "", fmt.Sprintf("%s no claim of this session covers %s. Allowed, but a peer can claim it unaware of you. %s (A subagent: tell your parent.)",
		ownClaimLead, ownPaths(found, 3), j.remedy(found, 3))
}

// ownPaths lists at most n findings, each fenced and placed, then a count.
func ownPaths(found []ownFinding, n int) string {
	var parts []string
	for i, f := range found {
		if i == n {
			parts = append(parts, fmt.Sprintf("and %d more", len(found)-n))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (in %s)", fence.Line(f.show, 512), fence.Line(f.top, 512)))
	}
	return strings.Join(parts, ", ")
}

// remedy is the move that clears the line, per catching root: register first
// where the caller has no live row, a NEW slug (a refresh replaces scopes), and
// the scopes that paste back.
func (j *ownJudge) remedy(found []ownFinding, n int) string {
	type group struct {
		top                        string
		runFrom, register, refused bool
		scopes                     []string
		unpastable                 int
	}
	var groups []*group
	for i, f := range found {
		if i == n {
			break
		}
		var g *group
		for _, x := range groups {
			if x.top == f.top && x.runFrom == f.runFrom && x.register == f.register && x.refused == f.refused {
				g = x
			}
		}
		if g == nil {
			g = &group{top: f.top, runFrom: f.runFrom, register: f.register, refused: f.refused}
			groups = append(groups, g)
		}
		if s, ok := pastableScope(f.show); ok {
			if !containsString(g.scopes, s) {
				g.scopes = append(g.scopes, s)
			}
		} else {
			g.unpastable++
		}
	}
	var out []string
	for _, g := range groups {
		where := ""
		if g.runFrom {
			where = ", run from " + fence.Line(g.top, 512)
		}
		if g.refused {
			// D-201: hello refuses this id, so no claim can be made by it
			// here; naming hello would send the reader to a refusal.
			out = append(out, fmt.Sprintf("This session's id is not one a ledger registers, so no claim of it can cover a path in %s: ask the operator.", fence.Line(g.top, 512)))
			continue
		}
		var b strings.Builder
		b.WriteString("Claim before the next edit" + where + ": ")
		if g.register {
			b.WriteString("this session has no live row in that ledger, so first `buddy hello </dev/null`, then ")
		}
		if len(g.scopes) > 0 {
			b.WriteString("`buddy claim <a-new-slug> --desc \"<what, as your brief says>\"")
			for _, s := range g.scopes {
				if strings.HasPrefix(s, "-") {
					// Inline, so a scope spelling a flag (`--shared`) is read
					// as the value, never refused as one (D-214; Codex
					// re-verify 1: `-x` is claimable, so not "rename it").
					b.WriteString(" --scope=" + shellQuote(s))
				} else {
					b.WriteString(" --scope " + shellQuote(s))
				}
			}
			b.WriteString("`")
		} else {
			// No parent pastes back either (a root-level `a,b.md`): claim
			// refuses a comma and `.`, so no claim of any session can cover
			// it as named (Fable cross-check).
			b.WriteString("no scope `buddy claim` accepts covers " + ownThem(g.unpastable) + " as printed (a comma, a space at either end, a backslash, a control character): rename " + ownThem(g.unpastable) + ", or ask the operator")
		}
		if g.unpastable > 0 && len(g.scopes) > 0 {
			// The same fallback for the rest of a mixed group: "a scope
			// covering it" sent the reader looking for one that cannot exist
			// (Codex re-verify 1).
			fmt.Fprintf(&b, "; no scope it accepts covers the other %d path(s) as printed: rename them, or ask the operator", g.unpastable)
		}
		b.WriteString(". A NEW slug: re-claiming a slug you hold replaces its scopes; add --shared for a file other lanes append to.")
		out = append(out, b.String())
	}
	return strings.Join(out, " ")
}

// pastableScope is p, or its nearest parent, as a scope `buddy claim` stores
// as typed: unchanged by the fence, no comma (claim refuses one), and
// NormalizeScope's own spelling (a backslash reads as a slash, surrounding
// space is trimmed). A printed scope that does not round-trip claims another
// path, and under deny the lane that pasted it stays refused (panel c3).
func pastableScope(p string) (string, bool) {
	for s := filepath.ToSlash(p); s != "" && s != "." && s != "/"; s = path.Dir(s) {
		// A leading dash pastes back: remedy prints it inline, `--scope=-x`.
		if fence.Line(s, 512) != s || strings.Contains(s, ",") {
			continue
		}
		if n, err := store.NormalizeScope(s); err != nil || n != s {
			continue
		}
		return s, true
	}
	return "", false
}

// ownThem is the pronoun for n unpastable paths.
func ownThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
