package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// The identifier register (D-029, issue #16).
//
// THE FAILURE. The reference repo allocates decision numbers, row ids and
// bundle ids from append-only PROSE ledgers, and sessions took max()+1 of
// what they could see. In one day: two sessions collided on a bundle id; a
// session returned four ids as unused that it had drafted as rows in its
// own document; a session said "filed as <id>" having written to no record
// file, and a peer nearly struck its own row as a duplicate; and the
// coordinator's own probe reported an id TAKEN off a raw substring count
// whose single hit was a range endpoint in a sentence. Four false occupancy
// reports, every one made from something other than a row-shaped read of a
// register. The requirement the run ended on: A REGISTER SHOULD NEVER HAVE
// TO PARSE PROSE TO KNOW WHAT IS TAKEN.
//
// WHAT THIS IS. Per space, a CEILING — the highest id known used or
// reserved — and the blocks handed out above it, each recorded to the
// session that took it. `take` is one immediate transaction: the next
// contiguous block above the ceiling, and the ceiling moves. Nothing is
// ever reissued: a "returned" id is a claim about intent, and the register
// cannot see a draft in a document or a citation in unlanded code, so there
// is no `return` verb — take from the ceiling, ids are free (the issue's
// own rule 2, learned twice in one day).
//
// WHAT THIS IS NOT. It is not knowledge of the artifact. A space must be
// SEEDED with a measured high-water mark before anything is taken, and
// seeding is the operator's assertion; a space that was never seeded
// refuses rather than allocating from zero into a document that already
// holds ids 1-100 (Codex design pass). `status` therefore answers in three
// registers and never claims more than it holds: reserved here (by whom,
// which block, when); above the ceiling (UNRESERVED IN THIS REGISTER, not
// globally free); at or below the ceiling with no block (NOT AVAILABLE for
// allocation — whether the artifact uses it, this register cannot say, and
// calling it "a hole" would be exactly the prose-parsing it exists to end).
//
// Spaces and blocks survive session end, orphaning and sweep: a
// reservation is a fact about the artifact's number line, not about a
// session's life, and a block taken by a session that has since gone is
// still a block nobody else may take.

// IDBlock is one allocation.
type IDBlock struct {
	Space       string
	Lo, Hi      int64
	SessionID   string
	Incarnation string
	Label       string // the holder's label at the time, for a listing that outlives the session
	Note        string
	Taken       time.Time
}

// IDSpace is one space's ceiling.
type IDSpace struct {
	Space   string
	Ceiling int64
	Created time.Time
}

// ErrNoSpace says a space was never seeded. Allocation from an unseeded
// space is refused, not defaulted: the register does not know the
// artifact's high-water mark and must not guess it is zero.
var ErrNoSpace = errors.New("no such id space")

// IDSeed declares a space's high-water mark: creates the space, or RAISES
// its ceiling. Lowering is refused naming the current ceiling — a lower
// number is an assertion that reserved blocks are free, and that is the
// reissue the register exists to prevent.
func (s *Store) IDSeed(space string, ceiling int64) (prev int64, created bool, err error) {
	if space == "" {
		return 0, false, errors.New("an id space needs a name")
	}
	if ceiling < 0 {
		return 0, false, fmt.Errorf("a ceiling cannot be negative (got %d)", ceiling)
	}
	err = s.tx(func(tx *sql.Tx) error {
		now := s.now().Unix()
		err := tx.QueryRow(`SELECT ceiling FROM id_spaces WHERE space=?`, space).Scan(&prev)
		if errors.Is(err, sql.ErrNoRows) {
			created = true
			_, err = tx.Exec(`INSERT INTO id_spaces (space, ceiling, created) VALUES (?,?,?)`, space, ceiling, now)
			return err
		}
		if err != nil {
			return err
		}
		if ceiling < prev {
			return fmt.Errorf("space %q has ceiling %d already; a ceiling is never lowered, because every id up to it may be reserved", space, prev)
		}
		_, err = tx.Exec(`UPDATE id_spaces SET ceiling=? WHERE space=?`, ceiling, space)
		return err
	})
	return prev, created, err
}

