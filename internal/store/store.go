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
	ended       INTEGER
);
CREATE TABLE IF NOT EXISTS claims (
	claim_id    TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	incarnation TEXT NOT NULL,
	slug        TEXT NOT NULL,
	descr       TEXT NOT NULL,
	created     INTEGER NOT NULL,
	renewed     INTEGER NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('open','released','orphaned'))
);
CREATE UNIQUE INDEX IF NOT EXISTS claims_open_slug ON claims(slug) WHERE state='open';
CREATE TABLE IF NOT EXISTS claim_scopes (
	claim_id TEXT NOT NULL,
	scope    TEXT NOT NULL,
	folded   TEXT NOT NULL
);
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
	created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS inbox_delivery (
	msg_id     INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	delivered  INTEGER NOT NULL,
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
CREATE TABLE IF NOT EXISTS dirty_scans (
	session_id TEXT NOT NULL,
	worktree   TEXT NOT NULL,
	scanned    INTEGER NOT NULL,
	PRIMARY KEY (session_id, worktree)
);
`

// Store wraps the ledger database. The clock is a seam; tests pin it.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the ledger at dbPath. now==nil uses the wall clock.
func Open(dbPath string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	dsn := "file:" + dbPath + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate ledger: %w", err)
	}
	return &Store{db: db, now: now}, nil
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
	PID         int
	Started     time.Time
	LastSeen    time.Time
	Ended       time.Time // zero if live
}

func (si SessionInfo) Live() bool { return si.Ended.IsZero() }

// Hello registers or refreshes a session. A new session or one previously
// ended gets a fresh incarnation; a live one is refreshed in place.
func (s *Store) Hello(sessionID, label, worktree string, pid int) (SessionInfo, error) {
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
			_, err = tx.Exec(`INSERT INTO sessions (session_id, incarnation, label, worktree, pid, started, last_seen)
				VALUES (?,?,?,?,?,?,?)`, sessionID, inc, label, worktree, pid, now, now)
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
			set := `incarnation=?, worktree=?, pid=?, started=?, last_seen=?, ended=NULL`
			args := []any{inc, worktree, pid, now, now}
			if label != "" {
				set += `, label=?`
				args = append(args, label)
			}
			args = append(args, sessionID)
			if _, err := tx.Exec(`UPDATE sessions SET `+set+` WHERE session_id=?`, args...); err != nil {
				return err
			}
		default:
			set := `last_seen=?, worktree=?, pid=?`
			args := []any{now, worktree, pid}
			if label != "" {
				set += `, label=?`
				args = append(args, label)
			}
			args = append(args, sessionID)
			if _, err := tx.Exec(`UPDATE sessions SET `+set+` WHERE session_id=?`, args...); err != nil {
				return err
			}
		}
		// Housekeeping every session start: claims of ended sessions become
		// orphaned here (Bye no longer does it — see Bye).
		if _, err := orphanEnded(tx, now); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT session_id, incarnation, label, worktree, pid, started, last_seen, COALESCE(ended,0)
			FROM sessions WHERE session_id=?`, sessionID).Scan(
			&out.SessionID, &out.Incarnation, &out.Label, &out.Worktree, &out.PID,
			unixScan{&out.Started}, unixScan{&out.LastSeen}, unixScan{&out.Ended})
	})
	return out, err
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

