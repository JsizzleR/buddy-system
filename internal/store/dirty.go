package store

// Dirty paths: which session has uncommitted changes to which file.
//
// This is the ADDRESSING half of the ledger, and it is deliberately not a
// second claims mechanism. A claim is DECLARED INTENT over a scope, taken
// before the work and enforced by the gate; a dirty path is an OBSERVED FACT
// about the working tree, recorded after the fact and enforced by nobody. The
// two answer different questions — "may I take this?" versus "who is holding
// this right now?" — and the second one had no answer at all, so a message
// about an uncommitted hunk ("whoever owns the CHANGELOG edit") was addressed
// to nobody and every recipient rationally ignored it.
//
// Nothing here may ever refuse anything. If these tables are empty, wrong, or
// stale, the only consequence is a message that goes unaddressed, which is
// exactly where we started. Claims remain the safety mechanism.
//
// Rows are keyed by (session_id, worktree, folded path). The worktree is part
// of the key because sessions really do run in separate `git worktree`
// checkouts of one repo, sharing one ledger (it lives in the git COMMON dir):
// two sessions with the same relative path dirty in different worktrees are
// editing different files and are not in conflict.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DirtyKeepAfterEnd is how long a session's dirty-path attributions outlive it.
//
// Far beyond the claims TTL on purpose. The right trigger for forgetting who
// left a hunk is the FILE BECOMING CLEAN, which RetainDirty handles for every
// session's rows — this is only the backstop for rows no scan will ever reach
// again, and it is set where the answer has stopped being useful rather than
// where it is merely old.
const DirtyKeepAfterEnd = 30 * 24 * time.Hour

// leaseExpired decides whether a scan slot stamped at last is available at now.
//
// A stamp in the FUTURE counts as expired rather than as "not due". A backwards
// clock step or a corrupt row would otherwise suppress scanning until wall time
// caught up — potentially forever — and a suppressed scan means retraction
// stops, which is the failure that makes rows name the wrong owner. Being
// wrong toward scanning again costs one `git status`.
func leaseExpired(now, last int64, every time.Duration) bool {
	return last > now || now-last >= int64(every.Seconds())
}

// DirtyRecord is one session's uncommitted-file record.
type DirtyRecord struct {
	Path      string // repo-relative, as the tool call named it
	Worktree  string // folded worktree root this row is keyed to
	FirstSeen time.Time
	LastSeen  time.Time
	Owner     SessionInfo
}

// Stale reports whether the owning session has gone quiet past StaleAfter.
// A stale row names an owner who will not answer, which is worse than no data
// — so callers must say so rather than mixing it in silently.
func (d DirtyRecord) Stale(now time.Time) bool {
	return d.Owner.Live() && now.Sub(d.Owner.LastSeen) > StaleAfter
}

