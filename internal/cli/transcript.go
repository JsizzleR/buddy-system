package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// transcript.go — the ONE place buddy reads a Claude Code transcript, and the
// only thing in the claims binary that looks at a file the harness owns.
//
// WHY AT ALL. An orchestrator session deciding who takes the next task is
// deciding about CAPACITY: "we are good here, you know this area, go take it"
// rests on how much context the candidate is already carrying, and the ledger
// knew nothing about it. The number exists in exactly one place — the
// session's own transcript — and the hook JSON already names the file
// (`transcript_path`), so reading it costs no new process, no new hook line
// and no round trip.
//
// WHAT IT TAKES: one record — the newest assistant turn's token accounting,
// its model, and the turn's own timestamp. NEVER ANY CONTENT. The transcript
// is the operator's entire session, and everything this feature produces lands
// in OTHER sessions' context windows; a design that could leak a prompt into a
// peer's listing would be a worse bug than the blindness it cures.
//
// WHAT IT REFUSES TO INFER: the context WINDOW. Measured 2026-09-20 on this
// box: a session running Opus with the 1M-token window records
// `"model":"claude-opus-5"`, byte for byte what the 200k variant records —
// nothing in the file distinguishes them. The observed 90,499-token prompt is
// 45% of one window and 9% of the other, so a percentage derived from the
// model string is not an approximation, it is a fabrication. The denominator
// is DECLARED by the operator (BUDDY_CONTEXT_WINDOW) or it is not printed.
//
// BOUNDED, AND SILENT WHEN IT CANNOT BE SURE. Measured over the seven
// transcripts of this project: files 0.6–2.3 MB, single lines up to 267 KB,
// and the last usage-bearing line beginning 2.8–11.0 KB from EOF. So the file
// is never read whole; the last tailBytes are read and scanned backwards, and
// 64 KB clears the measured worst case sixfold. It is still a BOUNDED ATTEMPT
// and not a guarantee — a 267 KB tool result appended after the last assistant
// turn puts the record outside the window — and the answer to that case is to
// keep the previous observation and report nothing new. There is no window
// size that cannot be missed; there is only a bigger one that costs the hook
// budget to be wrong less often.

// tailBytes is how much of the end of a transcript is read first, and
// maxTailBytes is how far one miss is allowed to escalate. See the header:
// the measured worst case was 11.0 KB from EOF, so the first window is 6x the
// evidence and answers effectively every call; the second covers the case the
// first cannot, a record pushed out of the window by ONE huge tool result
// (largest line measured here: 267 KB).
//
// A CAP AND NOT "THE WHOLE FILE". Reading back until something is found makes
// the cost of a hook a function of how long the session has been running —
// 2.3 MB and climbing, on the transcripts measured here — and the failure it
// buys back is one session reporting nothing. The second read is paid only
// when the first has already missed.
const (
	tailBytes    = 64 << 10
	maxTailBytes = 512 << 10
)

// usageSample is one assistant turn's accounting as the transcript recorded
// it. Prompt is what the model was HANDED: fresh input plus cache read plus
// cache written. Cache-read tokens occupy the window exactly like any other —
// a large cache hit is cheaper, not smaller — so they are summed, not netted
// out.
type usageSample struct {
	At    time.Time
	Model string
	// Effort is the reasoning effort the turn ran at. Measured across this
	// box's transcripts: "xhigh" on six of seven, "high" on the other — so it
	// discriminates, and two sessions on the same model at different efforts
	// are different instruments to hand a task to. Empty when the harness
	// does not record it.
	Effort     string
	Prompt     int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	// Cache5m and Cache1h: the tokens written into the prompt cache at each
	// LIFETIME tier, from usage.cache_creation. This is what says whether a
	// peer's cache is still hot — a session on the 1h tier is cheap to
	// resume for an hour after its last request, one on the 5m tier for five
	// minutes — and it cannot be inferred from anything else: the same model
	// string runs under either. Measured on this box, 2026-09-20: 2371 usage
	// records, every one carrying the object, all writes on the 1h tier.
	//
	// Taken from the NEWEST record that wrote anything, which is not always
	// the newest record: a turn that only READ the cache (1 of 2371 here)
	// reports both tiers as 0, and the tier the cache was built at is the
	// last one that built it. Both zero after the whole window: unrecorded,
	// and the roster says nothing about the cache. TierAt is that writer's
	// own time — its provenance, carried separately from At because the two
	// records can differ, and the ledger's write guard needs to order tier
	// evidence by the tier's clock, not the prompt's (Codex code pass,
	// 2026-09-20). Within the SAME window the tier is best-effort: a sample
	// found in the first 64 KB with no writer in it does not escalate to look
	// for one, because a hook's cost must not depend on how long since the
	// session last wrote its cache.
	Cache5m int64
	Cache1h int64
	TierAt  time.Time
}

