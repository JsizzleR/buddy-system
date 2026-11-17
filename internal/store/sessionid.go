package store

import (
	"errors"
	"fmt"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// ErrSessionIDRefused is the ledger's refusal to CREATE a session row for an id
// outside fence.ValidSessionID (D-201). It is returned only where a row would
// be created (helloFrom's no-row arm, BeginLogical's missing arm), never on a
// refresh, a revival or a lookup: a row an older binary registered under such
// an id keeps working, and no migration rewrites one. The gate never calls
// either arm, so no hook's verdict depends on it (invariant 3): a refused
// session is confined to the never-registered state.
//
// Typed, because the caller words it by where the id came from: a native id
// (hook payload, harness env) cannot be changed by the session and must not be
// replaced by a hand-chosen alias, while a --session or $BUDDY_SESSION value
// can be (cli's sessionIDRefusal).
var ErrSessionIDRefused = errors.New("session id refused")

// ValidSessionID is the ledger's wrapper of fence.ValidSessionID: nil, or
// ErrSessionIDRefused naming the id one token wide.
func ValidSessionID(id string) error {
	if fence.ValidSessionID(id) {
		return nil
	}
	return fmt.Errorf("%w: %s is not a session id the ledger registers (%s)",
		ErrSessionIDRefused, fence.Field(id, fence.SessionIDCap), fence.SessionIDRule)
}