// Bye marks a session ended — and does NOTHING else. It deliberately does not
// orphan claims: the SessionEnd hook cannot know its incarnation, so a delayed
// bye racing a re-hello of the same session_id could otherwise orphan a LIVE
// incarnation's work (Codex P1 finding). Marking ended is recoverable (the
// next hello reopens); orphaning happens in Hello/Sweep, where it is
// idempotent and keyed to rows still ended when they run. A non-empty
// incarnation fences the update.
func (s *Store) Bye(sessionID, incarnation string) error {
	return s.tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET ended=? WHERE session_id=? AND ended IS NULL AND (incarnation=? OR ?='')`,
			s.now().Unix(), sessionID, incarnation, incarnation)
		return err
	})
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
func (s *Store) Beat(sessionID, relPath string) error {
	return s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		res, err := tx.Exec(`UPDATE sessions SET last_seen=? WHERE session_id=? AND ended IS NULL`, now, sessionID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 || relPath == "" {
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
}

func (e ErrRefused) Error() string {
	if e.Scope == "" {
		return fmt.Sprintf("slug %q is already claimed by %s", e.Slug, e.Claimant)
	}
	return fmt.Sprintf("scope %q overlaps %q held by %s (slug %q)", e.Scope, e.Their, e.Claimant, e.Slug)
}

// Claim takes (or, for the same session re-claiming its own slug, refreshes)
// a claim. All scopes land atomically or not at all.
func (s *Store) Claim(sessionID, incarnation, slug, desc string, scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("a claim needs at least one --scope")
	}
	norm := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		n, err := NormalizeScope(sc)
		if err != nil {
			return err
		}
		norm = append(norm, n)
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

		// Slug conflict?
		var ownID, ownSession string
		err := tx.QueryRow(`SELECT claim_id, session_id FROM claims WHERE slug=? AND state='open'`, slug).Scan(&ownID, &ownSession)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && ownSession != sessionID {
			label, _ := s.labelOf(tx, ownSession)
			return ErrRefused{Slug: slug, Claimant: label}
		}

		// Overlap with any OTHER session's open scopes?
		rows, err := tx.Query(`SELECT cs.folded, c.slug, c.session_id FROM claim_scopes cs
			JOIN claims c ON c.claim_id=cs.claim_id WHERE c.state='open' AND c.session_id<>?`, sessionID)
		if err != nil {
			return err
		}
		type held struct{ folded, slug, session string }
		var theirs []held
		for rows.Next() {
			var h held
			if err := rows.Scan(&h.folded, &h.slug, &h.session); err != nil {
				rows.Close()
				return err
			}
			theirs = append(theirs, h)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, n := range norm {
			nf := fold(n)
			for _, h := range theirs {
				if scopesOverlap(nf, h.folded) {
					label, _ := s.labelOf(tx, h.session)
					return ErrRefused{Slug: h.slug, Scope: n, Their: h.folded, Claimant: label}
				}
			}
		}

		if ownID != "" { // refresh own claim
			if _, err := tx.Exec(`UPDATE claims SET descr=?, renewed=?, incarnation=? WHERE claim_id=?`,
				desc, now, incarnation, ownID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM claim_scopes WHERE claim_id=?`, ownID); err != nil {
				return err
			}
			return insertScopes(tx, ownID, norm)
		}

		id := newToken()
		if _, err := tx.Exec(`INSERT INTO claims (claim_id, session_id, incarnation, slug, descr, created, renewed, state)
			VALUES (?,?,?,?,?,?,?,'open')`, id, sessionID, incarnation, slug, desc, now, now); err != nil {
			return err
		}
		return insertScopes(tx, id, norm)
	})
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

func (s *Store) labelOf(tx *sql.Tx, sessionID string) (string, error) {
	var label string
	err := tx.QueryRow(`SELECT label FROM sessions WHERE session_id=?`, sessionID).Scan(&label)
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
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE claims SET state='released', renewed=? WHERE slug=? AND session_id=? AND incarnation=? AND state='open'`,
			s.now().Unix(), slug, sessionID, incarnation)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return s.whyNotReleased(tx, sessionID, slug)
		}
		return nil
	})
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
		return fmt.Sprintf("claim %q is open under an EARLIER incarnation of this session (it ended and re-registered since); it is not this incarnation's to release",
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
func (s *Store) claimsWhere(where string, args ...any) ([]ClaimInfo, error) {
	rows, err := s.db.Query(`SELECT c.claim_id, c.incarnation, c.slug, c.descr, c.state, c.created, c.renewed,
			ses.session_id, ses.incarnation, ses.label, ses.worktree, ses.pid, ses.started, ses.last_seen, COALESCE(ses.ended,0)
		FROM claims c JOIN sessions ses ON ses.session_id=c.session_id `+where+` ORDER BY c.created`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaimInfo
	for rows.Next() {
		var c ClaimInfo
		if err := rows.Scan(&c.ClaimID, &c.Incarnation, &c.Slug, &c.Desc, &c.State, unixScan{&c.Created}, unixScan{&c.Renewed},
			&c.Owner.SessionID, &c.Owner.Incarnation, &c.Owner.Label, &c.Owner.Worktree, &c.Owner.PID,
			unixScan{&c.Owner.Started}, unixScan{&c.Owner.LastSeen}, unixScan{&c.Owner.Ended}); err != nil {
			return nil, err
		}
		srows, err := s.db.Query(`SELECT scope FROM claim_scopes WHERE claim_id=? ORDER BY scope`, c.ClaimID)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var sc string
			if err := srows.Scan(&sc); err != nil {
				srows.Close()
				return nil, err
			}
			c.Scopes = append(c.Scopes, sc)
		}
		srows.Close()
		if err := srows.Err(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OwnerOf returns the open claim of ANOTHER session covering relPath, if any.
// This runs on the gate hot path (every mutating tool call), so it scans one
// scopes query instead of materializing every claim with its owner (the old
// 1+N shape), and loads the single matching claim only on a hit.
func (s *Store) OwnerOf(relPath, excludeSession string) (ClaimInfo, bool, error) {
	rel := fold(relPath)
	rows, err := s.db.Query(`SELECT cs.folded, cs.claim_id FROM claim_scopes cs
		JOIN claims c ON c.claim_id=cs.claim_id WHERE c.state='open' AND c.session_id<>?`, excludeSession)
	if err != nil {
		return ClaimInfo{}, false, err
	}
	defer rows.Close()
	claimID := ""
	for rows.Next() {
		var folded, id string
		if err := rows.Scan(&folded, &id); err != nil {
			return ClaimInfo{}, false, err
		}
		if claimID == "" && scopeCovers(folded, rel) {
			claimID = id
		}
	}
	if err := rows.Err(); err != nil {
		return ClaimInfo{}, false, err
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
	err := s.db.QueryRow(`SELECT session_id, incarnation, label, worktree, pid, started, last_seen, COALESCE(ended,0)
		FROM sessions WHERE session_id=?`, sessionID).Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree,
		&si.PID, unixScan{&si.Started}, unixScan{&si.LastSeen}, unixScan{&si.Ended})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionInfo{}, false, nil
	}
	if err != nil {
		return SessionInfo{}, false, err
	}
	return si, true, nil
}

// Sweep: orphan open claims of ended sessions; delete released/orphaned rows
// past ttl (orphaning stamps renewed, so a fresh orphan is never deleted in
// the same pass); GC delivered-or-aged inbox rows. With force, additionally
// orphan open claims whose owner session has been silent past forceAfter —
// an explicit operator action: a session that only thinks/reads does not move
// last_seen, which is why plain sweep never does this.
func (s *Store) Sweep(ttl, forceAfter time.Duration, force bool) (orphaned, deleted int, err error) {
	if ttl <= 0 || forceAfter <= 0 {
		return 0, 0, fmt.Errorf("sweep ttl/forceAfter must be positive (got %v, %v)", ttl, forceAfter)
	}
	err = s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		n, err := orphanEnded(tx, now)
		if err != nil {
			return err
		}
		orphaned += n

		if force {
			res, err := tx.Exec(`UPDATE claims SET state='orphaned', renewed=? WHERE state='open' AND session_id IN
				(SELECT session_id FROM sessions WHERE ended IS NULL AND last_seen < ?)`, now, now-int64(forceAfter.Seconds()))
			if err != nil {
				return err
			}
			m, _ := res.RowsAffected()
			orphaned += int(m)
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
		deleted = int(m)

		// Inbox GC: messages past ttl are dropped with their delivery marks.
		if _, err := tx.Exec(`DELETE FROM inbox_delivery WHERE msg_id IN
			(SELECT msg_id FROM inbox WHERE created < ?)`, cutoff); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM inbox WHERE created < ?`, cutoff); err != nil {
			return err
		}
		return sweepDirty(tx, now, cutoff)
	})
	return orphaned, deleted, err
}