// RecordDirty notes that a tool call by this session named paths in worktree.
//
// This is the ONLY thing that attributes a path to a session: a tool call is
// the one signal that says which session acted. See RetainDirty for why the
// git scan may not attribute, and what that costs.
//
// It records INTENT, not a verified diff — an edit that writes identical bytes
// leaves git clean, and this still records it. That row is dropped by the next
// RetainDirty, so the set converges on the truth within one scan interval
// rather than being wrong indefinitely.
//
// first_seen survives a re-record, so the display can say how long a session
// has had the file open.
func (s *Store) RecordDirty(sessionID, worktree string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO dirty_paths (session_id, worktree, path, folded, first_seen, last_seen)
				VALUES (?,?,?,?,?,?)
				ON CONFLICT(session_id, worktree, folded) DO UPDATE SET path=excluded.path, last_seen=excluded.last_seen`,
				sessionID, worktree, p, fold(p), now, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// RetainDirty drops rows in worktree whose path git no longer reports as
// dirty. It NEVER adds a row.
//
// That asymmetry is the correction of a real defect, and it is the whole
// reason a scan exists at all. `git status` describes a WORKING TREE, and in
// the deployment this tool was built for several sessions share one checkout —
// so its output is the UNION of everybody's edits and attributes none of it.
// An earlier version made the scan authoritative for the scanning session, and
// measured: a session that had done nothing but a read was recorded as a
// holder of a peer's uncommitted CHANGELOG hunk. With six sessions in one
// checkout every session would hold every dirty file, which is not merely
// wrong but is precisely the unaddressable noise this feature exists to end.
//
// Attribution therefore comes ONLY from a tool call naming a path — the one
// signal that says WHICH session acted. Retraction stays safe in the shared
// case because it is session-agnostic: a path git reports CLEAN is clean for
// everyone, so dropping it cannot misattribute anything.
//
// The honest cost, stated rather than hidden: a write made by a Bash command
// carries no path in the hook input and git cannot say whose it was, so it is
// never attributed. `whose` reports that case explicitly instead of implying
// the file is unheld. (Considered and declined: attributing scan results when
// the session is the sole live occupant of its worktree. It is sound only
// while that holds, and rows attributed alone would silently outlive the
// arrival of a second session — a stale attribution being worse than a missing
// one, for a capability the tool-named path already covers.)
//
// It retracts rows of EVERY session in that worktree, not just the scanner's.
// That is the strong form of the same argument: if git reports a path clean,
// then nobody has uncommitted changes to it, so every row naming it is false.
// It is also the only way a row outlives its author correctly — a session that
// died holding a file leaves a row no scan of its own will ever revisit, and
// without this it would name that file forever even after a peer committed it.
//
// `before` fences the delete against the scan's own staleness: git ran before
// this transaction opened, so a row recorded in the meantime describes an edit
// the scan could not have seen. Only rows last touched strictly BEFORE the
// scan started are eligible, which errs toward keeping a possibly-stale row
// over dropping a fresh one — the direction that loses no attribution. (Unix
// second resolution makes the boundary coarse; a row written in the same
// second as the scan began survives, which is the same safe direction.)
func (s *Store) RetainDirty(worktree string, stillDirty []string, before time.Time) error {
	keep := make(map[string]bool, len(stillDirty))
	for _, p := range stillDirty {
		keep[fold(p)] = true
	}
	cutoff := before.Unix()
	return s.tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT session_id, folded FROM dirty_paths WHERE worktree=? AND last_seen < ?`,
			worktree, cutoff)
		if err != nil {
			return err
		}
		type row struct{ session, folded string }
		var gone []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.session, &r.folded); err != nil {
				rows.Close()
				return err
			}
			if !keep[r.folded] {
				gone = append(gone, r)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// Bounded by the rows recorded for this worktree, never by the size of
		// the repository — so a tree with a hundred thousand dirty paths still
		// costs a transaction proportional to our own handful of rows.
		for _, r := range gone {
			if _, err := tx.Exec(`DELETE FROM dirty_paths WHERE session_id=? AND worktree=? AND folded=?`,
				r.session, worktree, r.folded); err != nil {
				return err
			}
		}
		return nil
	})
}

