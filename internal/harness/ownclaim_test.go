package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D-235: a present own_claim is exactly one key, spelled own_claim, holding
// one of three strings. Every refusal row is a value a plain json.Unmarshal
// reads as something the file did not say: null and "" as absent (off), a
// case variant as the field, a second key as the LAST one. The allow rows are
// the positive controls: the same decoder accepts what it should.
func TestDecodeLaunchOwnClaimStrict(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		refused          bool
	}{
		{name: "absent is off", body: `{"worktree":"/w"}`, want: ""},
		{name: "off", body: `{"own_claim":"off"}`, want: "off"},
		{name: "warn", body: `{"own_claim":"warn"}`, want: "warn"},
		{name: "deny", body: `{"own_claim":"deny"}`, want: "deny"},
		{name: "null", body: `{"own_claim":null}`, refused: true},
		{name: "empty string", body: `{"own_claim":""}`, refused: true},
		{name: "a number", body: `{"own_claim":1}`, refused: true},
		{name: "a number no float64 holds", body: `{"own_claim":1e1000}`, refused: true},
		{name: "another word", body: `{"own_claim":"Deny"}`, refused: true},
		{name: "case-variant key", body: `{"OWN_CLAIM":"deny"}`, refused: true},
		{name: "duplicate key", body: `{"own_claim":"deny","own_claim":"off"}`, refused: true},
		{name: "deny then a case-variant off", body: `{"own_claim":"deny","Own_Claim":"off"}`, refused: true},
		{name: "unrelated key kept", body: `{"own_claimed":"x","own_claim":"deny"}`, want: "deny"},
	} {
		t.Run(test.name, func(t *testing.T) {
			l, err := DecodeLaunch([]byte(test.body))
			if test.refused {
				// ErrOwnClaim, not just any error: a non-string once failed
				// the plain decode first, as a type error (Fable cross-check).
				if !errors.Is(err, ErrOwnClaim) {
					t.Fatalf("%s: %v (%q), want an ErrOwnClaim refusal", test.body, err, l.OwnClaim)
				}
				return
			}
			if err != nil || l.OwnClaim != test.want {
				t.Fatalf("%s: %q %v, want %q", test.body, l.OwnClaim, err, test.want)
			}
		})
	}
}

// The receipt is reread by the hook on every call: the same test applies to
// the one object Go decodes as its launch, whatever its key's case.
func TestDecodeSavedOwnClaimStrict(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		refused          bool
	}{
		{name: "no launch", body: `{"schema":"buddy.control.v1"}`, want: ""},
		{name: "launch without own_claim", body: `{"launch":{"worktree":"/w"}}`, want: ""},
		{name: "launch deny", body: `{"launch":{"own_claim":"deny"}}`, want: "deny"},
		{name: "launch null own_claim", body: `{"launch":{"own_claim":null}}`, refused: true},
		{name: "case-variant launch key", body: `{"LAUNCH":{"own_claim":null}}`, refused: true},
		{name: "two launches", body: `{"launch":{"own_claim":"deny"},"launch":{}}`, refused: true},
		{name: "a non-string", body: `{"launch":{"own_claim":true}}`, refused: true},
		{name: "a number no float64 holds", body: `{"launch":{"own_claim":1e1000}}`, refused: true},
		{name: "launch null", body: `{"launch":null}`, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, err := DecodeSaved([]byte(test.body))
			if test.refused {
				if !errors.Is(err, ErrOwnClaim) {
					t.Fatalf("accepted %s as %q", test.body, s.Launch.OwnClaim)
				}
				return
			}
			if err != nil || s.Launch.OwnClaim != test.want {
				t.Fatalf("%s: %q %v, want %q", test.body, s.Launch.OwnClaim, err, test.want)
			}
		})
	}
}

// Codex warn is refused (the gate is silent under Codex, D-228 D9); Claude
// warn is the harness rule's to allow and admission's to grant.
func TestCheckOwnClaim(t *testing.T) {
	for _, test := range []struct {
		harness, value string
		ok             bool
	}{
		{"claude", "", true}, {"claude", "off", true}, {"claude", "warn", true}, {"claude", "deny", true},
		{"codex", "", true}, {"codex", "off", true}, {"codex", "deny", true}, {"codex", "warn", false},
		{"claude", "Deny", false}, {"codex", "x", false},
	} {
		if err := CheckOwnClaim(test.harness, test.value); (err == nil) != test.ok {
			t.Errorf("%s %q: %v", test.harness, test.value, err)
		}
	}
	if (Launch{}).OwnClaimPosture() != OwnClaimOff || (Launch{OwnClaim: "deny"}).OwnClaimPosture() != OwnClaimDeny {
		t.Fatal("absent must be off, a typed value itself")
	}
}