// transcriptLine is the sliver of a transcript record this cares about. A
// narrow struct on purpose: the file's shape is the harness's to change, and
// every field named here is one that has to keep existing.
type transcriptLine struct {
	// A subagent's turns are its own context, not the session's. They were
	// inlined in the parent transcript by older harness versions and live in
	// a per-session subdirectory in the one measured here (2.1.278) — so this
	// costs one comparison and guards against reporting a 4k subagent prompt
	// as the 90k session that spawned it.
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Effort      string `json:"effort"`
	Message     struct {
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			Output     int64 `json:"output_tokens"`
			// The per-tier breakdown of CacheWrite. A pointer, so "the
			// harness did not record it" stays distinct from "it wrote
			// nothing at either tier".
			CacheCreation *struct {
				E5m int64 `json:"ephemeral_5m_input_tokens"`
				E1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// userLine is the even narrower sliver of a PROMPT or TOOL RESULT record: the
// three fields that say it is the session's own and when it was written. Its
// content is never named, so it is never decoded into anything.
type userLine struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	PromptID    string `json:"promptId"`
}

// lastUsage returns the newest usable usage record in the transcript at path.
// Every failure is the same answer — false, meaning "no new observation" —
// because this runs inside a hook whose verdict it must never change: a
// missing path, an unreadable file, a record outside the tail, a shape this
// does not know, an unparseable timestamp. The caller keeps whatever it
// recorded last and says how old it is.
func lastUsage(path string) (usageSample, bool) {
	r := readTurn(path, "")
	return r.u, r.ok
}

// turnRead is what one read of a transcript's tail says about the turn it
// ends with: the newest usage record, and whether something newer is still
// waiting for its reply.
//
// PENDING IS SEEN IN THE FILE, NOT GUESSED FROM A CLOCK (D-060, #52). At Stop
// the harness has not yet written the turn's final assistant record: measured
// on 2.1.283 over 11 Stops (prose turns, tool turns, long output), it was
// missing at every one and landed 33–83 ms after Stop fired, while the hooks
// were still running. What IS on disk by then is the turn's own prompt or its
// last tool result — a `"type":"user"` record — so "the newest user record is
// newer than the newest usage record" says exactly "the reply to it has not
// landed", in a prose turn and a tool turn alike, with no threshold to tune.
// Without it the Stop hook recorded the PREVIOUS request's footprint and dated
// the idle mark by it: in a prose-only session, the previous turn.
//
// A TIE IS PENDING. Two records in one millisecond cannot be ordered by
// their clocks, and the two wrong answers are not equally wrong: calling a
// landed reply pending records no footprint for a turn (busy records it at
// the next prompt), while calling a pending one landed records the previous
// request as this turn's — the bug this exists to remove.
//
// AND THE TURN IS NAMED. `prompt` is the hook's prompt_id; when this turn's
// own user record is found in the tail and a user record of ANOTHER prompt
// is newer than it, the session has begun a newer turn — a queued prompt, or
// a successor incarnation after a bye and a hello — and this Stop is late
// (`superseded`). Its turn's record not being found at all (an unknown kind
// of turn, a harness that sends no prompt_id) says nothing, and the time
// fences apply as before.
type turnRead struct {
	u  usageSample
	ok bool
	// pendingAt is that unanswered record's own time; zero means nothing is
	// waiting (or no user record was found in the tail read).
	pendingAt time.Time
	// superseded: the named turn's record is on disk behind a newer prompt.
	superseded bool
}

func readTurn(path, prompt string) turnRead {
	if path == "" {
		return turnRead{}
	}
	// A REGULAR FILE, checked before the open. os.Open on a FIFO with no
	// writer BLOCKS — indefinitely, inside a 100 ms hook — and a directory
	// opens fine and fails later. The harness names this path, so neither is
	// a realistic input; the check costs one stat and removes the class
	// rather than arguing about how it could be reached.
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return turnRead{}
	}
	f, err := os.Open(path)
	if err != nil {
		return turnRead{}
	}
	defer f.Close()
	// One escalation, then give up. The second window is not a bigger guess
	// at "enough": it is the one measured shape the first cannot hold, a
	// record behind a single oversized tool result.
	//
	// The newest user record is taken from the FIRST window that has one,
	// which is the smallest: it is at the end of the file by construction,
	// and an escalation only goes looking further back for a usage record.
	var last lastUser
	for _, window := range [...]int64{tailBytes, maxTailBytes} {
		u, ok, lu := usageInTail(f, fi.Size(), window, prompt)
		if last.at.IsZero() {
			last = lu
		}
		if ok {
			r := turnRead{u: u, ok: true, superseded: last.superseded}
			if !last.at.IsZero() && !last.at.Before(u.At) {
				r.pendingAt = last.at
			}
			return r
		}
		if window >= fi.Size() {
			break // the whole file was already read; a second pass reads the same bytes
		}
	}
	// No usage record at all — a session's first turn, before its first reply
	// lands. A user record here is pending exactly as above.
	return turnRead{pendingAt: last.at, superseded: last.superseded}
}

// usageInTail returns the newest usable record within the last `window` bytes
// of f, and what the newest user record there says (see lastUserOf).
//
// THE NEWEST BY TIMESTAMP, not the last one positionally. A transcript is
// appended to, so the two coincide on every file measured here — which is
// exactly why taking the position was an assumption rather than a property.
// Reading the whole window costs a parse per usage-bearing record (a handful,
// since the prefilter skips the tool results that are most of the bytes) and
// makes this function's name true of what it returns.
//
// THE USER RECORD IS THE LAST ONE POSITIONALLY, and that difference is paid
// for: user records are the tool results — most of the bytes — so taking the
// newest by timestamp would parse every one of them, inside a hook that
// polls. The last in append order is the harness's own order and costs one
// parse (a few more only when the last candidates are a subagent's or cut off
// mid-write). A user record that is out of order by timestamp can only make
// pending read false where it was true, which leaves the answer the Stop hook
// gave before D-060: the previous reply, recorded as it always was.
func usageInTail(f *os.File, size, window int64, prompt string) (usageSample, bool, lastUser) {
	var best usageSample
	found := false
	// The newest record that WROTE a cache tier, tracked separately from the
	// newest record: see usageSample.Cache5m.
	var tierAt time.Time
	var tier5m, tier1h int64
	off := size - window
	if off < 0 {
		off = 0
	}
	buf := make([]byte, size-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return best, false, lastUser{}
	}
	buf = buf[:n]
	// A window that did not start at byte 0 begins mid-record. That fragment
	// is not JSON and must never be handed to the parser as though it were:
	// dropping it is the difference between "no sample" and a sample parsed
	// out of half a tool result.
	if off > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return best, false, lastUser{} // one line longer than the whole window
		}
		buf = buf[i+1:]
	}
	lines := bytes.Split(buf, []byte("\n"))
	for _, ln := range lines {
		// A prefilter before the parser, because most of a transcript's bytes
		// are tool results: unmarshalling a 108 KB line to discover it has no
		// usage in it is the one cost a 100 ms hook cannot spend. A record
		// still being written fails the parse below and is skipped there.
		//
		// (The prefilter is literal: a record spelling the key with a JSON
		// escape — "\u0075sage" — would be skipped. No encoder writes that,
		// and the cost of not assuming so is parsing every tool result.)
		if !bytes.Contains(ln, []byte(`"usage"`)) {
			continue
		}
		var rec transcriptLine
		if err := json.Unmarshal(ln, &rec); err != nil {
			continue
		}
		if rec.IsSidechain || rec.Message.Usage == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339, rec.Timestamp)
		if err != nil {
			// No substituting "now": the whole point of storing the turn's
			// own time is that a reader can see how stale the number is, and
			// a fabricated timestamp reads as the freshest possible sample.
			continue
		}
		u := rec.Message.Usage
		// Counts outside the plausible are not a smaller problem than a
		// missing record, they are a louder one: a negative field would print
		// `prompt -1`, and summing two near-maxint fields wraps to a negative
		// prompt that then renders as a negative percentage. maxTokens is
		// thousands of times any real context window, so nothing legitimate
		// is refused and the addition below cannot overflow.
		//
		// VALIDATED BEFORE EITHER SELECTION. The first shape chose the tier
		// first, so a record rejected here for its counts had already set the
		// cache tier — the timer then described a turn the clock beside it
		// had discarded (Codex code pass, 2026-09-20).
		const maxTokens = 1 << 40
		if u.Input < 0 || u.CacheRead < 0 || u.CacheWrite < 0 || u.Output < 0 ||
			u.Input > maxTokens || u.CacheRead > maxTokens || u.CacheWrite > maxTokens || u.Output > maxTokens {
			continue
		}
		// The tier, from the newest record that WROTE one, chosen before the
		// prompt's own newest-record skip below: a pure read can be the newest
		// record while an older one in the same window built the cache. A
		// negative tier count is not a tier and the record sets none. Ties on
		// the timestamp go to the record later in the file, which is the
		// harness's append order.
		if cc := u.CacheCreation; cc != nil && cc.E5m >= 0 && cc.E1h >= 0 && cc.E5m <= maxTokens && cc.E1h <= maxTokens &&
			(cc.E5m > 0 || cc.E1h > 0) && !at.Before(tierAt) {
			tierAt, tier5m, tier1h = at, cc.E5m, cc.E1h
		}
		if found && at.Before(best.At) {
			continue // an older record further down the file
		}
		best, found = usageSample{
			At:         at,
			Model:      rec.Message.Model,
			Effort:     rec.Effort,
			Prompt:     u.Input + u.CacheRead + u.CacheWrite,
			CacheRead:  u.CacheRead,
			CacheWrite: u.CacheWrite,
			Output:     u.Output,
		}, true
	}
	if found {
		best.Cache5m, best.Cache1h, best.TierAt = tier5m, tier1h, tierAt
	}
	return best, found, lastUserOf(lines, prompt)
}