// DueForDirtyScan reports whether this session should run a full git scan of
// worktree now, and claims the slot in the same transaction so two callers
// cannot both scan.
//
// The stamp is written BEFORE the scan runs, deliberately: if git then fails
// or times out, the failure costs one skipped interval rather than a retry on
// every single tool call. Cheap-when-broken is the right direction for
// something that must never slow a caller down.
func (s *Store) DueForDirtyScan(sessionID, worktree string, every time.Duration) (bool, error) {
	// A non-positive interval would make every caller due forever; a
	// sub-second one truncates to zero against a second-resolution column and
	// does the same. Refuse rather than melt.
	if every < time.Second {
		return false, fmt.Errorf("dirty-scan interval must be at least 1s (got %v)", every)
	}
	// Fast path, deliberately OUTSIDE a transaction. This is consulted on every
	// single tool call of every session, and the answer is almost always "not
	// due" — but the ledger opens with _txlock=immediate, so even a read-only
	// transaction takes the database's WRITE lock and would serialize every
	// session's heartbeat behind every other one's. A plain read costs nothing
	// and is re-checked under the lock below before anything is claimed.
	now := s.now().Unix()
	var last int64
	err := s.db.QueryRow(`SELECT scanned FROM dirty_scans WHERE session_id=? AND worktree=?`,
		sessionID, worktree).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && !leaseExpired(now, last, every) {
		return false, nil
	}

	due := false
	err = s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		var last int64
		err := tx.QueryRow(`SELECT scanned FROM dirty_scans WHERE session_id=? AND worktree=?`,
			sessionID, worktree).Scan(&last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Re-checked under the lock: two callers that both passed the fast
		// path must not both scan.
		if err == nil && !leaseExpired(now, last, every) {
			return nil
		}
		due = true
		_, err = tx.Exec(`INSERT INTO dirty_scans (session_id, worktree, scanned) VALUES (?,?,?)
			ON CONFLICT(session_id, worktree) DO UPDATE SET scanned=excluded.scanned`, sessionID, worktree, now)
		return err
	})
	return due, err
}

// DirtyWarnPending reports whether this session has yet to be warned about
// relPath in worktree. Split from the marking half so a caller can decide to
// warn, deliver, and only then commit — see MarkDirtyWarned.
func (s *Store) DirtyWarnPending(sessionID, worktree, relPath string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM dirty_warned WHERE session_id=? AND worktree=? AND folded=?`,
		sessionID, worktree, fold(relPath)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// MarkDirtyWarned records that session has been warned about relPath in
// worktree, and reports whether this call is the one that recorded it.
//
// The dedup is durable, in the ledger, because sessions restart: an in-memory
// set would re-warn on every resume. It is once per (session, worktree, path)
// for the session's whole life — a warn that repeats on every subsequent edit
// of the same file trains its reader to skip it, and then it is worse than
// nothing. The accepted cost: if the peer holding the file changes, the second
// holder is never announced. That is the deliberate trade for silence.
func (s *Store) MarkDirtyWarned(sessionID, worktree, relPath string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO dirty_warned (session_id, worktree, folded, warned) VALUES (?,?,?,?)`,
		sessionID, worktree, fold(relPath), s.now().Unix())
	return err
}

// FirstDirtyWarn is the atomic check-and-mark, for callers with nothing to
// deliver in between.
func (s *Store) FirstDirtyWarn(sessionID, worktree, relPath string) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO dirty_warned (session_id, worktree, folded, warned) VALUES (?,?,?,?)`,
		sessionID, worktree, fold(relPath), s.now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DirtyPeersInWorktree returns every session other than excludeSession holding
// relPath in the SAME worktree, live or not.
//
// Same worktree only: a peer editing the same relative path in a different
// checkout is editing a different FILE, and warning about that is a false
// alarm — the fastest way to teach someone to ignore the notice.
//
// But NOT live-only, which an earlier version got wrong. Whether the edit is
// still sitting in the tree has nothing to do with whether its author is still
// answering: a session that stopped beating, or ended, without committing left
// the hunk exactly where it was, and the next session to touch that file is in
// precisely the collision the notice exists to report. Filtering those out
// stayed silent on real conflicts. What liveness legitimately changes is the
// WORDING — "ask them" versus "they may be gone" — so the caller labels each
// holder and the rows a scan proves clean are retracted by RetainDirty rather
// than hidden here.
func (s *Store) DirtyPeersInWorktree(worktree, relPath, excludeSession string) ([]DirtyRecord, error) {
	return s.dirtyWhere(`WHERE d.folded=? AND d.worktree=? AND d.session_id<>?`,
		fold(relPath), worktree, excludeSession)
}

// DirtyHolders returns every recorded holder of relPath, across ALL worktrees
// of this repo and including ended and stale sessions.
//
// Deliberately broader than the warn. The warn asks "am I in conflict?", where
// a different worktree is a false alarm; `whose` asks "who do I talk to?", and
// there the answer is a PERSON — someone editing the same file in a sibling
// checkout is exactly who a message about that file should reach, and a
// session that has since ended is still who made the edit that is sitting in
// the tree. The caller labels each row rather than filtering it away, because
// answering "nobody" about a file that is visibly dirty is the failure being
// fixed.
func (s *Store) DirtyHolders(relPath string, asDir bool) ([]DirtyRecord, error) {
	if asDir {
		// "whose is src/?" is a legitimate question and was answered "clean"
		// for want of any prefix handling. LIKE with an escaped prefix, so a
		// directory whose name contains % or _ does not match its siblings.
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(fold(relPath))
		return s.dirtyWhere(`WHERE d.folded=? OR d.folded LIKE ? ESCAPE '\'`,
			fold(relPath), esc+`/%`)
	}
	return s.dirtyWhere(`WHERE d.folded=?`, fold(relPath))
}