// IDTake allocates count contiguous ids above the space's ceiling to the
// session, in one transaction, and moves the ceiling. Refused: an unseeded
// space, a non-positive count, and a block that would overflow.
func (s *Store) IDTake(space string, count int64, sessionID, incarnation, label, note string) (lo, hi int64, err error) {
	if count <= 0 {
		return 0, 0, fmt.Errorf("count must be positive (got %d)", count)
	}
	err = s.tx(func(tx *sql.Tx) error {
		var ceiling int64
		err := tx.QueryRow(`SELECT ceiling FROM id_spaces WHERE space=?`, space).Scan(&ceiling)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w %q — seed it first with the artifact's measured high-water mark: buddy ids seed %s <n>", ErrNoSpace, space, space)
		}
		if err != nil {
			return err
		}
		if ceiling > math.MaxInt64-count {
			return fmt.Errorf("a block of %d above ceiling %d overflows", count, ceiling)
		}
		lo, hi = ceiling+1, ceiling+count
		if _, err := tx.Exec(`INSERT INTO id_blocks (space, lo, hi, session_id, incarnation, label, note, taken) VALUES (?,?,?,?,?,?,?,?)`,
			space, lo, hi, sessionID, incarnation, label, note, s.now().Unix()); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE id_spaces SET ceiling=? WHERE space=?`, hi, space)
		return err
	})
	return lo, hi, err
}

// IDSpaces lists every seeded space.
func (s *Store) IDSpaces() ([]IDSpace, error) {
	rows, err := s.db.Query(`SELECT space, ceiling, created FROM id_spaces ORDER BY space`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IDSpace
	for rows.Next() {
		var sp IDSpace
		if err := rows.Scan(&sp.Space, &sp.Ceiling, unixScan{&sp.Created}); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// IDBlocks lists blocks, all spaces when space is "", newest last.
func (s *Store) IDBlocks(space string) ([]IDBlock, error) {
	rows, err := s.db.Query(`SELECT space, lo, hi, session_id, incarnation, label, note, taken FROM id_blocks
		WHERE (?='' OR space=?) ORDER BY space, lo`, space, space)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IDBlock
	for rows.Next() {
		var b IDBlock
		if err := rows.Scan(&b.Space, &b.Lo, &b.Hi, &b.SessionID, &b.Incarnation, &b.Label, &b.Note, unixScan{&b.Taken}); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// IDVerdict is what the register knows about one id.
type IDVerdict int

const (
	// IDReserved: inside a block this register handed out; Block says whose.
	IDReserved IDVerdict = iota
	// IDAboveCeiling: no block and above the ceiling — unreserved IN THIS
	// REGISTER, which is not "free": the artifact may already use it, and
	// only a seed that measured the artifact would say.
	IDAboveCeiling
	// IDBelowCeilingUnreserved: at or below the ceiling and in no block —
	// the seed covered it, so it is NOT available for allocation, and
	// whether the artifact uses it this register cannot say.
	IDBelowCeilingUnreserved
)

// IDStatus answers for one id in a seeded space.
func (s *Store) IDStatus(space string, n int64) (IDVerdict, IDBlock, int64, error) {
	var ceiling int64
	err := s.db.QueryRow(`SELECT ceiling FROM id_spaces WHERE space=?`, space).Scan(&ceiling)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, IDBlock{}, 0, fmt.Errorf("%w %q", ErrNoSpace, space)
	}
	if err != nil {
		return 0, IDBlock{}, 0, err
	}
	var b IDBlock
	err = s.db.QueryRow(`SELECT space, lo, hi, session_id, incarnation, label, note, taken FROM id_blocks
		WHERE space=? AND lo<=? AND hi>=? LIMIT 1`, space, n, n).Scan(
		&b.Space, &b.Lo, &b.Hi, &b.SessionID, &b.Incarnation, &b.Label, &b.Note, unixScan{&b.Taken})
	switch {
	case err == nil:
		return IDReserved, b, ceiling, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, IDBlock{}, 0, err
	case n > ceiling:
		return IDAboveCeiling, IDBlock{}, ceiling, nil
	default:
		return IDBelowCeilingUnreserved, IDBlock{}, ceiling, nil
	}
}
