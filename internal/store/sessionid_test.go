package store

import (
	"errors"
	"strings"
	"testing"
)

// The wrapper agrees with the predicate both ways, and its refusal is one line
// that names the id one token wide whatever the id holds.
func TestStoreValidSessionID(t *testing.T) {
	for _, id := range []string{"07f63349-294a-489d-8f9b-79a9cb7b37a9", "sess-a"} {
		if err := ValidSessionID(id); err != nil {
			t.Fatalf("control %q refused: %v", id, err)
		}
	}
	for _, id := range []string{"", "sess x", "a\nb", "s-1234abcd", "all", strings.Repeat("a", 129), "x‮y"} {
		err := ValidSessionID(id)
		if !errors.Is(err, ErrSessionIDRefused) {
			t.Fatalf("%q: want ErrSessionIDRefused, got %v", id, err)
		}
		if strings.ContainsAny(err.Error(), "\n\r‮") {
			t.Fatalf("%q: the refusal is not one fenced line: %q", id, err)
		}
	}
}
