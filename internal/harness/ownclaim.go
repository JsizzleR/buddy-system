package harness

// ownclaim.go — the own-claim posture of a worker this adapter launches
// (D-228's O4, decided in D-235).
//
// WHY. D-228 taught the gate to catch an edit no claim of the calling session
// covers, while a peer holds a claim: a line in the model's context by default,
// a refusal under BUDDY_OWN_CLAIM=deny. The adapter's hook pinned the knob OFF
// for its workers, because the adapter's qualified gate contract was
// deny-or-nothing and no suite had seen a warn through it; so D-228's incident
// (a lane edits a file nobody claimed, a second lane claims it unaware) stayed
// open by exactly this route, and a coordinator holding orc-<project> arms
// every worker it launches.
//
// THE RULE (the operator's choices, 2026-11-18 21:14Z: typed field, per-build
// tier, default off, Codex warn refused).
//   - The posture is a TYPED launch field, `own_claim`, and nothing else. An
//     inherited BUDDY_OWN_CLAIM never reaches a worker's gate (the hook appends
//     the receipt's value last): the coordinator's shell differs per run and
//     nothing records it, and every qualify script's clean_env strips it, so a
//     qualification would measure one posture and production run another.
//     Refused: a hook argv flag, which changes the six-word gate command every
//     committed admission record pins, so every admitted build would refuse.
//   - Absent is `off`: every existing launch, receipt and fixture runs exactly
//     as before, the same bytes at the gate.
//   - Codex: off or deny. `warn` is refused at launch: under BUDDY_HARNESS=codex
//     the gate writes nothing (D-228 D9), so a typed warn would read as
//     enforcement and be silence. If O2 measures that Codex delivers the
//     context, D9 lifts in the gate and this refusal is the one line to move.
//   - Claude: off and deny on every admitted build (deny is the envelope the
//     start tier measures per build); warn only on a build every record of which
//     grants TierOwnClaimWarn, earned by its own suite. No committed record
//     grants it yet (D-081 writes no record over an existing one), so warn
//     arrives with the next Claude qualification.
//
// STRICT, because a lax read is a silent off. Go decodes `"own_claim": null`
// and `""` into an empty string (which is "absent", so off), folds a key's
// case (`"OWN_CLAIM"` fills the field) and keeps the LAST of a duplicated key,
// where a Python checker reading the same file keeps another (Codex design
// F1, panel lens a4). So a present key must be exactly one, spelled
// `own_claim`, holding a JSON string that is one of the three words; the same
// test runs on the launch file and on every receipt read (the hook rereads the
// receipt on each call). Which keys Go maps to a field is asked of Go's own
// decoder (goMaps), never re-implemented by a second folding rule.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// Postures a launch may name; "" (absent) is off.
const (
	OwnClaimOff  = "off"
	OwnClaimWarn = "warn"
	OwnClaimDeny = "deny"
)

// ErrOwnClaim marks every refusal of a posture, so the hook can say which
// refusal it was (the hook's other failures stay one generic line).
var ErrOwnClaim = errors.New("own_claim")

// OwnClaimPosture is the value the worker's gate runs under.
func (l Launch) OwnClaimPosture() string {
	if l.OwnClaim == "" {
		return OwnClaimOff
	}
	return l.OwnClaim
}

// CheckOwnClaim refuses a posture this harness cannot honour as typed. It is
// the harness rule only; the warn tier is admission's (precheck).
func CheckOwnClaim(harness, value string) error {
	switch value {
	case "", OwnClaimOff, OwnClaimDeny:
		return nil
	case OwnClaimWarn:
		if harness == "codex" {
			return fmt.Errorf("%w: \"warn\" is refused for a Codex worker: the gate writes no warn under Codex (D-228 D9), so it would be silence; use \"deny\" or \"off\"", ErrOwnClaim)
		}
		return nil
	}
	return fmt.Errorf("%w: must be \"off\", \"warn\" or \"deny\", not %q", ErrOwnClaim, fence.Line(value, 64))
}

// DecodeLaunch reads a launch file: json.Unmarshal into Launch, then the
// strict own_claim test over the same bytes.
//
// The strict test runs FIRST: a non-string value (`1`, `true`, `[]`) fails
// the plain decode with a type error, which is not ErrOwnClaim, so the hook
// would print its generic line for a posture refusal (Fable cross-check).
func DecodeLaunch(b []byte) (Launch, error) {
	var l Launch
	strictErr := strictOwnClaim(b)
	if errors.Is(strictErr, ErrOwnClaim) {
		return l, strictErr
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, err
	}
	return l, strictErr
}

// DecodeSaved reads a receipt: json.Unmarshal into Saved, then the strict
// own_claim test over the one object Go decodes as its launch.
func DecodeSaved(b []byte) (Saved, error) {
	var s Saved
	strictErr := strictSaved(b) // before the plain decode, as DecodeLaunch says
	if errors.Is(strictErr, ErrOwnClaim) {
		return s, strictErr
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, strictErr
}

// strictSaved is the strict own_claim test of the one object Go decodes as
// a receipt's launch.
func strictSaved(b []byte) error {
	launches, err := goMappedValues(b, func(k string) bool {
		var probe struct {
			V *struct{} `json:"launch"`
		}
		return json.Unmarshal([]byte(`{`+quoteKey(k)+`:{}}`), &probe) == nil && probe.V != nil
	})
	if err != nil {
		return err
	}
	if len(launches) > 1 {
		return fmt.Errorf("%w: the receipt names its launch more than once", ErrOwnClaim)
	}
	if len(launches) == 1 && !bytes.Equal(bytes.TrimSpace(launches[0].value), []byte("null")) {
		return strictOwnClaim(launches[0].value)
	}
	return nil
}

// strictOwnClaim tests one launch object's own_claim key, if any.
func strictOwnClaim(obj []byte) error {
	found, err := goMappedValues(obj, func(k string) bool {
		var probe struct {
			V string `json:"own_claim"`
		}
		return json.Unmarshal([]byte(`{`+quoteKey(k)+`:"x"}`), &probe) == nil && probe.V == "x"
	})
	if err != nil {
		return err
	}
	switch {
	case len(found) == 0:
		return nil
	case len(found) > 1:
		return fmt.Errorf("%w: named more than once", ErrOwnClaim)
	case found[0].key != "own_claim":
		return fmt.Errorf("%w: must be spelled own_claim, not %q", ErrOwnClaim, fence.Line(found[0].key, 64))
	}
	// Straight into a string: any value that is not one (a number too large
	// for a float64, 1e1000, included) is this refusal, never a decoder's
	// own error (Codex re-verify 1). null decodes to "", refused below.
	var s string
	if json.Unmarshal(found[0].value, &s) != nil || (s != OwnClaimOff && s != OwnClaimWarn && s != OwnClaimDeny) {
		// Fenced and capped: the value is the receipt's, and the hook's
		// refusal reaches the worker's context (Fable cross-check).
		return fmt.Errorf("%w: must be the string \"off\", \"warn\" or \"deny\", not %s", ErrOwnClaim, fence.Line(strings.TrimSpace(string(found[0].value)), 64))
	}
	return nil
}

type keyed struct {
	key   string
	value json.RawMessage
}

// goMappedValues lists every top-level member of the object b whose key Go's
// decoder would assign to the field maps names, in order, duplicates included.
func goMappedValues(b []byte, maps func(string) bool) ([]keyed, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("launch must be a JSON object")
	}
	var out []keyed
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if maps(k) {
			out = append(out, keyed{k, raw})
		}
	}
	return out, nil
}

func quoteKey(k string) string {
	b, _ := json.Marshal(k)
	return string(b)
}
