package cli

// `buddy who <slug>`: every scope of that claim, uncut (D-094).
//
// THE FAILURE. Every handoff notice says "re-claim it with the SAME scopes",
// and every view that lists a claim's scopes — `ls`, `who`'s CLAIMS HELD,
// `status`, the hello digest — cuts the list at 512 bytes ("...and 6 more not
// shown"), which is right for a listing and wrong for this one question. On
// one fleet a successor handed a 22-scope claim gave up re-claiming it ("the
// scope lists show truncated"); another typed all 22 by hand after trying
// `ls --json` (no such flag), sqlite3 on the live ledger (refused, 14) and an
// eval-built re-claim (refused by the gate).
//
// THE RULE. A `who` whose target resolved through a claim SLUG prints that
// claim's scopes, one per line, each through fence.Field exactly as
// scopeList renders an item (D-066: one token, a space shown as ␣, a line
// break as ⏎), and with no total cap — the claim's own scopes are bounded by
// what `claim` accepted. Only here: the other views keep their caps, because
// they list many claims and land in hook output and tool results that pay
// for every byte. A scope that `fence.Field` had to alter is said to be
// altered, since pasting it back would name a different path.

import (
	"fmt"
	"strconv"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// claimScopesBlock prints every scope of the open claim `slug`, or nothing
// when no open claim has that slug (the target resolved a moment ago; a
// release in between is not this view's to explain).
func claimScopesBlock(env Env, st *store.Store, slug string) error {
	open, err := st.Claims(false)
	if err != nil {
		return err
	}
	for _, c := range open {
		if c.Slug != slug {
			continue
		}
		n, mode := len(c.Scopes), sharedWord(c.Shared) // a count and buddy's own word: not peer text
		fmt.Fprintf(env.Stdout, "claim %s holds %d %sscope(s), every one (each is one --scope to re-claim it):\n",
			strconv.Quote(fence.Line(c.Slug, 128)), n, mode)
		for _, sc := range c.Scopes {
			shown := fence.Field(sc, 512)
			altered := ""
			if shown != sc {
				altered = "   (rendered: this scope holds a space, a control character or more than 512 bytes, so it does not paste back as shown)"
			}
			fmt.Fprintf(env.Stdout, "  scope %s%s\n", shown, altered)
		}
		return nil
	}
	return nil
}