// lastUser is what the tail says about its newest user record.
type lastUser struct {
	at         time.Time // the newest user record's own time; zero if none
	superseded bool      // the named prompt's record is found, and a newer one is another prompt's
}

// lastUserOf reads the user records among lines, walking back from the end:
// the newest parseable, non-sidechain one's time, and — when prompt names a
// turn and the newest is another prompt's — whether that turn's own record is
// further back (superseded). The walk stops at the named turn's record, so
// the normal case, where the newest record IS the named turn's, costs one
// parse. The literal prefilter is the same bet usageInTail's makes on
// "usage": the harness writes compact JSON, and inside a string value every
// quote is escaped, so a tool result's CONTENT cannot spell `"type":"user"` —
// only a record's own key can (or a nested object's, which the parse below
// then rejects on the top-level field).
func lastUserOf(lines [][]byte, prompt string) lastUser {
	var out lastUser
	newestID := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], []byte(`"type":"user"`)) {
			continue
		}
		var rec userLine
		if err := json.Unmarshal(lines[i], &rec); err != nil || rec.Type != "user" || rec.IsSidechain {
			continue // cut off mid-write, a subagent's, or a nested match
		}
		at, err := time.Parse(time.RFC3339, rec.Timestamp)
		if err != nil {
			continue
		}
		if out.at.IsZero() {
			out.at, newestID = at, rec.PromptID
			if prompt == "" || newestID == "" || newestID == prompt {
				return out
			}
			continue
		}
		if rec.PromptID == prompt {
			out.superseded = true
			return out
		}
	}
	return out
}

