package cli

// Unlike adapter inspect, diagnose never opens Buddy's store. A successful
// report can contain incompatibility or uncertainty: exit zero means the report
// was produced, not that hooks enforce, a worker can launch, or work may land.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"time"

	"github.com/JsizzleR/buddy-system/internal/harness"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const usageDiagnose = "usage: buddy diagnose [--harness codex|claude] [--launch <absolute-json-path> | --receipt <absolute-saved-receipt>] [--evidence <absolute-json-path>]\n" +
	"       Offline JSON compatibility report; no migration, session registration, inbox drain, hooks or native commands. Exit 0 means report produced, never readiness."

type diagnosticReport struct {
	Schema         string                     `json:"schema"`
	ObservedAt     string                     `json:"observed_at"`
	Readiness      string                     `json:"readiness"`
	Repository     string                     `json:"repository"`
	Ledger         store.LedgerDiagnostic     `json:"ledger"`
	ReceiptBinding string                     `json:"receipt_binding"`
	InputSHA256    string                     `json:"input_sha256,omitempty"`
	Harness        *harness.HarnessDiagnostic `json:"harness,omitempty"`
}

func cmdDiagnose(args []string, env Env) error {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	kind := fs.String("harness", "", "adapter contract to report")
	launchPath := fs.String("launch", "", "launch configuration; requires harness")
	receiptPath := fs.String("receipt", "", "saved buddy.control.v1 receipt with captured identity")
	evidencePath := fs.String("evidence", "", "optional historical native version/hook capture")
	if help, err := parseFlags(fs, args, usageDiagnose, env); help || err != nil {
		return err
	}
	if err := noStray("diagnose", fs, usageDiagnose); err != nil {
		return err
	}
	if *kind != "" && *kind != "codex" && *kind != "claude" {
		return errors.New("diagnose --harness must be codex or claude")
	}
	if *receiptPath != "" && *launchPath != "" {
		return errors.New("diagnose accepts --launch or --receipt, not both")
	}
	if *launchPath != "" && *kind == "" {
		return errors.New("diagnose --launch requires --harness")
	}
	if *evidencePath != "" && *launchPath == "" && *receiptPath == "" {
		return errors.New("diagnose --evidence requires --launch or --receipt")
	}
	r := diagnosticReport{Schema: harness.DiagnosticSchema, Readiness: "not_established", Repository: "unknown", ReceiptBinding: "unknown"}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	r.ObservedAt = now.UTC().Format(time.RFC3339Nano)
	var launch *harness.Launch
	var identity store.AdapterIdentity
	var evidence *harness.DiagnosticEvidence
	if *receiptPath != "" {
		var saved harness.Saved
		hash, err := diagnosticJSON(*receiptPath, &saved)
		if err != nil {
			return err
		}
		if saved.Schema != harness.Schema || !saved.Identity.Valid() {
			return errors.New("diagnose requires a valid saved adapter receipt")
		}
		if *kind != "" && *kind != saved.Identity.Harness {
			return errors.New("diagnose harness differs from saved receipt")
		}
		*kind = saved.Identity.Harness
		launch = &saved.Launch
		identity = store.AdapterIdentity{SessionID: saved.Identity.SessionID, Incarnation: saved.Identity.Incarnation}
		r.InputSHA256 = hash
	} else if *launchPath != "" {
		launch = &harness.Launch{}
		hash, err := diagnosticJSON(*launchPath, launch)
		if err != nil {
			return err
		}
		r.InputSHA256 = hash
	}
	if launch != nil && !filepath.IsAbs(launch.Worktree) {
		return errors.New("diagnose launch worktree must be absolute")
	}
	var evidenceHash string
	if *evidencePath != "" {
		evidence = &harness.DiagnosticEvidence{}
		hash, err := diagnosticJSON(*evidencePath, evidence)
		if err != nil {
			return err
		}
		evidenceHash = hash
	}
	if *kind != "" {
		d := harness.Diagnose(*kind, launch, evidence)
		d.EvidenceSHA256 = evidenceHash
		r.Harness = &d
	}
	rc, err := resolveRepo(env.Cwd)
	switch {
	case errors.Is(err, errNoLedger):
		r.Repository = "absent"
		r.Ledger = store.LedgerDiagnostic{State: "absent", Identity: "unknown"}
	case err != nil:
		r.Ledger = store.LedgerDiagnostic{State: "discovery_failed", Identity: "unknown"}
	default:
		r.Repository = "observed"
		var wt string
		if launch != nil {
			wt = launch.Worktree
			// Never diagnose one worktree's captured identity against a different
			// tree just because they share a common ledger. Aliases resolve before
			// comparison; lifecycle registration stores git's root spelling.
			want, e1 := filepath.EvalSymlinks(wt)
			got, e2 := filepath.EvalSymlinks(rc.top)
			r.ReceiptBinding = "worktree_mismatch"
			if e1 == nil && e2 == nil && want == got {
				r.ReceiptBinding = "worktree_matches"
				wt = rc.top
			} else {
				identity = store.AdapterIdentity{}
			}
		}
		r.Ledger = store.Diagnose(rc.ledger, identity, wt)
	}
	return json.NewEncoder(env.Stdout).Encode(r)
}

func diagnosticJSON(path string, dst any) (string, error) {
	b, err := harness.ReadDiagnosticFile(path)
	if err != nil {
		return "", err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return "", errors.New("diagnostic input must match the documented JSON schema")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return "", errors.New("diagnostic input must contain exactly one JSON object")
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}
