package harness

// Claim renewal for an adapter-launched worker (F-26, D-078).
//
// THE FAILURE. A worker launched by buddy-adapter edited its claimed file every
// few minutes for forty and the claim went STALE. Its gate contract installs
// only the PreToolUse gate (an attempted write is not a completed one), and the
// controller translated only busy, idle and end, none of which names a path. A
// PostToolUse hook in the adapter's settings was considered and kept only as a
// fallback: it would be a second lifecycle writer beside the controller,
// reopen admission for both harnesses (Mod profiles, hook hashes, matchers),
// and cost two process starts a tool call on the hook budget.
//
// THE ROUTE. The backend's reader already sees each native COMPLETION: a
// Claude tool_result matched to the tool_use that named the path, a Codex
// item/completed fileChange. It hands the paths to this mailbox and the
// controller renews the captured incarnation's covering claims through
// `buddy lifecycle renew`, a renewal-only transaction. What renews, and what
// does not, is decided in claude.go and codex.go; this file is the transport.
//
// WHY A MAILBOX AND NOT AN EVENT (Codex's review, adopted). One ordinary
// controller event per tool result blocks the native reader once the event
// channel fills (the 65th Claude or 129th Codex result behind a stalled
// consumer), and that reader also routes control responses. offer never
// blocks: it coalesces by launch generation and path under its own short
// lock, and past its bounds it DROPS the candidate and records a coverage gap.
// It can never hold back an approval, a control response, a result, idle or
// end, which keep their existing ordered route.
//
// WHY NOT BeatLogical. The batch is applied off the reader, so it can land
// after the turn's idle; BeatLogical runs logicalBusy and erased that idle row
// (TestLogicalRenewalAfterIdleKeepsIdle). Renewal changes no lifecycle state
// and never clears the controller's approval observation gap.
//
// PACING. The first observation of a path renews at once; after that, at most
// once per path per renewInterval. A deferred path is flushed by a timer, which
// only ever applies observations already received and never manufactures
// activity. The interval is a sixtieth of the 30-minute stale mark.

import (
	"strings"
	"sync"
	"time"
)

const (
	renewInterval = 30 * time.Second
	// Mailbox bounds: pending normalized paths and their bytes. Overflow drops
	// renewal candidates only, with a coverage gap.
	renewMaxPending      = 256
	renewMaxPendingBytes = 64 << 10
	// renewBatchMax is buddy's maxRenewPaths, the patch parser's own bound.
	renewBatchMax = 128
	// A native path longer than this is not carried at all (PATH_MAX is 1024
	// on darwin; 4096 leaves room for a lexical `..` spelling).
	renewPathMax = 4096
	// Claude correlation bounds: outstanding file-tool uses awaiting a result.
	correlationMax      = 128
	correlationMaxBytes = 1 << 20
)

// RenewalObservation is the journal's record of one renewal batch: the
// captured launch generation, the native spellings sent, where the ledger
// placed them, how many claims it renewed, and whether coverage is still
// complete. Identities and fenced paths only, never a tool's input or result.
type RenewalObservation struct {
	Generation string      `json:"launch_generation"`
	Paths      []string    `json:"paths,omitempty"`
	Placed     []string    `json:"placed_paths,omitempty"`
	Renewed    int         `json:"renewed_claims"`
	Unresolved []string    `json:"unresolved_paths,omitempty"`
	Result     Observation `json:"result"`
	Coverage   Observation `json:"coverage"`
}

// RenewResult is what the ledger reports a renewal did.
type RenewResult struct {
	Renewed int
	Placed  []string
	// Unresolved names paths one of whose gate spellings Buddy could not
	// resolve; their renewal may be incomplete, so coverage is unknown.
	Unresolved []string
}

// renewalSpelling is the one spelling a path is coalesced, paced and sent
// under: empty and `.` components removed, which never changes the file named
// (Codex review of the code pass, finding 4: "/w/a", "/w/./a" and "/w//a" each
// renewed at once). `..` is kept: after a symlinked directory it names a
// different file physically than lexically, and Buddy places both.
func renewalSpelling(p string) string {
	var kept []string
	for _, part := range strings.Split(p, "/") {
		if part != "" && part != "." {
			kept = append(kept, part)
		}
	}
	s := strings.Join(kept, "/")
	if strings.HasPrefix(p, "/") {
		return "/" + s
	}
	if s == "" {
		return "."
	}
	return s
}

type renewalKey struct{ generation, path string }

type renewalMailbox struct {
	mu      sync.Mutex
	pending []renewalKey
	index   map[renewalKey]bool
	bytes   int
	// gap is the first reason coverage became unknown, and gaps how many
	// coverage losses have been recorded; reported counts the ones already
	// journalled, so a new loss is journalled once even with no path due.
	gap      string
	gaps     int
	reported int
	wake     chan struct{}
}

func newRenewalMailbox() *renewalMailbox {
	return &renewalMailbox{index: map[renewalKey]bool{}, wake: make(chan struct{}, 1)}
}

// renewalSource is a backend that observes native completions. Unexported, so
// only this package's backends qualify; a backend without it renews nothing
// and says so in its capabilities.
type renewalSource interface {
	observeRenewals(*renewalMailbox)
}

func (m *renewalMailbox) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// offer queues the paths a completed native write named, under the launch
// generation the backend was started with. It never blocks on the controller.
func (m *renewalMailbox) offer(generation string, paths []string) {
	if m == nil || len(paths) == 0 {
		return
	}
	m.mu.Lock()
	dropped := 0
	for _, p := range paths {
		if p != "" {
			p = renewalSpelling(p)
		}
		k := renewalKey{generation, p}
		if p == "" || m.index[k] {
			continue
		}
		if len(p) > renewPathMax || len(m.pending) >= renewMaxPending || m.bytes+len(p) > renewMaxPendingBytes {
			dropped++
			continue
		}
		m.index[k] = true
		m.pending = append(m.pending, k)
		m.bytes += len(p)
	}
	if dropped > 0 {
		m.lose("renewal mailbox bound reached; a completed write's paths were not renewed")
	}
	m.mu.Unlock()
	m.signal()
}

// unknown records that a native completion could not be classified, so a
// write may have completed without its claims being renewed.
func (m *renewalMailbox) unknown(reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lose(reason)
	m.mu.Unlock()
	m.signal()
}

// lostReported records a coverage loss the caller is journalling itself.
func (m *renewalMailbox) lostReported(reason string) {
	m.mu.Lock()
	m.lose(reason)
	m.reported = m.gaps
	m.mu.Unlock()
}

func (m *renewalMailbox) reason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gap
}

func (m *renewalMailbox) lose(reason string) {
	if m.gap == "" {
		m.gap = reason
	}
	m.gaps++
}

// take removes up to max pending paths of generation that due admits, and
// drops (as a gap) any path queued under another generation: a completion
// from a launch this controller no longer runs never renews. It returns the
// batch, the gap reason, whether a coverage loss is unreported, and the
// remaining paths of this generation, for the caller's next deadline.
func (m *renewalMailbox) take(generation string, due func(string) bool, max int) (batch []string, gap string, unreported bool, rest []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.pending[:0]
	for _, k := range m.pending {
		switch {
		case k.generation != generation:
			m.lose("a completion from another launch generation was discarded")
		case len(batch) < max && due(k.path):
			batch = append(batch, k.path)
		default:
			kept = append(kept, k)
			rest = append(rest, k.path)
			continue
		}
		delete(m.index, k)
		m.bytes -= len(k.path)
	}
	m.pending = kept
	unreported = m.gaps > m.reported
	m.reported = m.gaps
	return batch, m.gap, unreported, rest
}
