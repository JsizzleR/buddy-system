package harness

// Issue #133 (D-216, amends D-077): a spawn whose start fails before any child
// exists used to leave the ownership record at "intent", and every later
// resume refused it as "a worker may have started", although the adapter
// itself saw that none did. A transient cause (a worktree missing for a
// moment) thereby cost the captured session for good. Now a DEFINITE start
// failure publishes the terminal phase not_started, which resume admits; an
// uncertain one, and a not_started that cannot be published, keep the intent.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resumeRecorder is a recorder bound to a fresh journal, with a first worker
// already spawned, recorded and gone, so the next resume admits: the
// positive control every case below starts from.
func resumeRecorder(t *testing.T) (ownershipRecorder, Identity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipt.json")
	j, err := os.Create(path + ".events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	journal, err := fileIdentity(j)
	if err != nil {
		t.Fatal(err)
	}
	rec := ownershipRecorder{receiptPath: path, journal: journal}
	id := Identity{"buddy.lifecycle.v1", "codex", "thread-1", "inc-1"}
	if err := fakeSpawn(authorizationFor(t, rec, "resume", &id)); err != nil {
		t.Fatal(err)
	}
	if refusal := admitted(t, rec, id); refusal != nil {
		t.Fatalf("positive control: a gone worker should admit: %v", refusal)
	}
	return rec, id
}

func admitted(t *testing.T, rec ownershipRecorder, id Identity) *AdmissionRefusal {
	t.Helper()
	loaded, refusal := rec.load(id)
	if refusal != nil {
		return refusal
	}
	return classifyOwnership(loaded, observeOwnership(loaded))
}

func phaseOf(t *testing.T, rec ownershipRecorder, id Identity) *OwnershipRecord {
	t.Helper()
	loaded, refusal := rec.load(id)
	if refusal != nil {
		t.Fatal(refusal)
	}
	return loaded
}

// The issue's reproduction, through the one launch path: a resume whose
// worktree is missing fails in cmd.Start, the cause goes away, and the next
// resume is admitted and launches.
func TestFailedStartPublishesNotStartedAndResumeAdmits(t *testing.T) {
	rec, id := resumeRecorder(t)
	missing := filepath.Join(t.TempDir(), "gone-worktree")
	second := authorizationFor(t, rec, "resume", &id)
	if _, err := StartJSONProcess(context.Background(), second, truePath(), nil, missing, nil); err == nil {
		t.Fatal("expected cmd.Start to fail on a missing worktree")
	}
	got := phaseOf(t, rec, id)
	if got.Phase != "not_started" || got.Attempt != second.template.Attempt || got.Leader != nil || got.StartError == "" {
		t.Fatalf("record after a definite start failure: phase %q attempt %q (want %q) leader %v start_error %q",
			got.Phase, got.Attempt, second.template.Attempt, got.Leader, got.StartError)
	}
	if second.Published() {
		t.Fatal("a launch that started nothing reports a published worker")
	}
	if err := os.MkdirAll(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	if refusal := admitted(t, rec, id); refusal != nil {
		t.Fatalf("a receipt whose last attempt started nothing is refused: %s", refusal.Reason)
	}
	if err := fakeSpawn(authorizationFor(t, rec, "resume", &id)); err != nil {
		t.Fatal(err)
	}
	if got := phaseOf(t, rec, id); got.Phase != "spawned" {
		t.Fatalf("the admitted resume did not record its worker: phase %q", got.Phase)
	}
}

// The intent still stands wherever the adapter cannot say no child exists.
// Each case beside the definite one, so "it kept the intent" is the guard and
// not a start that never failed.
func TestStartFailureKeepsIntentUnlessDefinite(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name     string
		startErr error
		readOnly bool // the directory refuses the not_started publication
		want     string
	}{
		{"definite", boom, false, "not_started"},
		{"uncertain", fmt.Errorf("wrapped: %w", &childUncertainError{boom}), false, "intent"},
		{"not_started_unpublishable", boom, true, "intent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, id := resumeRecorder(t)
			auth := authorizationFor(t, rec, "resume", &id)
			dir := filepath.Dir(rec.receiptPath)
			err := auth.Spawn(truePath(), func() (int, error) {
				if tc.readOnly {
					if err := os.Chmod(dir, 0o500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
				}
				return 0, tc.startErr
			}, func() { t.Fatal("end called with no child") })
			if !errors.Is(err, boom) {
				t.Fatalf("the start error is not returned: %v", err)
			}
			if tc.readOnly && !strings.Contains(err.Error(), "intent stands") {
				t.Fatalf("an unpublishable not_started does not say the intent stands: %v", err)
			}
			if got := phaseOf(t, rec, id); got.Phase != tc.want {
				t.Fatalf("phase %q, want %q", got.Phase, tc.want)
			}
			refusal := admitted(t, rec, id)
			if (refusal == nil) != (tc.want == "not_started") {
				t.Fatalf("admission %v for phase %q", refusal, tc.want)
			}
			if refusal != nil && (!strings.Contains(refusal.Reason, "intent-only") || refusal.Waitable) {
				t.Fatalf("an uncertain start is not refused as intent-only: %+v", refusal)
			}
		})
	}
}

// A not_started record is admitted only in its own shape: one that names a
// leader, an executable or descendants is not what Spawn writes, and is
// refused before anything is probed.
func TestNotStartedRecordShape(t *testing.T) {
	leader := goneProcess(t)
	good := &OwnershipRecord{Schema: OwnershipSchema, Attempt: "a", Phase: "not_started", StartError: "boom"}
	if r := classifyOwnership(good, observeOwnership(good)); r != nil {
		t.Fatalf("positive control: a well-shaped not_started record is refused: %s", r.Reason)
	}
	for name, rec := range map[string]*OwnershipRecord{
		"leader":      {Schema: OwnershipSchema, Attempt: "a", Phase: "not_started", StartError: "boom", Leader: &leader},
		"executable":  {Schema: OwnershipSchema, Attempt: "a", Phase: "not_started", StartError: "boom", Executable: &ExecutableIdentity{Path: "/x"}},
		"descendants": {Schema: OwnershipSchema, Attempt: "a", Phase: "not_started", StartError: "boom", Descendants: &DescendantRecord{}},
		"close":       {Schema: OwnershipSchema, Attempt: "a", Phase: "not_started", StartError: "boom", Close: &CloseReport{}},
		"no_error":    {Schema: OwnershipSchema, Attempt: "a", Phase: "not_started"},
	} {
		if r := classifyOwnership(rec, observeOwnership(rec)); r == nil || r.Waitable {
			t.Errorf("%s: a malformed not_started record is admitted or waitable: %+v", name, r)
		}
	}
}