// declaredWindow reads the operator's declaration of this session's context
// window, in tokens. 0 means "undeclared", which is a rendering instruction:
// print the prompt size and no percentage.
//
// AN ENVIRONMENT VARIABLE, not a lookup table of model names, because the
// measured fact is that the transcript cannot tell the 200k and 1M variants
// apart (see the header). A table would be a guess wearing a number's
// clothing, and it would be wrong silently, in the direction that matters:
// reporting 9% for a session that is actually at 45%.
//
// Suffixes because an operator types 1M, not 1000000, and a declaration that
// silently does nothing is worse than no declaration at all.
func declaredWindow(raw string) int64 {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1_000, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1_000_000, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	// The upper bound is what stops the multiply below from wrapping:
	// BUDDY_CONTEXT_WINDOW=18446744073709552k parses, and n*mult then wraps to
	// 384 — a denominator small enough to print a confident, enormous
	// percentage. Undeclared is the right answer to a declaration nobody
	// meant.
	if err != nil || n <= 0 || n > maxDeclaredWindow/mult {
		return 0
	}
	return n * mult
}

// maxDeclaredWindow bounds a declaration at a thousand times the largest
// window that exists today. It is a sanity bound, not a model fact: the point
// is that no arithmetic downstream can wrap, not that a bigger window is
// impossible.
const maxDeclaredWindow = 1 << 40