// Pause records a pause control for a session id, label, or "all".
func (s *Store) Pause(target, note string) error {
	_, err := s.db.Exec(`INSERT INTO controls (kind, target, note, created) VALUES ('pause',?,?,?)`,
		target, note, s.now().Unix())
	return err
}

// Resume clears pause controls for the target.
func (s *Store) Resume(target string) (int, error) {
	res, err := s.db.Exec(`UPDATE controls SET cleared=? WHERE kind='pause' AND target=? AND cleared IS NULL`,
		s.now().Unix(), target)
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

// Msg queues a message for a session id, label, or "all".
func (s *Store) Msg(target, from, body string) error {
	_, err := s.db.Exec(`INSERT INTO inbox (target, sender, body, created) VALUES (?,?,?,?)`,
		target, from, body, s.now().Unix())
	return err
}

// InboxMsg is one queued message.
type InboxMsg struct {
	ID      int64
	From    string
	Body    string
	Created time.Time
}

// Undelivered returns messages addressed to the session (by id, label, or
// "all") not yet marked delivered to it, oldest first.
func (s *Store) Undelivered(sessionID, label string) ([]InboxMsg, error) {
	rows, err := s.db.Query(`SELECT m.msg_id, m.sender, m.body, m.created FROM inbox m
		WHERE m.target IN (?, ?, 'all')
		AND NOT EXISTS (SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=?)
		ORDER BY m.msg_id`, sessionID, label, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxMsg
	for rows.Next() {
		var m InboxMsg
		if err := rows.Scan(&m.ID, &m.From, &m.Body, unixScan{&m.Created}); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
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

// Sessions lists all sessions, live first, then by last_seen.
func (s *Store) Sessions() ([]SessionInfo, error) {
	rows, err := s.db.Query(`SELECT session_id, incarnation, label, worktree, pid, started, last_seen, COALESCE(ended,0)
		FROM sessions ORDER BY (ended IS NOT NULL), last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree, &si.PID,
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
func (s *Store) Session(sessionID string) (SessionInfo, bool, error) {
	sessions, err := s.Sessions()
	if err != nil {
		return SessionInfo{}, false, err
	}
	for _, si := range sessions {
		if si.SessionID == sessionID {
			return si, true, nil
		}
	}
	return SessionInfo{}, false, nil
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
	sessions, err := s.Sessions()
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
