// Package store is the claims + control + inbox ledger: one SQLite database
// at <repo>/.git/buddy.db, shared by every worktree of a checkout.
//
// Design rules (PLAN.md C1, Codex-reviewed):
//   - Every read-check-mutate runs in one IMMEDIATE transaction.
//   - Identity is (session_id, incarnation); PID is diagnostic only.
//   - Scopes are canonical repo-relative exact paths or directory prefixes.
//     No globs. Comparison is case-folded (APFS default is case-insensitive).
//   - Staleness marks, it never reaps: sweep removes only released/orphaned
//     rows past TTL; open claims of live sessions survive any age.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
)

// StaleAfter is how long without renewal before a claim or session shows STALE.
const StaleAfter = 30 * time.Minute

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	session_id  TEXT PRIMARY KEY,
	incarnation TEXT NOT NULL,
	label       TEXT NOT NULL,
	worktree    TEXT NOT NULL,
	pid         INTEGER NOT NULL DEFAULT 0,
	started     INTEGER NOT NULL,
	last_seen   INTEGER NOT NULL,
	ended       INTEGER,
	-- The terminal handle the session REPORTED AT REGISTRATION (a herdr pane
	-- id, a tmux pane), read from the environment the harness inherited and
	-- rendered as "<provider>:<id>". A handle for the OPERATOR — it is the
	-- thing on their screen — and never something buddy invokes. Added by an
	-- ALTER arm in migrate (schema 6): the script is IF NOT EXISTS, which
	-- cannot add a column to a table that already exists.
	terminal    TEXT NOT NULL DEFAULT ''
);
-- WHICH PROCESSES A SESSION IS REGISTERED TO. This is what fences bye.
--
-- THE DEFECT (D-025, "Known unfixed" until then). The SessionEnd hook carries
-- no incarnation, so bye had only a session id, and a delayed bye from a
-- dead incarnation ended a LIVE one under the same id (claude --resume
-- keeps the id): its heartbeats became silent no-ops and the next peer's
-- hello orphaned its claims while it was still editing. Two processes on one
-- session id (a second --resume while the first still runs) had the same
-- shape without any delay at all.
--
-- A session is registered to the harness PROCESS that spawned its hooks
-- (measured 2026-09-20: the hook's parent is the claude process itself), with
-- the process's start time as a reuse discriminator, because a bare pid is a
-- recycled number. A bye ends the session only when NO registered process is
-- still alive. Several rows per session are legal and are the point: the
-- second --resume registers a second process, and whichever exits first
-- leaves the session open for the other. Rows are wiped on revival (a new
-- incarnation) and on the end that empties them; a dead one is pruned by the
-- next bye that looks.
--
-- Only HOOK-DRIVEN hello and beat register a process. A hand-run hello has no
-- harness ancestor (or the operator's own terminal, which lives forever and
-- would refuse every bye), so it registers nothing and a session with no rows
-- here is UNBOUND: its bye ends it as it always did.
CREATE TABLE IF NOT EXISTS session_procs (
	session_id TEXT NOT NULL,
	pid        INTEGER NOT NULL,
	born       INTEGER NOT NULL DEFAULT 0,
	registered INTEGER NOT NULL,
	PRIMARY KEY (session_id, pid)
);
CREATE TABLE IF NOT EXISTS claims (
	claim_id    TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	incarnation TEXT NOT NULL,
	slug        TEXT NOT NULL,
	descr       TEXT NOT NULL,
	created     INTEGER NOT NULL,
	renewed     INTEGER NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('open','released','orphaned')),
	shared      INTEGER NOT NULL DEFAULT 0 -- D-042: overlaps other SHARED claims; never an exclusive one
);
CREATE UNIQUE INDEX IF NOT EXISTS claims_open_slug ON claims(slug) WHERE state='open';
CREATE TABLE IF NOT EXISTS claim_scopes (
	claim_id TEXT NOT NULL,
	scope    TEXT NOT NULL,
	folded   TEXT NOT NULL
);
-- Covering index for the per-claim scope lookups: claimsWhere reads scopes by
-- claim_id, and Claim (refresh) and Sweep delete by it. Without it every one
-- of those is a full scan of claim_scopes. IF NOT EXISTS is what puts it on a
-- ledger created before it existed: any ledger below schemaVersion re-runs
-- this whole script under Open's migration, and the index lands then.
CREATE INDEX IF NOT EXISTS claim_scopes_claim ON claim_scopes(claim_id, scope);
CREATE TABLE IF NOT EXISTS controls (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	kind    TEXT NOT NULL CHECK (kind IN ('pause')),
	target  TEXT NOT NULL,
	note    TEXT NOT NULL,
	created INTEGER NOT NULL,
	cleared INTEGER
);
CREATE TABLE IF NOT EXISTS inbox (
	msg_id  INTEGER PRIMARY KEY AUTOINCREMENT,
	target  TEXT NOT NULL,
	sender  TEXT NOT NULL,
	body    TEXT NOT NULL,
	created INTEGER NOT NULL,
	-- D-043. sender_session is the SENDING session, for ownership of a
	-- correction: '' is the operator at a bare terminal, NULL is unknown
	-- (every row sent before the column, and a send whose session could not
	-- be resolved); never --from. supersedes is the msg_id this one
	-- corrects, 0 for none.
	sender_session TEXT,
	supersedes     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS inbox_delivery (
	msg_id     INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	delivered  INTEGER NOT NULL,
	PRIMARY KEY (msg_id, session_id)
);
-- A broadcast addresses the sessions that are live WHEN it is sent. Without
-- this audience snapshot, target='all' matches every session created later
-- until a manual sweep removes the row: a one-line interjection becomes
-- recurring context for tomorrow's unrelated sessions.
CREATE TABLE IF NOT EXISTS inbox_recipient (
	msg_id     INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	PRIMARY KEY (msg_id, session_id)
);
CREATE TABLE IF NOT EXISTS dirty_paths (
	session_id TEXT NOT NULL,
	worktree   TEXT NOT NULL,
	path       TEXT NOT NULL,
	folded     TEXT NOT NULL,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	PRIMARY KEY (session_id, worktree, folded)
);
CREATE INDEX IF NOT EXISTS dirty_paths_folded ON dirty_paths(folded);
CREATE TABLE IF NOT EXISTS dirty_warned (
	session_id TEXT NOT NULL,
	worktree   TEXT NOT NULL,
	folded     TEXT NOT NULL,
	warned     INTEGER NOT NULL,
	PRIMARY KEY (session_id, worktree, folded)
);
-- One row per session: the last thing its own transcript said about how much
-- context it is carrying. An OBSERVATION, like dirty_paths — it reserves
-- nothing, refuses nothing, and a session that never reports one is reported
-- as unknown rather than as empty. No message text is ever stored here: the
-- columns are token counts, a model id and two timestamps, because everything
-- in this table is rendered into OTHER sessions' context windows.
--
-- declared_window is the operator's declaration (BUDDY_CONTEXT_WINDOW), 0 when
-- undeclared. It is not derived from the model: measured 2026-09-20, a session
-- running the 1M-token variant records the same model string as the 200k one,
-- so a percentage inferred from that string would be a fabrication.
CREATE TABLE IF NOT EXISTS session_context (
	session_id      TEXT PRIMARY KEY,
	incarnation     TEXT NOT NULL,
	observed        INTEGER NOT NULL,
	-- MILLISECONDS, and the only column in this ledger that is not whole
	-- Unix seconds. The write guard orders observations by this value, and
	-- at one-second resolution two turns inside one second compared EQUAL,
	-- the guard accepted the older one, and the roster reported a prompt
	-- size one turn stale (issue #7). Seconds are right for what they date
	-- here — a heartbeat, a claim, a pause — and wrong for the one column
	-- whose whole job is ordering two events that can share a second.
	turn_ms         INTEGER NOT NULL,
	model           TEXT NOT NULL,
	effort          TEXT NOT NULL DEFAULT '',
	prompt          INTEGER NOT NULL,
	cache_read      INTEGER NOT NULL,
	cache_write     INTEGER NOT NULL,
	output          INTEGER NOT NULL,
	declared_window INTEGER NOT NULL DEFAULT 0,
	-- The prompt-cache LIFETIME the turn wrote, as token counts per tier, raw
	-- from the transcript's usage.cache_creation: ephemeral_5m_input_tokens
	-- and ephemeral_1h_input_tokens. Counts and not a verdict, because the
	-- verdict ("hot") is a function of the clock and belongs to the reader:
	-- a cache written with the 1h tier is hot for an hour after the LAST
	-- request that used it, and the turn's own time above is the closest
	-- thing the ledger has to that. Both 0 means the harness recorded no
	-- tier, and the roster then says nothing about the cache.
	cache_5m        INTEGER NOT NULL DEFAULT 0,
	cache_1h        INTEGER NOT NULL DEFAULT 0,
	-- MILLISECONDS like turn_ms: the time of the record that WROTE the tier
	-- above, which is not always the turn the prompt counts came from (a pure
	-- cache read is the newest record; the writer is older). The tier columns
	-- are guarded by this clock, not by turn_ms, so two samples of the same
	-- turn cannot let older tier evidence overwrite newer.
	tier_ms         INTEGER NOT NULL DEFAULT 0
);
-- Turn state. A row means the session reported itself IDLE — its Stop hook
-- fired, the turn is over, and it is sitting at its prompt — and has run no
-- tool since. Beat deletes the row in its own transaction, because a tool
-- call IS a turn in progress.
--
-- NO ROW MEANS UNKNOWN, NEVER "busy". The Stop hook is a line in a settings
-- file that a machine may simply not have, exactly like the rest of them, so
-- a fleet with no Stop hook wired reports nobody idle — and a reader that
-- treated absence as evidence would conclude that every session on it is
-- mid-turn. The roster prints an idle row and says nothing about the others.
CREATE TABLE IF NOT EXISTS session_idle (
	session_id  TEXT PRIMARY KEY,
	incarnation TEXT NOT NULL,
	since       INTEGER NOT NULL
);
-- THE COMMIT A SESSION'S TREE WAS ON at the end of its last reported turn
-- (D-038, issue #28): git rev-parse HEAD in the Stop hook's cwd. An
-- OBSERVATION with an age (invariant 10), never a refusal. The lag behind
-- main is NOT stored: main moves without this session doing anything, so
-- "3 behind" is computed by the reader against main as it is when read.
-- turn_ms orders two samples as session_context's does.
CREATE TABLE IF NOT EXISTS session_base (
	session_id  TEXT PRIMARY KEY,
	incarnation TEXT NOT NULL,
	sha         TEXT NOT NULL,
	turn_ms     INTEGER NOT NULL
);
-- THE AUTHORITY WATCH LIST and its one-shot marks (D-028, issue #19). A
-- session's copy of the standing rules is a snapshot from session start;
-- these say which files are worth a Stat on every tool call, and which
-- (session, incarnation, file, mtime) has already been told. CLAUDE.md is
-- watched whether or not it is listed. The list lives here and not in git
-- config because the check runs on the beat hook, where a git fork is 7-9 ms
-- against a 100 ms budget and the ledger is already open.
CREATE TABLE IF NOT EXISTS authority (
	path   TEXT NOT NULL,
	folded TEXT NOT NULL PRIMARY KEY,
	added  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS authority_warned (
	session_id  TEXT NOT NULL,
	incarnation TEXT NOT NULL,
	folded      TEXT NOT NULL,
	-- NANOSECONDS: the file's mtime as the dedup key, so two writes in one
	-- second are two changes. A file restored to an OLDER mtime, or rewritten
	-- with the same one, is not detected — the advisory says so.
	mtime_ns    INTEGER NOT NULL,
	warned      INTEGER NOT NULL,
	PRIMARY KEY (session_id, incarnation, folded, mtime_ns)
);
-- THE IDENTIFIER REGISTER (D-029, issue #16): per space a CEILING, the
-- highest id known used or reserved, and the blocks handed out above it.
-- The register never parses prose to know what is taken, never reissues,
-- and does not know the artifact: a space must be SEEDED with a measured
-- high-water mark before anything is taken. Blocks outlive their session —
-- a reservation is a fact about the number line, not about a session's
-- life — so nothing here is swept or orphaned.
CREATE TABLE IF NOT EXISTS id_spaces (
	space   TEXT PRIMARY KEY,
	ceiling INTEGER NOT NULL,
	created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS id_blocks (
	block_id    INTEGER PRIMARY KEY AUTOINCREMENT,
	space       TEXT NOT NULL,
	lo          INTEGER NOT NULL,
	hi          INTEGER NOT NULL,
	session_id  TEXT NOT NULL,
	incarnation TEXT NOT NULL,
	label       TEXT NOT NULL,
	note        TEXT NOT NULL,
	taken       INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS dirty_scans (
	session_id TEXT NOT NULL,
	worktree   TEXT NOT NULL,
	scanned    INTEGER NOT NULL,
	PRIMARY KEY (session_id, worktree)
);
-- THE WAIT REGISTER (D-033, issue #26): what a session has DECLARED it is
-- waiting on, so that its own scheduled check can keep its prompt cache warm
-- and its inbox draining while it is parked. One row per session, open while
-- cleared IS NULL; re-declaring replaces it. It reserves nothing and refuses
-- nothing — no gate or claim reads it (invariant 10). internal/store/wait.go
-- carries the why.
--
-- No verdict column: LANDED and EXPIRED are computed from the clock and the
-- claims table at read time (D-020's rule). reason records what CLOSED the
-- row; told is beat's one-shot LANDED mark, set only after the write.
CREATE TABLE IF NOT EXISTS session_waits (
	session_id  TEXT PRIMARY KEY,
	incarnation TEXT NOT NULL,
	-- The declaration's own identity. Not since: whole seconds cannot tell a
	-- wait from its replacement declared in the same second (Codex design pass).
	decl        TEXT NOT NULL,
	since       INTEGER NOT NULL,
	deadline    INTEGER NOT NULL,
	note        TEXT NOT NULL,
	last_check  INTEGER NOT NULL DEFAULT 0,
	checks      INTEGER NOT NULL DEFAULT 0,
	told        INTEGER NOT NULL DEFAULT 0,
	cleared     INTEGER,
	reason      TEXT CHECK (reason IS NULL OR reason IN ('landed','expired','cleared','ended'))
);
-- The claims a declaration waits on, resolved ONCE to claim ids (D-013). The
-- slug is kept as declared for display only; landing is judged on the id.
CREATE TABLE IF NOT EXISTS session_wait_targets (
	decl        TEXT NOT NULL,
	session_id  TEXT NOT NULL,
	incarnation TEXT NOT NULL,
	ord         INTEGER NOT NULL,
	claim_id    TEXT NOT NULL,
	slug        TEXT NOT NULL,
	PRIMARY KEY (decl, claim_id)
);
CREATE INDEX IF NOT EXISTS session_wait_targets_session ON session_wait_targets(session_id);
`

// schemaVersion is stamped into PRAGMA user_version once the schema above and
// its backfills have been applied. Bump it whenever the schema string changes:
// a ledger below it re-runs the whole (IF NOT EXISTS) script under migrate; a
// ledger at it is opened without touching the write lock at all. Ledgers from
// before the stamp existed read 0 and migrate exactly once.
// 12 adds inbox.sender_session and inbox.supersedes (D-043), ALTER arms.
// 11 adds claims.shared (D-042), the second ALTER arm — see migrate.
// 10 adds session_base (D-038).
// 9 adds session_waits and session_wait_targets (D-033).
// 8 adds id_spaces and id_blocks (D-029).
// 7 adds authority and authority_warned (D-028).
// 6 adds sessions.terminal (an ALTER arm — see migrate) and session_procs.
// 5 adds cache_5m/cache_1h to session_context, again by DROPPING it (D-018:
// it is the one table that may be, because every row is re-derived at the
// next beat). 4 reshaped session_context (turn_ms, effort) the same way. 3 adds
// session_idle. 2 adds session_context. A new TABLE and not a column on sessions: the
// script below is all IF NOT EXISTS, which creates a missing table on an old
// ledger and cannot add a missing column to an existing one — a column would
// need an ALTER arm of its own, for a row that is not part of a session's
// identity and is absent for most of them.
const schemaVersion = 12

// Store wraps the ledger database. The clock is a seam; tests pin it.
type Store struct {
	db  *sql.DB
	now func() time.Time
	// betweenReads is a TEST SEAM: called by ClaimConflicts between its
	// conflict scan and its displacement scan, so a test can land a peer's
	// write there and prove the two scans see one snapshot. nil in
	// production.
	betweenReads func()
}

// Open opens (creating if needed) the ledger at dbPath. now==nil uses the wall clock.
//
// The version check is a plain read, deliberately OUTSIDE any transaction.
// Open used to begin a transaction for `CREATE TABLE IF NOT EXISTS` on every
// call, and the ledger opens with _txlock=immediate — so every open, including
// the read-only gate hook's, took the database WRITE lock. Uncontended that
// costs ~30 µs, which is nothing; the problem is SERIALIZATION, not latency: a
// gate opening behind a peer's Sweep or RetainDirty queues for up to
// busy_timeout, 5000 ms against a 100 ms hook budget. A ledger already at
// schemaVersion (every open after the first) now never asks for that lock.
func Open(dbPath string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	dsn := "file:" + dbPath + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect ledger schema version: %w", err)
	}
	if ver > schemaVersion {
		db.Close()
		return nil, newerLedger(dbPath, ver)
	}
	if ver < schemaVersion {
		if err := migrate(db); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, now: now}, nil
}

// ErrLedgerNewer is Open's refusal of a ledger stamped with a schema version
// ABOVE the one this binary was built with (issue #29).
//
// Open used to migrate only upward and accept anything else, so a stale
// binary — an old ~/bin/buddy, a build from another worktree, a hook pointing
// at an old path — opened a ledger a newer binary had migrated and used it as
// if it were its own shape. A query against a reshaped table fails loudly,
// but a write to a table whose MEANING changed while its columns did not
// succeeds, wrongly, and nothing says so.
//
// An error, not a read-only mode: to every caller this is "ledger exists but
// cannot be read", which the gate already DENIES (invariant 3). It must never
// collapse into "no ledger", the silent-allow arm. The fix is on the binary's
// side, never the ledger's: nothing migrates DOWN.
var ErrLedgerNewer = errors.New("ledger schema is newer than this buddy binary")

func newerLedger(dbPath string, ver int) error {
	return fmt.Errorf("%w: %s is at schema version %d and this binary understands up to %d; rebuild and reinstall buddy from a current checkout (hooks run whichever binary their line names)",
		ErrLedgerNewer, dbPath, ver, schemaVersion)
}

// migrate brings a ledger below schemaVersion up to it, in ONE transaction:
// the version re-check, the marker-table inspection, the schema, the backfill
// and the stamp all commit together or not at all. If the process dies part
// way, SQLite rolls all of it back and the next Open sees the old version and
// starts over; it can never mistake a half-finished migration for a complete
// one. The earlier shape inspected sqlite_master BEFORE beginning, which was
// not the atomicity its comment claimed: a peer could have created the marker
// table between the look and the lock.
//
// The version is re-read under the lock because two openers can both pass the
// unlocked check in Open: the second one to win BEGIN IMMEDIATE finds the
// first one's stamp and does nothing.
func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin ledger migration: %w", err)
	}
	defer tx.Rollback()
	var ver int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		return fmt.Errorf("inspect ledger schema version: %w", err)
	}
	// Re-checked under the lock: a newer binary can have migrated between
	// Open's unlocked read and this BEGIN, and returning nil here would hand
	// back a Store on a shape this binary does not know.
	if ver > schemaVersion {
		return newerLedger("the ledger", ver)
	}
	if ver == schemaVersion {
		return nil
	}
	// Detect the one migration that needs data backfill before the schema
	// creates its marker table. Existing broadcasts can be assigned to the
	// sessions whose recorded lifetime covered the send; sessions that started
	// later are deliberately excluded.
	hadRecipientTable := false
	var one int
	err = tx.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='inbox_recipient'`).Scan(&one)
	switch {
	case err == nil:
		hadRecipientTable = true
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("inspect ledger migration state: %w", err)
	}
	// The one table this ledger is allowed to throw away, and only because
	// of what it holds: a context observation is re-derived from the
	// session's own transcript on its next tool call, and a session with no
	// next tool call has a row nobody can act on anyway. Everything else here
	// is a record — claims, pauses, messages — and none of it may be dropped
	// to change a column. Ordered before the script so the CREATE below
	// rebuilds it in the new shape.
	if ver < 5 {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS session_context`); err != nil {
			return fmt.Errorf("migrate ledger (session_context): %w", err)
		}
	}
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("migrate ledger: %w", err)
	}
	// The first column ever added to an existing table. CREATE TABLE IF NOT
	// EXISTS above creates a fresh ledger's sessions table with the column
	// and does nothing to an old one, so an old ledger needs the ALTER — and
	// only once, which is what the table_info probe is for: ALTER has no IF
	// NOT EXISTS, and running it twice is an error that would wedge every
	// open below version 6.
	if ver < 6 {
		if err := addColumnIfMissing(tx, "sessions", "terminal", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// And the second (D-042). Every existing claim was taken exclusive, which
	// is what the default says, so no row changes meaning on the way up.
	if ver < 11 {
		if err := addColumnIfMissing(tx, "claims", "shared", "INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	// D-043. sender_session is added NULLABLE with no default, so every
	// message sent before it reads as owner UNKNOWN. A default of '' would
	// have made them the operator's, and a bare terminal could then correct a
	// message some session sent (Codex design pass, D-043).
	if ver < 12 {
		if err := addColumnIfMissing(tx, "inbox", "sender_session", "TEXT"); err != nil {
			return err
		}
		if err := addColumnIfMissing(tx, "inbox", "supersedes", "INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		// Here and not in the schema script: that script runs BEFORE these
		// ALTERs, and an index on a column an old ledger does not have yet
		// would fail the whole migration. Partial, because nearly every row
		// corrects nothing, and every drain asks "what corrects this one?".
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS inbox_supersedes ON inbox(supersedes) WHERE supersedes<>0`); err != nil {
			return fmt.Errorf("migrate ledger (inbox_supersedes): %w", err)
		}
		// `buddy sent` with no id lists one sender's newest; off the hook
		// path, but a scan of every message ever sent to print "none" is not.
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS inbox_sender ON inbox(sender_session, msg_id)`); err != nil {
			return fmt.Errorf("migrate ledger (inbox_sender): %w", err)
		}
	}
	if !hadRecipientTable {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO inbox_recipient (msg_id, session_id)
			SELECT m.msg_id, s.session_id FROM inbox m JOIN sessions s
				ON s.started<=m.created AND (s.ended IS NULL OR s.ended>=m.created)
			WHERE m.target='all'`); err != nil {
			return fmt.Errorf("backfill broadcast recipients: %w", err)
		}
	}
	// PRAGMA takes no bound parameters; the value is a compile-time constant.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("stamp ledger schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ledger migration: %w", err)
	}
	return nil
}

// addColumnIfMissing is the ALTER arm a column on an existing table needs.
func addColumnIfMissing(tx *sql.Tx, table, column, decl string) error {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, decl)); err != nil {
		return fmt.Errorf("migrate ledger (%s.%s): %w", table, column, err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func newToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NormalizeScope canonicalizes a repo-relative scope path, rejecting anything
// that could escape the repo or alias another scope by spelling.
func NormalizeScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "", errors.New("empty scope")
	}
	if strings.HasPrefix(scope, "/") {
		return "", fmt.Errorf("scope %q is absolute; scopes are repo-relative", scope)
	}
	clean := path.Clean(strings.ReplaceAll(scope, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("scope %q escapes the repo", scope)
	}
	if clean == "." {
		return "", fmt.Errorf("scope %q claims the whole repo; name a path", scope)
	}
	return strings.TrimSuffix(clean, "/"), nil
}

// fold canonicalizes for comparison: NFC (macOS stores NFD) then lower-case
// (APFS default is case-insensitive).
func fold(s string) string { return strings.ToLower(norm.NFC.String(s)) }

// Fold canonicalizes a value for comparison under the ledger's own rule.
//
// Exported for the same reason as SamePath: a caller that builds part of a KEY
// this package stores must fold it the way this package folds everything else.
// worktreeKey used plain ToLower while every path beside it in the same row was
// ToLower(NFC(...)), so one repo reached through an NFD-spelled path produced
// two distinct worktree keys — and two keys means the peers query finds nobody
// and the notice silently never fires.
func Fold(s string) string { return fold(s) }

// SamePath reports whether two repo-relative paths name the same file under
// the ledger's own folding rule.
//
// Exported because callers outside the store compare paths against values the
// store keyed — and a comparison that folds differently from the thing it is
// comparing against is a mismatch with a green light: strings.EqualFold looks
// equivalent and is not, since it does not normalize, so an NFD spelling of an
// accented filename would answer "different file" about one row and "same
// file" about another. One rule, one function.
func SamePath(a, b string) bool { return fold(a) == fold(b) }

// scopesOverlap reports whether two folded scopes cover a common path:
// equal, or one is a directory prefix of the other.
func scopesOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

// scopeCovers reports whether folded scope covers folded rel path.
func scopeCovers(scope, rel string) bool {
	return scope == rel || strings.HasPrefix(rel, scope+"/")
}

// SessionInfo mirrors a sessions row.
type SessionInfo struct {
	SessionID   string
	Incarnation string
	Label       string
	Worktree    string
	PID         int    // the harness process last registered, 0 when unbound (see session_procs)
	Terminal    string // "<provider>:<id>" reported at registration, "" when none
	Started     time.Time
	LastSeen    time.Time
	Ended       time.Time // zero if live
}

// ProcRef names a process well enough to tell a live one from a recycled
// pid: the pid, and the process's start time as the kernel reports it (any
// unit, compared for equality only). Born 0 means "not recorded", and a
// caller's liveness test must then fall back to the pid alone.
type ProcRef struct {
	PID  int
	Born int64
}

func (si SessionInfo) Live() bool { return si.Ended.IsZero() }

// Hello registers or refreshes a session with no process registration and no
// terminal handle: the shape every caller had before D-025, kept for them.
// The pid is stored as it always was (diagnostic) and binds nothing.
func (s *Store) Hello(sessionID, label, worktree string, pid int) (SessionInfo, error) {
	return s.helloFrom(sessionID, label, worktree, pid, ProcRef{}, "")
}

// HelloFrom registers or refreshes a session AND registers the harness
// process it arrived from. A new session or one previously ended gets a
// fresh incarnation; a live one is refreshed in place.
//
// proc is the process the hook was spawned by (ProcRef{} when the caller
// could not establish one, which registers nothing — see session_procs).
// terminal is the handle the environment reported, "" for none.
func (s *Store) HelloFrom(sessionID, label, worktree string, proc ProcRef, terminal string) (SessionInfo, error) {
	return s.helloFrom(sessionID, label, worktree, proc.PID, proc, terminal)
}

func (s *Store) helloFrom(sessionID, label, worktree string, pid int, proc ProcRef, terminal string) (SessionInfo, error) {
	var out SessionInfo
	err := s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		var inc string
		var ended sql.NullInt64
		err := tx.QueryRow(`SELECT incarnation, ended FROM sessions WHERE session_id=?`, sessionID).Scan(&inc, &ended)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			inc = newToken()
			if label == "" {
				label = defaultLabel(worktree, sessionID)
			}
			_, err = tx.Exec(`INSERT INTO sessions (session_id, incarnation, label, worktree, pid, terminal, started, last_seen)
				VALUES (?,?,?,?,?,?,?,?)`, sessionID, inc, label, worktree, pid, terminal, now, now)
			if err != nil {
				return err
			}
		case err != nil:
			return err
		case ended.Valid:
			// Orphan the old incarnation's open claims BEFORE reviving:
			// once ended is cleared below, orphanEnded's ended-IS-NOT-NULL
			// subquery no longer matches this session, and the claims would
			// stay open under an earlier incarnation — unreleasable past the
			// incarnation fence and skipped by sweep (owner looks live).
			if _, err := tx.Exec(`UPDATE claims SET state='orphaned', renewed=? WHERE state='open' AND session_id=?`,
				now, sessionID); err != nil {
				return err
			}
			inc = newToken()
			// The terminal is REPLACED, even by "": a revived session is a new
			// registration, and a pane the old one sat in must not survive
			// into one that reported none (Codex design pass, D-025).
			if _, err := tx.Exec(`UPDATE sessions SET incarnation=?, worktree=?, pid=?, terminal=?, started=?, last_seen=?, ended=NULL,
				label=COALESCE(NULLIF(?,''), label) WHERE session_id=?`,
				inc, worktree, pid, terminal, now, now, label, sessionID); err != nil {
				return err
			}
			// The dead incarnation's registrations go with it. Every one of
			// them is dead: a session is ended only once no registered
			// process is alive (ByeFrom) or by the incarnation-fenced Bye.
			if _, err := tx.Exec(`DELETE FROM session_procs WHERE session_id=?`, sessionID); err != nil {
				return err
			}
		default:
			// An empty label means "keep the one you have" (labels are pause/msg
			// targets and must not drift under a session — see defaultLabel), so
			// NULLIF turns it into NULL and COALESCE falls through to the stored
			// value. One fixed statement replaces two hand-assembled SET clauses
			// that appended `, label=?` conditionally.
			//
			// The pid and the terminal are kept when the refresh brings none. A
			// hand-run `buddy hello --session X` on a live, hook-registered
			// session used to overwrite the pid with its own hook's; the Codex
			// design pass named the manual refresh as one of four ways a
			// delayed bye got back in (D-025). What actually BINDS is
			// session_procs, which a refresh never touches; the column is the
			// roster's "last registered process" and must not read 0 for a
			// session that is bound.
			if _, err := tx.Exec(`UPDATE sessions SET last_seen=?, worktree=?,
				pid=CASE WHEN ?=0 THEN pid ELSE ? END,
				terminal=COALESCE(NULLIF(?,''), terminal),
				label=COALESCE(NULLIF(?,''), label) WHERE session_id=?`,
				now, worktree, pid, pid, terminal, label, sessionID); err != nil {
				return err
			}
		}
		if err := registerProc(tx, sessionID, proc, now); err != nil {
			return err
		}
		// Housekeeping every session start: claims of ended sessions become
		// orphaned here (Bye no longer does it — see Bye), and their waits
		// are closed as ended with them.
		if err := cleanupEnded(tx, now); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT session_id, incarnation, label, worktree, pid, terminal, started, last_seen, COALESCE(ended,0)
			FROM sessions WHERE session_id=?`, sessionID).Scan(
			&out.SessionID, &out.Incarnation, &out.Label, &out.Worktree, &out.PID, &out.Terminal,
			unixScan{&out.Started}, unixScan{&out.LastSeen}, unixScan{&out.Ended})
	})
	return out, err
}

// registerProc records that proc is one of the session's live processes. A
// zero proc registers nothing. Keyed by pid: a re-registration of the same
// pid (every beat brings one) refreshes the start time, so a recycled pid
// that reached the same session id — a stretch, but free to handle — carries
// the born of the process that is actually there.
func registerProc(tx *sql.Tx, sessionID string, proc ProcRef, now int64) error {
	if proc.PID == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO session_procs (session_id, pid, born, registered) VALUES (?,?,?,?)
		ON CONFLICT(session_id, pid) DO UPDATE SET born=excluded.born, registered=excluded.registered`,
		sessionID, proc.PID, proc.Born, now)
	return err
}

// defaultLabel names a session that gave no --label: the worktree's basename
// plus a session-id prefix, so a chat line or claim listing says WHERE it is
// working and WHICH session it is without a ledger lookup (a bare s-xxxxxxxx
// told the operator nothing). Minted once at first hello and stable after —
// labels are pause/msg targets, so they must not drift under a session.
func defaultLabel(worktree, sessionID string) string {
	id := strings.ReplaceAll(sessionID, "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	base := path.Base(strings.TrimSuffix(worktree, "/"))
	if base == "." || base == "/" || base == "" {
		return "s-" + id
	}
	return base + "/s-" + id
}

// Bye marks a session ended — and does NOTHING to claims. It deliberately
// does not orphan them: orphaning happens in Hello/Sweep/Claim, where it is
// idempotent and keyed to rows still ended when they run, so a bye that is
// wrong about WHICH incarnation it ends is recoverable (the next hello
// reopens) where an inline orphan would not be. A non-empty incarnation
// fences the update; "" ends whichever incarnation is live.
//
// This is the incarnation-fenced end, for a caller that HAS an incarnation
// (tests, and anything that resolved the session first). The SessionEnd hook
// has none and goes through ByeFrom, which is fenced by the registered
// PROCESSES instead — the fence "" here used to short-circuit, and that was
// the delayed-bye defect (D-025). The registrations are cleared with the end:
// an ended session has no live process by definition of this call, and a row
// left behind would refuse the next incarnation's bye.
func (s *Store) Bye(sessionID, incarnation string) error {
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE sessions SET ended=? WHERE session_id=? AND ended IS NULL AND (incarnation=? OR ?='')`,
			s.now().Unix(), sessionID, incarnation, incarnation)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		_, err = tx.Exec(`DELETE FROM session_procs WHERE session_id=?`, sessionID)
		return err
	})
}

// ByeResult says what ByeFrom did, and why not.
type ByeResult struct {
	Known    bool      // a live row existed for the session
	Ended    bool      // it was ended
	Blocking []ProcRef // registered processes still alive that kept it open
	Stranger bool      // from names a process the session never registered
}

// ByeFrom ends a session ON BEHALF OF the process `from`, and refuses while
// any OTHER registered process is still alive. This is the SessionEnd hook's
// end, and the fence that closes the delayed-bye defect (D-025):
//
//   - the session's registrations are read; from's own is removed (it is
//     exiting, that is what a bye means);
//   - every other registration is asked `alive`; a dead one is pruned;
//   - if any is alive and force is false, the session is NOT ended and the
//     result names them — a delayed bye from a dead incarnation, or a
//     passenger process exiting first, leaves the live process registered
//     and live (the caller's own row and the pruned dead ones ARE gone: that
//     is the bookkeeping, and it commits);
//   - otherwise the session is ended and its registrations cleared.
//
// "MINE" IS PID AND BIRTH TIME, not pid alone (Codex code pass, D-025). A
// hook that captured its anchor, stalled, and outlived its parent could
// see that pid recycled and re-registered by a replacement process; on pid
// alone the old bye would recognise the replacement's row as its own,
// delete it without ever asking `alive`, and end the session under a live
// process — the defect by yet another road. A recorded birth time that
// differs is a different process, and the caller is then a stranger.
//
// A session with NO registrations is unbound and ends as it always did: a
// hand-run hello registers nothing, an old ledger has no rows, and neither
// may make the hook stop working. from.PID == 0 (a hand-run `buddy bye <id>`)
// removes nothing and is refused by any live registration like a stranger,
// which is why the manual verb carries --force.
//
// alive is a seam: internal/store never inspects processes. The caller
// answers with the pid AND the recorded start time, so a recycled pid reads
// as dead; an alive answer it cannot establish should be true, because the
// cost of a wrong "alive" is a session that stays live until the operator
// looks, and the cost of a wrong "dead" is this defect.
func (s *Store) ByeFrom(sessionID string, from ProcRef, alive func(ProcRef) bool, force bool) (ByeResult, error) {
	var res ByeResult
	err := s.tx(func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM sessions WHERE session_id=? AND ended IS NULL`, sessionID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		res.Known = true
		rows, err := tx.Query(`SELECT pid, born FROM session_procs WHERE session_id=? ORDER BY pid`, sessionID)
		if err != nil {
			return err
		}
		var regs []ProcRef
		for rows.Next() {
			var p ProcRef
			if err := rows.Scan(&p.PID, &p.Born); err != nil {
				rows.Close()
				return err
			}
			regs = append(regs, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		mine := false
		for _, p := range regs {
			if from.PID != 0 && p.PID == from.PID && (from.Born == 0 || p.Born == 0 || p.Born == from.Born) {
				mine = true
			} else if alive(p) {
				res.Blocking = append(res.Blocking, p)
				continue
			}
			if _, err := tx.Exec(`DELETE FROM session_procs WHERE session_id=? AND pid=?`, sessionID, p.PID); err != nil {
				return err
			}
		}
		res.Stranger = from.PID != 0 && !mine && len(regs) > 0
		if len(res.Blocking) > 0 && !force {
			return nil
		}
		if _, err := tx.Exec(`UPDATE sessions SET ended=? WHERE session_id=? AND ended IS NULL`, s.now().Unix(), sessionID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM session_procs WHERE session_id=?`, sessionID); err != nil {
			return err
		}
		res.Ended = true
		return nil
	})
	return res, err
}

// SessionProcs returns every registered process, by session id.
func (s *Store) SessionProcs() (map[string][]ProcRef, error) {
	rows, err := s.db.Query(`SELECT session_id, pid, born FROM session_procs ORDER BY session_id, registered, pid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]ProcRef{}
	for rows.Next() {
		var id string
		var p ProcRef
		if err := rows.Scan(&id, &p.PID, &p.Born); err != nil {
			return nil, err
		}
		out[id] = append(out[id], p)
	}
	return out, rows.Err()
}

// orphanEnded orphans open claims whose owner session is ended, stamping
// renewed so a later TTL delete cannot fire in the same pass.
func orphanEnded(tx *sql.Tx, now int64) (int, error) {
	res, err := tx.Exec(`UPDATE claims SET state='orphaned', renewed=? WHERE state='open' AND session_id IN
		(SELECT session_id FROM sessions WHERE ended IS NOT NULL)`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Beat refreshes last_seen for a live session, and renews the session's own
// claims that cover relPath (repo-relative; empty renews nothing). A beat for
// an ended session is ignored — it must not resurrect one.
//
// It also clears the session's idle mark, IN THIS TRANSACTION rather than in
// a second one: a tool call is a turn in progress, so the heartbeat and the
// end of idleness are one fact, and splitting them would buy a second write
// lock per tool call for a row that is usually not there.
func (s *Store) Beat(sessionID, relPath string) error {
	return s.BeatFrom(sessionID, relPath, ProcRef{})
}

// BeatFrom is Beat that also registers the process the hook arrived from
// (see session_procs), so a session whose hello registered nothing — a
// hand-run one, or one that predates the table — is bound by its first
// hook-driven tool call, and a second process on the same id registers
// itself the moment it acts. A zero proc registers nothing. The pid column
// is filled in only when it is 0: it is the LAST registered process, and a
// beat must not flip it between two passengers on every tool call.
func (s *Store) BeatFrom(sessionID, relPath string, proc ProcRef) error {
	return s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		res, err := tx.Exec(`UPDATE sessions SET last_seen=?, pid=CASE WHEN pid=0 THEN ? ELSE pid END
			WHERE session_id=? AND ended IS NULL`, now, proc.PID, sessionID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // ended, or unknown: nothing of this session's to clear
		}
		if err := registerProc(tx, sessionID, proc, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM session_idle WHERE session_id=?`, sessionID); err != nil {
			return err
		}
		if relPath == "" {
			return nil
		}
		rel := fold(relPath)
		rows, err := tx.Query(`SELECT c.claim_id, cs.folded FROM claims c JOIN claim_scopes cs ON cs.claim_id=c.claim_id
			WHERE c.session_id=? AND c.state='open'`, sessionID)
		if err != nil {
			return err
		}
		renew := map[string]bool{}
		for rows.Next() {
			var id, scope string
			if err := rows.Scan(&id, &scope); err != nil {
				rows.Close()
				return err
			}
			if scopeCovers(scope, rel) {
				renew[id] = true
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for id := range renew {
			if _, err := tx.Exec(`UPDATE claims SET renewed=? WHERE claim_id=?`, now, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClaimInfo is a claim joined with its owner and scopes.
type ClaimInfo struct {
	ClaimID     string
	Incarnation string // the incarnation that TOOK the claim (not the owner's current one)
	Slug        string
	Desc        string
	State       string
	Scopes      []string
	Created     time.Time
	Renewed     time.Time
	Owner       SessionInfo
	// Shared marks a claim other SHARED claims may overlap (D-042). Every
	// listing prints it, because a reader deciding whether to contest a path
	// needs to know the holder invited company.
	Shared bool
}

// Stale reports whether an open claim has gone unrenewed past StaleAfter.
func (c ClaimInfo) Stale(now time.Time) bool {
	return c.State == "open" && now.Sub(c.Renewed) > StaleAfter
}

// ErrRefused is returned when a claim cannot be taken; it names the conflict.
type ErrRefused struct {
	Slug     string
	Scope    string // the requested scope that overlapped ("" for slug conflicts)
	Their    string // the conflicting scope
	Claimant string // owner label (session label)
	// Renewed is when the holding claim was last renewed, so the CLI can say
	// the holder has gone quiet (issue #18). It rides the error and not just
	// Conflict because cmdClaim rebuilds the set from this type on the refusal
	// path — without it the dry run would annotate staleness and the refusal
	// it forecasts would not, which is the disagreement allConflicts exists to
	// prevent. Zero means not recorded and must never render as STALE.
	Renewed time.Time
	// Shared is the HOLDER's mode (D-042): a scope held shared refused this
	// request only because the request was exclusive, and the refusal says so.
	Shared bool
	// More is the REST of the conflict set. The named fields carry the first
	// conflict, as they always have; a four-path claim with two collisions
	// used to report one and cost a second round trip to learn the other
	// (wishlist §5b). Error() prints the first and counts the rest, because it
	// is rendered on one fenced line; a caller that wants the set prints More.
	More []ErrRefused
}

func (e ErrRefused) Error() string {
	var msg string
	if e.Scope == "" {
		msg = fmt.Sprintf("slug %q is already claimed by %s", e.Slug, e.Claimant)
	} else {
		held := "held"
		if e.Shared {
			held = "held SHARED" // D-042: this holder refused only an exclusive request
		}
		msg = fmt.Sprintf("scope %q overlaps %q %s by %s (slug %q)", e.Scope, e.Their, held, e.Claimant, e.Slug)
	}
	if n := len(e.More); n > 0 {
		// "run --dry-run to see the set" was true until the refusal began
		// printing the set itself (D-022): the advice now sends the reader to
		// re-run a command for output that is already two lines above.
		msg += fmt.Sprintf(" — and %d more conflict(s), listed above", n)
	}
	return msg
}

// Claim takes (or, for the same session re-claiming its own slug, refreshes)
// an EXCLUSIVE claim. All scopes land atomically or not at all.
func (s *Store) Claim(sessionID, incarnation, slug, desc string, scopes []string) error {
	return s.ClaimMode(sessionID, incarnation, slug, desc, scopes, false)
}

// ClaimMode is Claim with the mode named (D-042). A SHARED claim may overlap
// other shared claims and nothing else; an exclusive one overlaps nothing.
// A refresh takes the mode it is given, so re-claiming a shared slug without
// --shared makes it exclusive again, and is refused if a peer shares it.
func (s *Store) ClaimMode(sessionID, incarnation, slug, desc string, scopes []string, shared bool) error {
	norm, err := claimScopes(scopes, shared)
	if err != nil {
		return err
	}
	return s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()

		var live int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND incarnation=? AND ended IS NULL`,
			sessionID, incarnation).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return fmt.Errorf("session %s (incarnation %s) is not live; run buddy hello first", sessionID, incarnation)
		}

		// A HOLDER THAT HAS SAID BYE CANNOT REFUSE (D-026, issue #15). The
		// same idempotent orphaning hello and sweep run, here first, in this
		// transaction: a session that finished, exited cleanly and still held
		// two scopes blocked a peer with green work for hours, until some
		// other session happened to start. This is invariant 12's "orphaning
		// happens in hello/sweep, never inline in bye" with one more of the
		// former; `ended` is positive evidence and bye still touches no claim
		// row. The conflict scan below excludes ended owners for the dry
		// run's sake, so this is what keeps an ended holder's row from being
		// left OPEN beside the new claim — two open rows over one scope,
		// which the gate would then adjudicate against the new holder.
		if err := cleanupEnded(tx, now); err != nil {
			return err
		}

		// Somebody else's slug, and overlap with any OTHER session's open
		// scopes, as ONE set (conflicts.go): the refusal names every collision
		// and not just the first, and it is the same computation the dry run
		// makes, so a forecast cannot disagree with the refusal it predicts.
		// The comparison runs on the folded column; the refusal reports the
		// scope AS CLAIMED, because that is what `buddy ls` prints and what the
		// peer typed — an operator told they overlap "src/api" went looking for
		// a claim on "Src/API" and found no such line.
		conflicts, err := s.allConflicts(tx, sessionID, slug, norm, shared)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return refusedFrom(conflicts)
		}
		// Own open slug: a refresh, below.
		ownID, _, _, err := openSlugOwner(tx, slug)
		if err != nil {
			return err
		}

		if ownID != "" { // refresh own claim
			if _, err := tx.Exec(`UPDATE claims SET descr=?, renewed=?, incarnation=?, shared=? WHERE claim_id=?`,
				desc, now, incarnation, shared, ownID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM claim_scopes WHERE claim_id=?`, ownID); err != nil {
				return err
			}
			return insertScopes(tx, ownID, norm)
		}

		id := newToken()
		if _, err := tx.Exec(`INSERT INTO claims (claim_id, session_id, incarnation, slug, descr, created, renewed, state, shared)
			VALUES (?,?,?,?,?,?,?,'open',?)`, id, sessionID, incarnation, slug, desc, now, now, shared); err != nil {
			return err
		}
		return insertScopes(tx, id, norm)
	})
}

// rowQuerier is what openSlugOwner needs from either a *sql.Tx (Claim, inside
// its transaction) or a *sql.DB (ResolveTarget, outside one).
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// openSlugOwner answers "who holds this slug open?": the claim id and its
// owning session, or taken=false. At most one row can match, because
// claims_open_slug is UNIQUE over state='open'. It is the ONE spelling of that
// query: Claim and ResolveTarget each wrote their own with the columns in a
// different order, and two copies of a rule drift — a change to what "open"
// means (say, excluding orphaned rows) would have had to be found twice.
func openSlugOwner(q rowQuerier, slug string) (claimID, sessionID string, taken bool, err error) {
	err = q.QueryRow(`SELECT claim_id, session_id FROM claims WHERE slug=? AND state='open'`, slug).Scan(&claimID, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return claimID, sessionID, true, nil
}

func insertScopes(tx *sql.Tx, claimID string, scopes []string) error {
	for _, sc := range scopes {
		if _, err := tx.Exec(`INSERT INTO claim_scopes (claim_id, scope, folded) VALUES (?,?,?)`,
			claimID, sc, fold(sc)); err != nil {
			return err
		}
	}
	return nil
}

// labelOf takes a rowQuerier, not a *sql.Tx, so the dry run (ClaimConflicts,
// outside a transaction) and the refusal (Claim, inside one) name holders the
// same way.
func (s *Store) labelOf(q rowQuerier, sessionID string) (string, error) {
	var label string
	err := q.QueryRow(`SELECT label FROM sessions WHERE session_id=?`, sessionID).Scan(&label)
	if err != nil {
		return sessionID, err
	}
	return label, nil
}

// Release releases the session's own open claim.
//
// It is fenced by incarnation, as Claim is. A session id alone is an ABA:
// resolve identity, then the session byes, re-hellos (a fresh incarnation) and
// re-takes the same slug, and an id-only release would hand back a claim the
// new incarnation had just made. The fence costs nothing in the legitimate
// case — an open claim always carries its owner's current incarnation, because
// the only path that mints a new one (Hello over an ended session) orphans the
// old incarnation's open claims in the same transaction.
func (s *Store) Release(sessionID, incarnation, slug string) error {
	_, err := s.ReleaseID(sessionID, incarnation, slug)
	return err
}

// ReleaseID is Release that also returns the id of the claim it released,
// read inside the release's own transaction. `release` names the sessions
// that declared a wait on the claim it closed (D-033), and an id looked up
// BEFORE the release could name a different claim than the one released —
// another process of the same session releasing and re-taking the slug in
// between (Codex code pass).
func (s *Store) ReleaseID(sessionID, incarnation, slug string) (string, error) {
	var claimID string
	err := s.tx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT claim_id FROM claims WHERE slug=? AND session_id=? AND incarnation=? AND state='open'`,
			slug, sessionID, incarnation).Scan(&claimID)
		if errors.Is(err, sql.ErrNoRows) {
			return s.whyNotReleased(tx, sessionID, slug)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE claims SET state='released', renewed=? WHERE claim_id=?`, s.now().Unix(), claimID)
		return err
	})
	if err != nil {
		return "", err
	}
	return claimID, nil
}

// ErrNoRelease says why a release did nothing.
//
// The single message this replaced — "no open claim %q held by this session" —
// collapsed four different truths into one sentence, and the two that matter
// most are opposites: a claim you ALREADY HANDED BACK and a claim SOMEONE ELSE
// HOLDS need opposite next actions, and the caller cannot tell them apart from
// outside. That was acute while identity itself was inferred, because "held by
// this session" is unfalsifiable when you cannot be sure which session you are
// — but it is wrong on its own merits, and stays wrong now that identity is
// known.
type ErrNoRelease struct {
	Slug     string
	Exists   bool          // a row with this slug exists at all
	State    string        // its state, when Exists
	Claimant string        // owner label, when Exists
	Yours    bool          // owned by the releasing session id
	Since    time.Duration // since it was last touched
}

func (e ErrNoRelease) Error() string {
	switch {
	case !e.Exists:
		return fmt.Sprintf("no claim %q in this ledger — `buddy ls --all` lists closed ones too", e.Slug)
	case e.State != "open":
		whose := "it was " + e.Claimant + "'s"
		if e.Yours {
			whose = "yours"
		}
		return fmt.Sprintf("claim %q is already %s (%s, %s ago) — nothing to release",
			e.Slug, e.State, whose, humanAge(e.Since))
	case !e.Yours:
		return fmt.Sprintf("claim %q is open and held by %s, not you — ask them to release it, or `buddy sweep --force` if they are gone",
			e.Slug, e.Claimant)
	default:
		// A DIFFERENT incarnation, not an earlier one: the caller's is the
		// one that may be stale. A release delayed across a bye and a hello
		// arrives with the OLD incarnation while the open claim belongs to
		// the new one, and "earlier" named the wrong side of that.
		return fmt.Sprintf("claim %q is open under a DIFFERENT incarnation of this session (it ended and re-registered since); it is not this incarnation's to release",
			e.Slug)
	}
}

// whyNotReleased diagnoses a release that matched no rows, inside the release's
// own transaction so the answer cannot race the update it explains.
func (s *Store) whyNotReleased(tx *sql.Tx, sessionID, slug string) error {
	e := ErrNoRelease{Slug: slug}
	var owner string
	var renewed int64
	// At most one row can be open for a slug (the partial unique index), but
	// closed rows repeat, so prefer the open one and otherwise the newest.
	err := tx.QueryRow(`SELECT session_id, state, renewed FROM claims WHERE slug=?
		ORDER BY state='open' DESC, renewed DESC LIMIT 1`, slug).Scan(&owner, &e.State, &renewed)
	if errors.Is(err, sql.ErrNoRows) {
		return e
	}
	if err != nil {
		return err
	}
	e.Exists = true
	e.Yours = owner == sessionID
	e.Claimant, _ = s.labelOf(tx, owner)
	e.Since = s.now().Sub(time.Unix(renewed, 0))
	return e
}

// humanAge renders a duration the way the CLI renders claim ages.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Claims lists claims, open-only unless includeClosed.
func (s *Store) Claims(includeClosed bool) ([]ClaimInfo, error) {
	where := `WHERE c.state='open'`
	if includeClosed {
		where = ``
	}
	return s.claimsWhere(where)
}

// claimsWhere lists claims matching a WHERE clause over c (claims) and ses
// (sessions), with scopes attached.
//
// Two queries, not 1+N: the scopes of every matching claim come back in one
// statement keyed by the SAME where clause, then attach in Go. The earlier
// shape issued one scope query per claim, and each one was a full scan of
// claim_scopes until claim_scopes_claim existed. Scopes stay sorted by scope
// within a claim, as the per-claim query sorted them — listings and the
// commit gate's output are compared by eye and by tests.
func (s *Store) claimsWhere(where string, args ...any) ([]ClaimInfo, error) {
	rows, err := s.db.Query(`SELECT c.claim_id, c.incarnation, c.slug, c.descr, c.state, c.created, c.renewed, c.shared,
			ses.session_id, ses.incarnation, ses.label, ses.worktree, ses.pid, ses.started, ses.last_seen, COALESCE(ses.ended,0)
		FROM claims c JOIN sessions ses ON ses.session_id=c.session_id `+where+` ORDER BY c.created`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaimInfo
	for rows.Next() {
		var c ClaimInfo
		if err := rows.Scan(&c.ClaimID, &c.Incarnation, &c.Slug, &c.Desc, &c.State, unixScan{&c.Created}, unixScan{&c.Renewed}, &c.Shared,
			&c.Owner.SessionID, &c.Owner.Incarnation, &c.Owner.Label, &c.Owner.Worktree, &c.Owner.PID,
			unixScan{&c.Owner.Started}, unixScan{&c.Owner.LastSeen}, unixScan{&c.Owner.Ended}); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	srows, err := s.db.Query(`SELECT cs.claim_id, cs.scope FROM claim_scopes cs WHERE cs.claim_id IN
			(SELECT c.claim_id FROM claims c JOIN sessions ses ON ses.session_id=c.session_id `+where+`)
		ORDER BY cs.claim_id, cs.scope`, args...)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	scopes := make(map[string][]string, len(out))
	for srows.Next() {
		var id, sc string
		if err := srows.Scan(&id, &sc); err != nil {
			return nil, err
		}
		scopes[id] = append(scopes[id], sc)
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Scopes = scopes[out[i].ClaimID]
	}
	return out, nil
}

// OwnerOf returns the open claim of ANOTHER session that stops relPath being
// edited, if any. This runs on the gate hot path (every mutating tool call),
// so it scans one scopes query instead of materializing every claim with its
// owner (the old 1+N shape), and loads the single matching claim only on a hit.
//
// SHARED CLAIMS (D-042). A path under another session's EXCLUSIVE claim is
// held, as it always was, and an exclusive holder is returned in preference
// to a shared one, so a shared hold can never hide an exclusive blocker. A
// path under only SHARED claims of others is held unless the caller holds a
// claim of its own covering the path: a shared claim is an invitation to
// claim alongside, not an open door, so the ledger still records everyone
// editing. The caller's own claim counts in either mode because an exclusive
// one cannot overlap a peer's shared one across sessions (the conflict scan
// refuses it both ways); requiring "shared" here would be a guard no reachable
// state arms. It must COVER the path: a claim on rules/section does not
// admit an edit to rules itself.
func (s *Store) OwnerOf(relPath, excludeSession string) (ClaimInfo, bool, error) {
	rel := fold(relPath)
	rows, err := s.db.Query(`SELECT cs.folded, cs.claim_id, c.session_id, c.shared FROM claim_scopes cs
		JOIN claims c ON c.claim_id=cs.claim_id WHERE c.state='open'`)
	if err != nil {
		return ClaimInfo{}, false, err
	}
	defer rows.Close()
	exclusive, shared, mine := "", "", false
	for rows.Next() {
		var folded, id, owner string
		var sh bool
		if err := rows.Scan(&folded, &id, &owner, &sh); err != nil {
			return ClaimInfo{}, false, err
		}
		if !scopeCovers(folded, rel) {
			continue
		}
		switch {
		case owner == excludeSession && excludeSession != "":
			mine = true
		case !sh && exclusive == "":
			exclusive = id
		case sh && shared == "":
			shared = id
		}
	}
	if err := rows.Err(); err != nil {
		return ClaimInfo{}, false, err
	}
	claimID := exclusive
	if claimID == "" && !mine {
		claimID = shared
	}
	if claimID == "" {
		return ClaimInfo{}, false, nil
	}
	claims, err := s.claimsWhere(`WHERE c.claim_id=?`, claimID)
	if err != nil || len(claims) == 0 {
		return ClaimInfo{}, false, err
	}
	return claims[0], true, nil
}

// SessionByID returns the session row for id, if it exists. A point query for
// hot paths; Sessions() stays the listing verb.
func (s *Store) SessionByID(sessionID string) (SessionInfo, bool, error) {
	var si SessionInfo
	err := s.db.QueryRow(`SELECT session_id, incarnation, label, worktree, pid, terminal, started, last_seen, COALESCE(ended,0)
		FROM sessions WHERE session_id=?`, sessionID).Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree,
		&si.PID, &si.Terminal, unixScan{&si.Started}, unixScan{&si.LastSeen}, unixScan{&si.Ended})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionInfo{}, false, nil
	}
	if err != nil {
		return SessionInfo{}, false, err
	}
	return si, true, nil
}

// SweepOpts selects what a sweep does beyond its always-on work.
type SweepOpts struct {
	// Force also orphans open claims whose owner has been silent past
	// forceAfter — the operator's explicit act (invariant 11).
	Force bool
	// DryRun performs the WHOLE sweep inside its transaction and rolls it
	// back, so the result is exactly what a real run would have done. It is
	// not a second computation of the same predicates: `claim --dry-run`
	// shares its conflict math with the refusal for the reason this shares
	// the sweep with itself — a forecast that re-derives the answer
	// differently drifts from it the first time somebody edits one WHERE
	// clause and forgets the other (issue #23, where `sweep --dry-run` was an
	// unrecognised flag that ran a second real sweep and printed a count
	// that looked exactly like a forecast). A rolled-back transaction leaves
	// the ledger file as it was.
	DryRun bool
}

// SweepResult is what a sweep did — or, with DryRun, would have done.
type SweepResult struct {
	Orphaned int
	Deleted  int
	// OrphanedClaims names every claim this pass moved from open to
	// orphaned. Orphaning is the one act of a sweep that changes what the
	// gate refuses (the scope is free the moment the row flips), so it is
	// the one act that is named row by row; deleting released rows past ttl
	// is bookkeeping and stays a count. Slug and label are peer text: the
	// caller fences them.
	OrphanedClaims []SweepOrphan
}

// SweepOrphan is one claim a sweep orphaned, with the session that held it.
type SweepOrphan struct {
	Slug      string
	Label     string
	SessionID string
}

// errDryRun is the sentinel a dry-run sweep returns from inside its
// transaction so that tx rolls back; Sweep swallows it. It never escapes.
var errDryRun = errors.New("dry run: rolled back")

// Sweep: orphan open claims of ended sessions; delete released/orphaned rows
// past ttl (orphaning stamps renewed, so a fresh orphan is never deleted in
// the same pass); GC delivered-or-aged inbox rows. With opts.Force,
// additionally orphan open claims whose owner session has been silent past
// forceAfter — an explicit operator action: a session that only thinks/reads
// does not move last_seen, which is why plain sweep never does this. With
// opts.DryRun, all of it is rolled back and only the report survives.
func (s *Store) Sweep(ttl, forceAfter time.Duration, opts SweepOpts) (SweepResult, error) {
	var r SweepResult
	if ttl <= 0 || forceAfter <= 0 {
		return r, fmt.Errorf("sweep ttl/forceAfter must be positive (got %v, %v)", ttl, forceAfter)
	}
	err := s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		// The open set going in. Whatever is orphaned coming out and was in
		// it is what THIS pass orphaned — named without repeating either
		// orphaning predicate, so a change to one cannot be missed here.
		wasOpen, err := openClaimIDs(tx)
		if err != nil {
			return err
		}
		n, err := orphanEnded(tx, now)
		if err != nil {
			return err
		}
		r.Orphaned += n
		// Waits of ended sessions close with their claims (D-033). A LIVE
		// session's wait is never closed here, --force or not: it is not a
		// reservation, and it expires by its own deadline.
		if err := clearEndedWaits(tx, now); err != nil {
			return err
		}

		if opts.Force {
			res, err := tx.Exec(`UPDATE claims SET state='orphaned', renewed=? WHERE state='open' AND session_id IN
				(SELECT session_id FROM sessions WHERE ended IS NULL AND last_seen < ?)`, now, now-int64(forceAfter.Seconds()))
			if err != nil {
				return err
			}
			m, _ := res.RowsAffected()
			r.Orphaned += int(m)
		}
		if r.OrphanedClaims, err = orphanedFrom(tx, wasOpen); err != nil {
			return err
		}

		cutoff := now - int64(ttl.Seconds())
		if _, err := tx.Exec(`DELETE FROM claim_scopes WHERE claim_id IN
			(SELECT claim_id FROM claims WHERE state IN ('released','orphaned') AND renewed < ?)`, cutoff); err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM claims WHERE state IN ('released','orphaned') AND renewed < ?`, cutoff)
		if err != nil {
			return err
		}
		m, _ := res.RowsAffected()
		r.Deleted = int(m)

		// Inbox GC: messages past ttl are dropped with their delivery marks.
		if _, err := tx.Exec(`DELETE FROM inbox_delivery WHERE msg_id IN
				(SELECT msg_id FROM inbox WHERE created < ?)`, cutoff); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM inbox_recipient WHERE msg_id IN
				(SELECT msg_id FROM inbox WHERE created < ?)`, cutoff); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM inbox WHERE created < ?`, cutoff); err != nil {
			return err
		}
		if err := sweepDirty(tx, now, cutoff); err != nil {
			return err
		}
		if opts.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return r, nil
	}
	if err != nil {
		return SweepResult{}, err
	}
	return r, nil
}

// openClaimIDs is the set of claim ids in state 'open' right now.
func openClaimIDs(tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT claim_id FROM claims WHERE state='open'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// orphanedFrom names the orphaned claims whose id is in wasOpen, in slug
// order, each with its holder's label.
func orphanedFrom(tx *sql.Tx, wasOpen map[string]bool) ([]SweepOrphan, error) {
	rows, err := tx.Query(`SELECT c.claim_id, c.slug, s.label, c.session_id FROM claims c
		JOIN sessions s ON s.session_id = c.session_id
		WHERE c.state='orphaned' ORDER BY c.slug, c.claim_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SweepOrphan
	for rows.Next() {
		var id string
		var o SweepOrphan
		if err := rows.Scan(&id, &o.Slug, &o.Label, &o.SessionID); err != nil {
			return nil, err
		}
		if wasOpen[id] {
			out = append(out, o)
		}
	}
	return out, rows.Err()
}

// Pause records a pause control against a RESOLVED target. It stores the
// canonical session id, which is why PausedFor below needs no change: its
// (session_id, label, "all") match already covers the id arm.
func (s *Store) Pause(t Target, note string) error {
	if err := t.check(); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO controls (kind, target, note, created) VALUES ('pause',?,?,?)`,
		t.ID, note, s.now().Unix())
	return err
}

// Resume clears pause controls for the target.
//
// It matches the resolved id, the operator's raw argument, AND the resolved
// label, because rows written before targets were resolved carry whatever was
// typed — commonly a label — and the operator resuming need not use the same
// alias they paused with. PausedFor still honours those rows (its label arm),
// so a resume that missed them would strand a live pause that the gate keeps
// enforcing and no verb can lift. Clearing by label is safe here only because
// ResolveTarget refuses a label held by more than one session.
func (s *Store) Resume(t Target) (int, error) {
	if err := t.check(); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`UPDATE controls SET cleared=? WHERE kind='pause' AND target IN (?, ?, ?) AND cleared IS NULL`,
		s.now().Unix(), t.ID, t.Raw, t.Label)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PausedFor returns the newest uncleared pause note matching the session
// (by id, label, or "all").
func (s *Store) PausedFor(sessionID, label string) (string, bool, error) {
	var note string
	err := s.db.QueryRow(`SELECT note FROM controls WHERE kind='pause' AND cleared IS NULL
		AND target IN (?, ?, 'all') ORDER BY id DESC LIMIT 1`, sessionID, label).Scan(&note)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return note, true, nil
}

// BroadcastKeep is the maximum useful life of a fleet-wide interjection. A
// direct message remains durable until delivery or sweep; a broadcast is a
// statement to the fleet that existed when it was sent, not standing context
// for a session that has been idle for days.
const BroadcastKeep = 24 * time.Hour

// Msg queues a message against a RESOLVED target, sent by the operator (no
// session), correcting nothing. It is Send with the zero options; the CLI's
// send goes through Send with the sending session named.
func (s *Store) Msg(t Target, from, body string) error {
	_, err := s.Send(t, from, body, SendOpts{SenderKnown: true})
	return err
}

// InboxMsg is one queued message, with its correction links (D-043) as they
// stand for the session it is being delivered to.
type InboxMsg struct {
	ID      int64
	From    string
	Body    string
	Created time.Time
	// Supersedes is the message this one corrects (0 = none), and
	// SupersededBy every later message that corrects this one, ascending.
	Supersedes   int64
	SupersededBy []int64
	// For a correction: whether the recipient's copy of the original has a
	// recorded delivery (and when), or expired undelivered.
	OrigDelivered time.Time
	OrigExpired   bool
}

// Undelivered returns messages addressed to the session (by id or label), plus
// unexpired broadcasts whose recipient snapshot contains it, not yet marked
// delivered, oldest first.
func (s *Store) Undelivered(sessionID, label string) ([]InboxMsg, error) {
	// `m.target<>'all'` on the direct arm: nothing reserves the label "all",
	// and a session wearing it matched EVERY broadcast here, past both the
	// recipient snapshot and the 24h keep. That predates D-043, but a
	// correction's audience guarantee rests on the snapshot being the only
	// way into a broadcast (Codex code pass, D-043).
	rows, err := s.db.Query(`SELECT m.msg_id, m.sender, m.body, m.created, m.supersedes FROM inbox m
			WHERE ((m.target<>'all' AND m.target IN (?, ?)) OR (m.target='all' AND m.created>=? AND EXISTS
				(SELECT 1 FROM inbox_recipient r WHERE r.msg_id=m.msg_id AND r.session_id=?)))
			AND NOT EXISTS (SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=?)
			ORDER BY m.msg_id`, sessionID, label, s.now().Add(-BroadcastKeep).Unix(), sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxMsg
	for rows.Next() {
		var m InboxMsg
		if err := rows.Scan(&m.ID, &m.From, &m.Body, unixScan{&m.Created}, &m.Supersedes); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, s.links(sessionID, out)
}

// MarkDelivered records delivery of msgs to the session. Called only after
// the caller has successfully written them out (at-least-once delivery).
func (s *Store) MarkDelivered(sessionID string, ids []int64) error {
	return s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		for _, id := range ids {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO inbox_delivery (msg_id, session_id, delivered) VALUES (?,?,?)`,
				id, sessionID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// SessionOrder is the key Sessions sorts on. It is a typed constant and not
// the caller's string, for the reason Target is a type: the value reaches an
// ORDER BY, and a column name assembled from an argument is a guard the
// compiler cannot hold. The two queries below are written out in full rather
// than concatenated from a fragment, so there is no spelling of this argument
// that produces a third one.
type SessionOrder int

const (
	// ByLastSeen dates a session by the last hook that spoke for it — "who
	// is working now". The default: it is the question a human at a terminal
	// is usually asking.
	ByLastSeen SessionOrder = iota
	// ByStarted dates it by THIS INCARNATION's registration — "in what order
	// did these arrive". A revived session (Hello on an ended row) restarts
	// that clock on purpose: identity here is (session_id, incarnation), and
	// the revived one is a new session wearing an old id.
	ByStarted
)

// Sessions lists every session, live ones first, newest of the chosen key
// first within each group.
//
// BOTH KEYS SORT DESCENDING. A flag that changed the key and the direction
// together would be two changes under one name, and it buys nothing: under
// DESC "the session that started just after mine" is the line ABOVE mine,
// exactly as adjacent as it would be below under ASC.
//
// session_id BREAKS TIES because both keys are whole seconds. Four sessions
// spawned by one script share a second, and with no tiebreak SQLite may order
// them differently on each call — a listing that reshuffles between two reads
// cannot answer "the next one after me" at all, which is the question the
// started key exists for.
//
// The live/ended partition survives both keys: an ended session is not a
// candidate for anything, and interleaving 300 of them with the live rows
// would cost the listing its first purpose to serve its second.
func (s *Store) Sessions(order SessionOrder) ([]SessionInfo, error) {
	const (
		bySeen = `SELECT session_id, incarnation, label, worktree, pid, terminal, started, last_seen, COALESCE(ended,0)
		FROM sessions ORDER BY (ended IS NOT NULL), last_seen DESC, session_id`
		byStarted = `SELECT session_id, incarnation, label, worktree, pid, terminal, started, last_seen, COALESCE(ended,0)
		FROM sessions ORDER BY (ended IS NOT NULL), started DESC, session_id`
	)
	q := bySeen
	if order == ByStarted {
		q = byStarted
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree, &si.PID, &si.Terminal,
			unixScan{&si.Started}, unixScan{&si.LastSeen}, unixScan{&si.Ended}); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// Session returns the session with this id, live or ended. Callers that reject
// an id need the difference: "never said hello here" and "said bye" have
// different remedies, and recommending the wrong one is worse than saying
// nothing.
//
// Deprecated: it is SessionByID under an older name — the same contract, and
// it now delegates rather than loading every session and scanning in Go. Kept
// so existing callers compile; new code should call SessionByID.
func (s *Store) Session(sessionID string) (SessionInfo, bool, error) {
	return s.SessionByID(sessionID)
}

// ResolveSessions returns EVERY live session whose registered worktree contains
// cwd, most recently seen first, and separately the total number of live
// sessions in the ledger.
//
// It deliberately returns all the matches rather than the first. N sessions
// sharing one checkout are indistinguishable by directory, so picking the
// newest heartbeat attributes a caller's work to whoever pinged last — which
// for a claim inverts the invariant the claim exists to state.
//
// liveTotal is what lets a caller tell "this directory names the only session
// there is" from "this directory names one of several". Only the first is
// evidence: a matching worktree says where a session was REGISTERED, never
// where the caller is now, so with other sessions live it is a correlation, not
// an identification.
func (s *Store) ResolveSessions(cwd string) (matching []SessionInfo, liveTotal int, err error) {
	sessions, err := s.Sessions(ByLastSeen)
	if err != nil {
		return nil, 0, err
	}
	cf := fold(strings.TrimSuffix(cwd, "/"))
	for _, si := range sessions {
		if !si.Live() {
			continue
		}
		liveTotal++
		wf := fold(strings.TrimSuffix(si.Worktree, "/"))
		if cf == wf || strings.HasPrefix(cf, wf+"/") {
			matching = append(matching, si)
		}
	}
	return matching, liveTotal, nil
}

func (s *Store) tx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// unixScan scans a unix-seconds integer into a time.Time (0 → zero time).
type unixScan struct{ t *time.Time }

func (u unixScan) Scan(v any) error {
	n, ok := v.(int64)
	if !ok {
		return fmt.Errorf("expected int64 unix time, got %T", v)
	}
	if n == 0 {
		*u.t = time.Time{}
		return nil
	}
	*u.t = time.Unix(n, 0)
	return nil
}

// ---- context samples ----

// ContextSample is the last thing a session's own transcript said about how
// much context it is carrying. internal/cli/transcript.go is where the
// numbers come from and what they deliberately do not contain: no message
// text of any kind, and no window size.
type ContextSample struct {
	Incarnation string    // the incarnation the observation belongs to
	Observed    time.Time // when a beat read the transcript
	TurnAt      time.Time // the turn's OWN timestamp: the age of the NUMBER
	Model       string
	Effort      string // the reasoning effort that turn ran at; "" when unrecorded
	Prompt      int64  // everything the model was handed: input + cache read + cache write
	CacheRead   int64
	CacheWrite  int64
	Output      int64
	Window      int64 // operator-DECLARED window in tokens; 0 = undeclared
	// Cache5m and Cache1h are the tokens the turn WROTE into the prompt cache
	// at each lifetime tier, raw from the transcript. Both zero: the harness
	// recorded no tier and nothing about the cache is printed.
	Cache5m int64
	Cache1h int64
	TierAt  time.Time // the writer's own time; zero when no tier was recorded
}

// RecordContext stores one observation, for the incarnation that OBSERVED it.
//
// THE CALLER NAMES THAT INCARNATION, and it is checked here against the
// ledger's own in the same transaction. Reading it here instead — the first
// shape of this, and wrong — says only "whoever is live now", so a beat that
// read a transcript, lost its session to a bye and a revival, and then
// arrived would have stamped the NEW incarnation with the OLD one's number.
// The caller reads the incarnation BEFORE it reads the transcript, so a
// mismatch here means a revival happened in between and the sample is
// dropped. A missing, ended or superseded session is a silent no-op, exactly
// like a beat that arrives after bye.
//
// AN OLDER TURN NEVER REPLACES A NEWER ONE. Two beats can reach this out of
// order, and re-reading the same tail after a failed capture would otherwise
// re-stamp an old turn as freshly observed. The guard is on turn_at, not on
// observed, because turn_at is the fact and observed is only when we looked.
// It is stored in MILLISECONDS for that comparison alone (see the schema): at
// one-second resolution two turns inside one second compared equal and the
// older one won.
//
// ONE ROW PER SESSION, overwritten. An append-only history would answer
// different questions (growth curves, compaction frequency) at 334 sessions x
// one row per tool call; the routing decision needs the latest observation
// and its age. The row lives exactly as long as the sessions row it hangs
// off — nothing deletes sessions, so nothing needs a sweep arm here, and an
// ended session keeps the last thing known about it.
func (s *Store) RecordContext(sessionID, incarnation string, c ContextSample) error {
	if incarnation == "" {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		var inc string
		err := tx.QueryRow(`SELECT incarnation FROM sessions WHERE session_id=? AND ended IS NULL`, sessionID).Scan(&inc)
		if errors.Is(err, sql.ErrNoRows) || inc != incarnation {
			return nil
		}
		if err != nil {
			return err
		}
		// THE TIER HAS ITS OWN GUARD. The row-level rule (a newer or equal
		// turn replaces) orders the prompt counts by the turn that produced
		// them. The tier comes from a different record — the newest WRITER
		// — so two samples of the SAME turn can carry different tier
		// evidence (a writer appended between two reads of the tail), and
		// under the row rule alone the later write wins whatever its
		// evidence is. The CASE keeps whichever tier evidence is newer by
		// ITS clock, and a fresh incarnation takes the new row whole.
		tierMS := int64(0)
		if !c.TierAt.IsZero() {
			tierMS = c.TierAt.UnixMilli()
		}
		_, err = tx.Exec(`INSERT INTO session_context
			(session_id, incarnation, observed, turn_ms, model, effort, prompt, cache_read, cache_write, output, declared_window, cache_5m, cache_1h, tier_ms)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET
				incarnation=excluded.incarnation, observed=excluded.observed, turn_ms=excluded.turn_ms,
				model=excluded.model, effort=excluded.effort, prompt=excluded.prompt,
				cache_read=excluded.cache_read, cache_write=excluded.cache_write,
				output=excluded.output, declared_window=excluded.declared_window,
				cache_5m=CASE WHEN excluded.incarnation<>session_context.incarnation OR excluded.tier_ms>=session_context.tier_ms
					THEN excluded.cache_5m ELSE session_context.cache_5m END,
				cache_1h=CASE WHEN excluded.incarnation<>session_context.incarnation OR excluded.tier_ms>=session_context.tier_ms
					THEN excluded.cache_1h ELSE session_context.cache_1h END,
				tier_ms=CASE WHEN excluded.incarnation<>session_context.incarnation OR excluded.tier_ms>=session_context.tier_ms
					THEN excluded.tier_ms ELSE session_context.tier_ms END
			WHERE excluded.incarnation<>session_context.incarnation
				OR excluded.turn_ms>=session_context.turn_ms`,
			sessionID, inc, c.Observed.Unix(), c.TurnAt.UnixMilli(), c.Model, c.Effort,
			c.Prompt, c.CacheRead, c.CacheWrite, c.Output, c.Window, c.Cache5m, c.Cache1h, tierMS)
		return err
	})
}

// ContextSamples returns the current observation per session id, each
// carrying the incarnation it belongs to.
//
// The join on incarnation is the read side of the same rule: a snapshot taken
// by a superseded incarnation is not this session's context and must not be
// reported as it. It is hidden rather than deleted, because a read path that
// writes is a read path that can fail, and the next beat overwrites it anyway.
//
// The incarnation comes back to the CALLER as well, because this is a second
// query: a caller that listed the sessions a moment ago holds rows that a
// revival in between has already superseded, and the join can only make this
// query self-consistent, never the pair of them. Compare before you print.
func (s *Store) ContextSamples() (map[string]ContextSample, error) {
	rows, err := s.db.Query(`SELECT c.session_id, c.incarnation, c.observed, c.turn_ms, c.model, c.effort,
			c.prompt, c.cache_read, c.cache_write, c.output, c.declared_window, c.cache_5m, c.cache_1h, c.tier_ms
		FROM session_context c JOIN sessions s ON s.session_id=c.session_id AND s.incarnation=c.incarnation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ContextSample{}
	for rows.Next() {
		var id string
		var c ContextSample
		var turnMS, tierMS int64
		if err := rows.Scan(&id, &c.Incarnation, unixScan{&c.Observed}, &turnMS, &c.Model, &c.Effort,
			&c.Prompt, &c.CacheRead, &c.CacheWrite, &c.Output, &c.Window, &c.Cache5m, &c.Cache1h, &tierMS); err != nil {
			return nil, err
		}
		if tierMS > 0 {
			c.TierAt = time.UnixMilli(tierMS)
		}
		// Not unixScan: this column is milliseconds, for the reason the
		// schema gives, and reading it as seconds would date every sample to
		// the year 57000.
		c.TurnAt = time.UnixMilli(turnMS)
		out[id] = c
	}
	return out, rows.Err()
}

// Base is the commit a session's tree was on when its last reported turn
// ended (D-038).
type Base struct {
	Incarnation string
	SHA         string
	TurnAt      time.Time
}

// RecordBase stores a session's HEAD as the Stop hook saw it, fenced like
// RecordContext: only the current incarnation of a live session, and only a
// turn at least as new as the one already recorded, so a delayed Stop cannot
// roll the base back to a tree the session has since left.
func (s *Store) RecordBase(sessionID, incarnation, sha string, turnAt time.Time) error {
	if incarnation == "" || sha == "" {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		var inc string
		err := tx.QueryRow(`SELECT incarnation FROM sessions WHERE session_id=? AND ended IS NULL`, sessionID).Scan(&inc)
		if errors.Is(err, sql.ErrNoRows) || inc != incarnation {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO session_base (session_id, incarnation, sha, turn_ms) VALUES (?,?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET
				incarnation=excluded.incarnation, sha=excluded.sha, turn_ms=excluded.turn_ms
			WHERE excluded.incarnation<>session_base.incarnation
				OR excluded.turn_ms>=session_base.turn_ms`,
			sessionID, incarnation, sha, turnAt.UnixMilli())
		return err
	})
}

// Bases returns each session's recorded base, joined on incarnation for the
// reason ContextSamples is: a predecessor's tree is not this session's.
// The incarnation comes back so a caller holding an earlier session list
// can compare before printing.
func (s *Store) Bases() (map[string]Base, error) {
	rows, err := s.db.Query(`SELECT b.session_id, b.incarnation, b.sha, b.turn_ms FROM session_base b
		JOIN sessions s ON s.session_id=b.session_id AND s.incarnation=b.incarnation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Base{}
	for rows.Next() {
		var id string
		var b Base
		var turnMS int64
		if err := rows.Scan(&id, &b.Incarnation, &b.SHA, &turnMS); err != nil {
			return nil, err
		}
		b.TurnAt = time.UnixMilli(turnMS)
		out[id] = b
	}
	return out, rows.Err()
}

// ---- turn state ----

// IdleState is one session's report that it is waiting at its prompt.
type IdleState struct {
	Incarnation string
	Since       time.Time
}

// MarkIdle records that the session has finished a turn and is waiting for
// its operator, dated by `at` — the END of the turn that just finished, which
// the caller reads from the transcript. A zero `at` means the caller could
// not establish it and the write time is used instead, which is what this
// did for everything before issue #11.
//
// Ended or unknown session: a silent no-op, like every other late hook.
//
// THE INCARNATION IS READ HERE, unlike RecordContext next door, and the
// difference is the point. A context sample is read from a file BEFORE the
// write, so the incarnation that observed it has to be carried in or the
// sample can land on a successor. This observation is MADE at the moment of
// the call — "whoever is live right now just stopped" is exactly the claim —
// so the ledger's own current row is the correct answer, not a stale one.
//
// A repeated Stop with no tool call in between moves `since` forward, because
// it is a new turn ending: the session became idle again, later.
//
// THE EVENT TIME IS WHAT FENCES THE INCARNATION. The hook payload names no
// incarnation, so this reads whichever one is live — and a Stop delayed
// across a bye, a hello and a beat would then mark the NEW incarnation idle
// on the OLD one's turn, correctly tagged and simply untrue (issue #11). A
// turn that ended before this incarnation registered cannot be this
// incarnation's turn, so `at < started` is refused. With no event time the
// check cannot run and the old behaviour stands: written, and cleared by the
// next beat. Degrading rather than refusing is deliberate — refusing on an
// unreadable transcript would turn the feature off silently, which is the
// failure this project keeps finding.
func (s *Store) MarkIdle(sessionID string, at time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		var inc string
		var started time.Time
		err := tx.QueryRow(`SELECT incarnation, started FROM sessions WHERE session_id=? AND ended IS NULL`,
			sessionID).Scan(&inc, unixScan{&started})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		since := s.now()
		if !at.IsZero() {
			if at.Before(started) {
				return nil // an earlier incarnation's turn, arriving late
			}
			since = at
		}
		_, err = tx.Exec(`INSERT INTO session_idle (session_id, incarnation, since) VALUES (?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET incarnation=excluded.incarnation, since=excluded.since`,
			sessionID, inc, since.Unix())
		return err
	})
}

// ClearIdle retracts an idle mark: the session is working again.
//
// `beat` does this inside its own transaction because a tool call IS a turn
// in progress. This is the same retraction for the turn that runs no tool at
// all — a plain text answer clears nothing, so the row kept saying "idle
// since the previous turn" for as long as the model was writing (issue #10)
// — and it is driven by the optional UserPromptSubmit hook.
//
// No incarnation check and no liveness check, deliberately: deleting an
// observation can only ever RETRACT a claim about a session, never assert
// one, so the worst a misdirected clear can do is cost the next Stop a
// repeat. The asymmetry is the same one the whole table runs on.
func (s *Store) ClearIdle(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM session_idle WHERE session_id=?`, sessionID)
	return err
}

// IdleSessions returns the sessions that have reported themselves idle, by
// session id, each carrying the incarnation that reported it.
//
// Joined on the current incarnation for the reason ContextSamples is, and
// returning the incarnation for the reason ContextSamples does: this is a
// second query, and a caller holding session rows read a moment ago must
// compare rather than trust two queries to agree.
//
// Live only, unlike ContextSamples: a footprint is a fact about a session
// that remains true after it ends, and "waiting for work" is not. Without
// the predicate, a session that ended between a caller's two queries would
// be printed idle off the earlier snapshot — available, and gone.
func (s *Store) IdleSessions() (map[string]IdleState, error) {
	rows, err := s.db.Query(`SELECT i.session_id, i.incarnation, i.since FROM session_idle i
		JOIN sessions s ON s.session_id=i.session_id AND s.incarnation=i.incarnation
		WHERE s.ended IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]IdleState{}
	for rows.Next() {
		var id string
		var st IdleState
		if err := rows.Scan(&id, &st.Incarnation, unixScan{&st.Since}); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