func (s *Store) dirtyWhere(where string, args ...any) ([]DirtyRecord, error) {
	rows, err := s.db.Query(`SELECT d.path, d.worktree, d.first_seen, d.last_seen,
			ses.session_id, ses.incarnation, ses.label, ses.worktree, ses.pid, ses.started, ses.last_seen, COALESCE(ses.ended,0)
		FROM dirty_paths d JOIN sessions ses ON ses.session_id=d.session_id `+where+`
		ORDER BY (ses.ended IS NOT NULL), ses.last_seen DESC, d.path`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirtyRecord
	for rows.Next() {
		var d DirtyRecord
		if err := rows.Scan(&d.Path, &d.Worktree, unixScan{&d.FirstSeen}, unixScan{&d.LastSeen},
			&d.Owner.SessionID, &d.Owner.Incarnation, &d.Owner.Label, &d.Owner.Worktree, &d.Owner.PID,
			unixScan{&d.Owner.Started}, unixScan{&d.Owner.LastSeen}, unixScan{&d.Owner.Ended}); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// sweepDirty drops the scan and warn bookkeeping of long-ended sessions.
//
// It deliberately does NOT touch dirty_paths. An ended session can leave real
// uncommitted edits, and its row is the ONLY record of whose they are — a scan
// cannot reconstruct it, because git attributes nothing. Deleting that on a
// timer destroys the answer to "who left this hunk here?" while the hunk is
// still sitting in the tree, which is the question the whole feature exists to
// answer. The right trigger for forgetting an attribution is the FILE BECOMING
// CLEAN, and RetainDirty now does exactly that, for every session's rows.
//
// It does sweep dirty_paths at a FAR longer horizon (DirtyKeepAfterEnd), which
// is a different claim from the 24h one it replaces: a row whose session ended
// a month ago names a label nobody recognizes any more, so its attribution
// value has decayed to nothing while its cost has not. That bounds growth for
// the cases a scan can never reach — a worktree removed outright, a repo whose
// sessions all stopped, a box where `git status` fails permanently — without
// deleting attributions anyone could still act on.
//
// The warn marks DO go, because re-announcing a file to a session that has
// been gone for a day is a new day's information.
func sweepDirty(tx *sql.Tx, now, cutoff int64) error {
	const gone = `(SELECT session_id FROM sessions WHERE ended IS NOT NULL AND ended < ?)`
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM dirty_warned WHERE session_id IN ` + gone, cutoff},
		{`DELETE FROM dirty_scans WHERE session_id IN ` + gone, cutoff},
		{`DELETE FROM dirty_paths WHERE session_id IN ` + gone, now - int64(DirtyKeepAfterEnd.Seconds())},
	} {
		if _, err := tx.Exec(q.sql, q.arg); err != nil {
			return err
		}
	}
	return nil
}