// A Claude warn launch needs TierOwnClaimWarn from every record of its
// version, refused before the `.start` intent or the ledger; with the tier the
// same launch passes admission (positive control); deny needs no tier; a
// Codex warn is refused by the harness rule before admission is consulted.
func TestControllerOwnClaimPostureBeforeSideEffects(t *testing.T) {
	for _, test := range []struct {
		name, posture string
		tiers         []string
		code          string
	}{
		{"warn without the tier", "warn", []string{TierStart}, "tier_not_granted"},
		{"warn with the tier", "warn", []string{TierStart, TierOwnClaimWarn}, ""},
		{"deny needs no tier", "deny", []string{TierStart}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, launch, _ := claudeFixture(t)
			launch.OwnClaim = test.posture
			withAdmissions(t, testRecord(t, "claude", claudeContractVersion, b.(*claudeBackend).executable, test.tiers...))
			receipt := filepath.Join(t.TempDir(), "receipt.json")
			ledger := &countingLedger{}
			c, err := NewController(b, ledger, receipt)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_, err = c.Start(context.Background(), "claude", launch, "worker")
			if test.code != "" {
				if AdmissionCode(err) != test.code {
					t.Fatalf("start: %v", err)
				}
				if _, serr := os.Lstat(receipt + ".start"); !os.IsNotExist(serr) || ledger.calls != 0 {
					t.Fatalf("refused start left an intent (%v) or touched the ledger (%d)", serr, ledger.calls)
				}
				return
			}
			if AdmissionCode(err) != "" {
				t.Fatalf("positive control refused by admission: %v", err)
			}
			if _, serr := os.Lstat(receipt + ".start"); os.IsNotExist(serr) && err != nil && ledger.calls == 0 {
				t.Fatalf("positive control stopped before the intent: %v", err)
			}
		})
	}
	t.Run("codex warn refused before admission", func(t *testing.T) {
		b, _, launch := codexFixture(t)
		launch.OwnClaim = "warn"
		withAdmissions(t) // no record at all: a refusal naming admission would mean the order is wrong
		receipt := filepath.Join(t.TempDir(), "receipt.json")
		c, err := NewController(b, &countingLedger{}, receipt)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, err = c.Start(context.Background(), "codex", launch, "worker")
		if err == nil || AdmissionCode(err) != "" || !strings.Contains(err.Error(), "D-228 D9") {
			t.Fatalf("codex warn: %v", err)
		}
		if _, serr := os.Lstat(receipt + ".start"); !os.IsNotExist(serr) {
			t.Fatalf("refused start left an intent: %v", serr)
		}
	})
}

// Resume reads the posture from the receipt, strictly, and a warn worker
// resumes only where warn is earned.
func TestControllerResumeOwnClaim(t *testing.T) {
	for _, test := range []struct {
		name, launchJSON string
		tiers            []string
		code             string
		refused          bool
		want             string // the refusal's text, where its code is not enough
	}{
		{"warn without the tier", `"warn"`, []string{TierStart, TierResume}, "tier_not_granted", true, ""},
		{"warn with the tier", `"warn"`, []string{TierStart, TierResume, TierOwnClaimWarn}, "", false, ""},
		// The receipt's strict read: a plain decode reads null as off, which
		// passes admission and is refused only later, by the empty journal,
		// so the text, not just "refused", tells the two apart (a mutant that
		// put the plain decode back survived the first draft of this row).
		{"null posture", `null`, []string{TierStart, TierResume, TierOwnClaimWarn}, "", true, "invalid captured receipt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, launch, _ := claudeFixture(t)
			withAdmissions(t, testRecord(t, "claude", claudeContractVersion, b.(*claudeBackend).executable, test.tiers...))
			receipt := filepath.Join(t.TempDir(), "receipt.json")
			id := Identity{"buddy.lifecycle.v1", "claude", "00000000-0000-4000-8000-000000000001", "captured"}
			data, _ := json.Marshal(Saved{Schema: Schema, Identity: id, Launch: launch})
			data = []byte(strings.Replace(string(data), `"launch":{`, `"launch":{"own_claim":`+test.launchJSON+`,`, 1))
			if err := os.WriteFile(receipt, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receipt+".events", nil, 0600); err != nil {
				t.Fatal(err)
			}
			ledger := &fakeLedger{}
			c, err := NewController(b, ledger, receipt)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Resume(context.Background())
			c.Close()
			if test.code != "" && AdmissionCode(err) != test.code {
				t.Fatalf("resume: %v", err)
			}
			if test.want != "" && (err == nil || err.Error() != test.want) {
				t.Fatalf("resume: %v, want %q", err, test.want)
			}
			if test.refused && (err == nil || len(ledger.events) != 0) {
				t.Fatalf("resume must refuse before the ledger: %v %v", err, ledger.events)
			}
			// Positive control: past admission, the empty journal refuses it.
			if !test.refused && (err == nil || AdmissionCode(err) != "") {
				t.Fatalf("positive control should pass admission and fail later: %v", err)
			}
		})
	}
}

