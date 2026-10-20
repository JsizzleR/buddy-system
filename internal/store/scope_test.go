package store

import (
	"errors"
	"strings"
	"testing"
)

// D-100: backslashes fold BEFORE the absolute check. Each input that used to
// be stored absolute or empty is refused at the strict (adding) doors, and a
// removal door gets the spelling the old code stored, so it can name a
// legacy row. Every other refusal stands at both, and a relative backslash
// spelling is still a scope (the positive controls).
func TestNormalizeScopeFoldsBeforeRefusingAbsolute(t *testing.T) {
	for _, tc := range []struct {
		in, strict, held string
		strictErr        bool // the held door refuses exactly when heldErr
		heldErr          bool
	}{
		{`\etc`, "", "/etc", true, false},
		{`\..\x`, "", "/x", true, false},
		{`\`, "", "", true, false},
		{" \t\\etc ", "", "/etc", true, false},
		{`\/etc`, "", "/etc", true, false},
		{`\\server\share`, "", "/server/share", true, false},
		{"/etc", "", "/etc", true, false},
		{`.\..\x`, "", "", true, true},
		{`a\..\..`, "", "", true, true},
		{`a\..`, "", "", true, true},
		{" ", "", "", true, true},
		{"", "", "", true, true},
		{".", "", "", true, true},
		{"../x", "", "", true, true},
		{`src\file.go`, "src/file.go", "src/file.go", false, false},
		{`src\pkg\`, "src/pkg", "src/pkg", false, false},
	} {
		got, err := NormalizeScope(tc.in)
		if (err != nil) != tc.strictErr || got != tc.strict {
			t.Errorf("NormalizeScope(%q) = %q, %v; want %q, err=%v", tc.in, got, err, tc.strict, tc.strictErr)
		}
		got, err = NormalizeHeldScope(tc.in)
		if (err != nil) != tc.heldErr || got != tc.held {
			t.Errorf("NormalizeHeldScope(%q) = %q, %v; want %q, err=%v", tc.in, got, err, tc.held, tc.heldErr)
		}
	}
	// The refusal says why a path that does not start with "/" is absolute.
	if _, err := NormalizeScope(`\etc`); err == nil || !strings.Contains(err.Error(), "a backslash reads as a slash") {
		t.Errorf("a backslash refusal must say the backslash is a slash: %v", err)
	}
	if _, err := NormalizeScope("/etc"); err == nil || strings.Contains(err.Error(), "backslash") {
		t.Errorf("a plain absolute refusal names no backslash: %v", err)
	}
}

// The adding door: claim refuses a backslash-absolute scope and writes
// nothing; a relative backslash spelling beside it is granted.
func TestClaimRefusesABackslashAbsoluteScope(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	for _, sc := range []string{`\etc`, `\..\x`, `\`} {
		if err := st.ClaimMode(a.SessionID, a.Incarnation, "bad", "d", []string{"src", sc}, false); !errors.Is(err, errAbsoluteScope) {
			t.Errorf("claim --scope %q: want the absolute refusal, got %v", sc, err)
		}
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM claims WHERE slug='bad'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a refused claim wrote %d rows (%v)", n, err)
	}
	if err := st.ClaimMode(a.SessionID, a.Incarnation, "ok", "d", []string{`src\x.go`}, false); err != nil {
		t.Fatalf("positive control: a relative backslash scope is granted: %v", err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM claim_scopes cs JOIN claims c ON c.claim_id=cs.claim_id
		WHERE c.slug='ok' AND cs.scope='src/x.go'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the granted claim must hold src/x.go: %d (%v)", n, err)
	}
}

// The removal door: a claim holding legacy "/x" and "" rows (stored through
// the bug) narrows by the backslash spelling that made them, one row at a
// time, never touching a relative row of the same name; a request with one
// scope not held removes nothing.
func TestReleaseScopeNamesALegacyAbsoluteRow(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.ClaimMode(a.SessionID, a.Incarnation, "old", "d", []string{"src", "x"}, false); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := st.db.QueryRow(`SELECT claim_id FROM claims WHERE slug='old' AND state='open'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, sc := range []string{"/x", ""} {
		if _, err := st.db.Exec(`INSERT INTO claim_scopes (claim_id, scope, folded) VALUES (?,?,?)`, id, sc, sc); err != nil {
			t.Fatal(err)
		}
	}
	held := func() string {
		t.Helper()
		rows, err := st.db.Query(`SELECT scope FROM claim_scopes WHERE claim_id=? ORDER BY scope`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, "["+s+"]")
		}
		return strings.Join(out, " ")
	}
	release := func(scopes ...string) error {
		_, _, err := st.ReleaseScopesID(a.SessionID, a.Incarnation, "old", scopes)
		return err
	}
	var notHeld ErrScopeNotHeld
	if err := release("/src"); !errors.As(err, &notHeld) || held() != "[] [/x] [src] [x]" {
		t.Fatalf("/src is not src: %v, held %s", err, held())
	}
	if err := release(`\..\x`, "nothere"); !errors.As(err, &notHeld) || held() != "[] [/x] [src] [x]" {
		t.Fatalf("one scope not held must remove nothing: %v, held %s", err, held())
	}
	if err := release(`\..\x`); err != nil || held() != "[] [src] [x]" {
		t.Fatalf(`\..\x must remove exactly the legacy /x row: %v, held %s`, err, held())
	}
	if err := release(`\`); err != nil || held() != "[src] [x]" {
		t.Fatalf(`\ must remove exactly the legacy empty row: %v, held %s`, err, held())
	}
	if err := release(`.\..\x`); err == nil || held() != "[src] [x]" {
		t.Fatalf("an escaping scope stays refused at the removal door: %v, held %s", err, held())
	}
}
