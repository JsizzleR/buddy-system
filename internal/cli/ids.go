package cli

// `buddy ids`: the identifier register (D-029, issue #16). The store file
// carries the why; this is the verb surface, and its one job beyond
// rendering is to REFUSE to overclaim — see status.

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const idsUsage = "usage: buddy ids seed <space> <n>          declare the space's measured high-water mark (create or RAISE; never lower)\n" +
	"       buddy ids take <space> <count> [--note <text>] [--session <id>]   a contiguous block above the ceiling, recorded to you\n" +
	"       buddy ids ls [space] [--all]         who holds which numbers, since when: blocks taken in the last 3d, older ones counted (--all lists them)\n" +
	"       buddy ids status <space> <n>         reserved here / above the ceiling / at-or-below and unreserved here\n" +
	"       (no return verb: a returned id is a claim about intent, take from the ceiling — ids are free)"

// idsListWindow is how far back `ids ls` lists blocks; older ones fold into
// one count line per space, as the roster folds its dead rows (D-091). The
// token-overhead report (D-167): `ids ls` printed the register since it was
// seeded, 7,090 B and 51 blocks on the live ledger on 2026-11-11, the oldest
// 27 days old, on 54 calls in 14 days; 10 of those blocks were under a day
// old, none 1 to 3 days, 41 older. A block's age says nothing about whether
// its numbers are used (they never return), so the fold line says they are
// still held, and `status` answers for any one number whatever its age. The
// fold line and the usage say "3d": change all three together.
const idsListWindow = 72 * time.Hour