// D-235: the posture is the start's for the worker's whole life. A
// SUCCESSFUL resume hands the backend the receipt's launch, deny included,
// and leaves the receipt's posture as captured, so the hook (which rereads
// the receipt per call) runs the start posture after it (Codex code pass,
// finding 2: the other resume rows all refuse). Positive control: the
// start's own receipt says deny before the resume.
func TestControllerResumeKeepsOwnClaim(t *testing.T) {
	ctx := context.Background()
	b, l := &fakeBackend{}, &fakeLedger{queued: true}
	path := filepath.Join(t.TempDir(), "receipt.json")
	c, err := NewController(b, l, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Start(ctx, "codex", Launch{Worktree: t.TempDir(), OwnClaim: OwnClaimDeny}, "test"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	posture := func() string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := DecodeSaved(data)
		if err != nil {
			t.Fatal(err)
		}
		return saved.Launch.OwnClaimPosture()
	}
	if got := posture(); got != OwnClaimDeny {
		t.Fatalf("positive control: the start receipt says %s", got)
	}
	b2 := &fakeBackend{}
	c2, err := NewController(b2, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if b2.resumed == nil || b2.resumed.OwnClaimPosture() != OwnClaimDeny {
		t.Fatalf("resume handed the backend %+v, want own_claim deny", b2.resumed)
	}
	if got := posture(); got != OwnClaimDeny {
		t.Fatalf("after resume the receipt says %s", got)
	}
}

// The refusal names the receipt's value, and the hook's refusal reaches the
// worker's context: one line, capped, whatever the value holds (Fable
// cross-check: it was printed raw with %s).
func TestOwnClaimRefusalFenced(t *testing.T) {
	long := strings.Repeat("x", 4096)
	for _, body := range []string{`{"own_claim":"a\u2028b` + long + `"}`, `{"own_claim":["` + long + `"]}`} {
		_, err := DecodeLaunch([]byte(body))
		if !errors.Is(err, ErrOwnClaim) || len(err.Error()) > 512 || strings.ContainsAny(err.Error(), "\n\u2028") {
			t.Errorf("%.40s: %d bytes, %q", body, len(fmt.Sprint(err)), fmt.Sprint(err))
		}
	}
	if err := CheckOwnClaim("claude", "a\u2028"+long); !errors.Is(err, ErrOwnClaim) || len(err.Error()) > 512 || strings.Contains(err.Error(), "\u2028") {
		t.Errorf("CheckOwnClaim: %d bytes", len(fmt.Sprint(err)))
	}
	// Positive control: a plain bad word is still named.
	if _, err := DecodeLaunch([]byte(`{"own_claim":"Deny"}`)); err == nil || !strings.Contains(err.Error(), "Deny") {
		t.Errorf("control: %v", err)
	}
}

// Resume's own harness rule (Fable cross-check: the Codex rows all ran
// through backends whose precheck refuses first, so deleting it stayed
// green). A backend with no precheck: a Codex receipt at warn still refuses,
// naming D9, before the ledger. Positive control: the same receipt at deny
// gets past admission.
func TestControllerResumeCodexWarn(t *testing.T) {
	for _, posture := range []string{OwnClaimWarn, OwnClaimDeny} {
		b, l := &fakeBackend{}, &fakeLedger{queued: true}
		path := filepath.Join(t.TempDir(), "receipt.json")
		c, err := NewController(b, l, path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Start(context.Background(), "codex", Launch{Worktree: t.TempDir(), OwnClaim: OwnClaimDeny}, "test"); err != nil {
			t.Fatal(err)
		}
		c.Close()
		data, _ := os.ReadFile(path)
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"own_claim":"deny"`, `"own_claim":"`+posture+`"`, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		before := len(l.events)
		c2, err := NewController(&fakeBackend{}, l, path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c2.Resume(context.Background())
		c2.Close()
		if posture == OwnClaimWarn && (err == nil || !strings.Contains(err.Error(), "D-228 D9") || len(l.events) != before) {
			t.Fatalf("codex warn resume: %v, ledger events %d -> %d", err, before, len(l.events))
		}
		if posture == OwnClaimDeny && err != nil {
			t.Fatalf("positive control: codex deny resume: %v", err)
		}
	}
}