func cmdIDs(args []string, env Env) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(idsUsage)
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "seed":
		if len(args) != 3 {
			return errors.New(idsUsage)
		}
		n, err := parseID(args[2])
		if err != nil {
			return err
		}
		prev, created, err := st.IDSeed(args[1], n)
		if err != nil {
			return fencedErr(err)
		}
		if created {
			fmt.Fprintf(env.Stdout, "space %s seeded: ceiling %d — %s\n", fence.Field(args[1], 64), n, nextBlock(n))
		} else {
			fmt.Fprintf(env.Stdout, "space %s ceiling raised %d -> %d\n", fence.Field(args[1], 64), prev, n)
		}
		return nil
	case "take":
		if len(args) < 3 {
			return errors.New(idsUsage)
		}
		space := args[1]
		count, err := parseID(args[2])
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("ids take", flag.ContinueOnError)
		note := fs.String("note", "", "what the block is for")
		var session string
		sessionFlag(fs, &session)
		if help, err := parseFlags(fs, args[3:], idsUsage, env); help || err != nil {
			if err != nil && strings.Contains(err.Error(), "not defined: -desc") {
				// claim's word for the same thing. Typed on one fleet and
				// answered with flag's bare "flag provided but not defined".
				return fmt.Errorf("ids take uses --note, not --desc: %w", err)
			}
			return err
		}
		if fs.NArg() > 0 {
			// Go's parser stops at the first non-flag, so `take record 5 junk
			// --session B` would allocate to whoever the environment names and
			// drop the rest: a block recorded to the wrong session is the
			// duplicate this register exists to prevent (Codex code pass).
			return fmt.Errorf("ids take: unexpected argument %s (count, then flags)", strconv.Quote(fence.Line(fs.Arg(0), 64)))
		}
		si, err := whoAmI(st, env, session)
		if err != nil {
			return err
		}
		lo, hi, err := st.IDTake(space, count, si.SessionID, si.Incarnation, si.Label, *note)
		if err != nil {
			return fencedErr(err)
		}
		fmt.Fprintf(env.Stdout, "took %s %d..%d for %s\n", fence.Field(space, 64), lo, hi, fence.Field(si.Label, 64))
		return nil
	case "ls":
		// One optional space, and --all on either side of it: Go's parser
		// stops at the first positional, so the flags after it are parsed
		// again, and anything still left is refused, never ignored (D-031).
		fs := flag.NewFlagSet("ids ls", flag.ContinueOnError)
		all := fs.Bool("all", false, "list every block, the old ones too")
		if help, err := parseFlags(fs, args[1:], idsUsage, env); help || err != nil {
			return err
		}
		space := ""
		if fs.NArg() > 0 {
			space = fs.Arg(0)
			// After a `--` every argument is positional: parsed again, a
			// trailing --all would be read as the flag (Codex code pass).
			rest := fs.Args()[1:]
			if used := len(args[1:]) - fs.NArg(); used > 0 && args[used] == "--" && len(rest) > 0 {
				return usageRefusal(fmt.Sprintf("ids ls takes one space, got %s after it", strconv.Quote(fence.Line(rest[0], 64))), idsUsage)
			}
			if help, err := parseFlags(fs, rest, idsUsage, env); help || err != nil {
				return err
			}
			if err := noStray("ids ls <space>", fs, idsUsage); err != nil {
				return err
			}
		}
		spaces, err := st.IDSpaces()
		if err != nil {
			return err
		}
		blocks, err := st.IDBlocks(space)
		if err != nil {
			return err
		}
		now := nowOf(env)
		shown := 0
		for _, sp := range spaces {
			if space != "" && sp.Space != space {
				continue
			}
			shown++
			// The ceiling is the fact every allocation rests on, so it prints
			// beside its blocks and not on a separate screen. Every value is
			// peer text: the space name, the label and the note.
			// "space " in front, so a space NAMED "BUDDY:" cannot open a line
			// that reads as a notice in a tool result (Codex code pass).
			fmt.Fprintf(env.Stdout, "space %s  ceiling %d  (%s)\n", fence.Field(sp.Space, 64), sp.Ceiling, nextBlock(sp.Ceiling))
			folded := 0
			for _, b := range blocks {
				if b.Space != sp.Space {
					continue
				}
				if !*all && now.Sub(b.Taken) > idsListWindow {
					folded++
					continue
				}
				fmt.Fprintf(env.Stdout, "  %d..%-8d %-24s taken %-4s %s\n", b.Lo, b.Hi, fence.Field(b.Label, 64), age(now, b.Taken), fence.Line(b.Note, 512))
			}
			if folded > 0 {
				fmt.Fprintf(env.Stdout, "  (+ %d block(s) taken more than 3d ago, still held — not listed: buddy ids ls --all)\n", folded)
			}
		}
		if shown == 0 {
			if space == "" {
				fmt.Fprintln(env.Stdout, "no id spaces seeded (buddy ids seed <space> <n>)")
			} else {
				return fencedErr(fmt.Errorf("%w %q", store.ErrNoSpace, space))
			}
		}
		return nil
	case "status":
		if len(args) != 3 {
			return errors.New(idsUsage)
		}
		n, err := parseID(args[2])
		if err != nil {
			return err
		}
		verdict, b, ceiling, err := st.IDStatus(args[1], n)
		if err != nil {
			return fencedErr(err)
		}
		space := fence.Field(args[1], 64)
		// THREE ANSWERS, none of which claims to know the artifact. The
		// register knows what IT handed out and what the seed covered; it
		// does not know what a document contains, and the whole point of
		// this verb is that it never pretends to (issue #16's four false
		// occupancy reports were all a probe claiming more than it read).
		switch verdict {
		case store.IDReserved:
			holder, note := fence.Field(b.Label, 64), noteSuffix(b.Note)
			fmt.Fprintf(env.Stdout, "%s %d: RESERVED here by %s (block %d..%d, taken %s ago)%s\n",
				space, n, holder, b.Lo, b.Hi, age(nowOf(env), b.Taken), note)
		case store.IDAboveCeiling:
			fmt.Fprintf(env.Stdout, "%s %d: above the ceiling (%d) — UNRESERVED IN THIS REGISTER, which is not \"free\": the artifact may already use it; `buddy ids take` allocates from the ceiling, never by number\n",
				space, n, ceiling)
		case store.IDBelowCeilingUnreserved:
			fmt.Fprintf(env.Stdout, "%s %d: at or below the ceiling (%d) and in no block — NOT AVAILABLE for allocation; whether the artifact uses it, this register cannot say (read the artifact as a record, not as prose)\n",
				space, n, ceiling)
		}
		return nil
	default:
		return errors.New(idsUsage)
	}
}

// nextBlock says where the next allocation would start, and says
// "exhausted" at the top of the number line instead of wrapping to a
// negative number (Codex code pass: a ceiling of MaxInt64 advertised a next
// block at -9223372036854775808).
func nextBlock(ceiling int64) string {
	if ceiling == math.MaxInt64 {
		return "exhausted: nothing above the ceiling"
	}
	return fmt.Sprintf("next block starts at %d", ceiling+1)
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " — " + fence.Line(note, 512)
}

// parseID parses a non-negative id or count. Refused rather than wrapped:
// a "-1" or a "1e9" reaching the register as 0 or as a huge block is the
// silent duplicate this whole verb exists to prevent.
func parseID(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s is not a non-negative integer", strconv.Quote(fence.Line(s, 64)))
	}
	return n, nil
}
