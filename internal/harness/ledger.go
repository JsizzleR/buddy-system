package harness

// CLILedger consumes only versioned JSON. It does not scrape hello, msg, who,
// or sent receipts, and never queries the current incarnation to repair one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type CLILedger struct{ Binary, Worktree string }

func (l CLILedger) call(ctx context.Context, args ...string) (json.RawMessage, error) {
	if !filepath.IsAbs(l.Binary) || !filepath.IsAbs(l.Worktree) {
		return nil, errors.New("Buddy binary and worktree must be absolute")
	}
	cmd := exec.CommandContext(ctx, l.Binary, args...)
	cmd.Dir = l.Worktree
	// Cancellation kills the child, but Output also waits for its stdout to
	// close; a grandchild holding that pipe would keep a "bounded" call
	// blocked anyway (F-30). WaitDelay bounds that second wait too.
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("Buddy refused: %s", e.Stderr)
		}
		return nil, err
	}
	if !json.Valid(b) {
		return nil, errors.New("Buddy returned an invalid structured receipt")
	}
	return b, nil
}
func (l CLILedger) Begin(ctx context.Context, harness, id, label string) (Identity, error) {
	b, err := l.call(ctx, "lifecycle", "begin", "--harness", harness, "--session", id, "--label", label)
	var out Identity
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	return out, err
}
func (l CLILedger) Event(ctx context.Context, i Identity, event string) error {
	_, err := l.call(ctx, "lifecycle", event, "--harness", i.Harness, "--session", i.SessionID, "--incarnation", i.Incarnation)
	return err
}

// Renew passes the native spellings through unchanged: buddy places them the
// way its gate adjudicates a write, from this worktree as cwd (D-078).
func (l CLILedger) Renew(ctx context.Context, i Identity, paths []string) (RenewResult, error) {
	args := []string{"lifecycle", "renew", "--harness", i.Harness, "--session", i.SessionID, "--incarnation", i.Incarnation}
	for _, p := range paths {
		args = append(args, "--path", p)
	}
	b, err := l.call(ctx, args...)
	if err != nil {
		return RenewResult{}, err
	}
	var v struct {
		Schema        string   `json:"schema"`
		SessionID     string   `json:"session_id"`
		Incarnation   string   `json:"incarnation"`
		Event         string   `json:"event"`
		RenewedClaims *int     `json:"renewed_claims"`
		PlacedPaths   []string `json:"placed_paths"`
		Unresolved    []string `json:"unresolved_paths"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return RenewResult{}, err
	}
	if v.Schema != "buddy.lifecycle.v1" || v.SessionID != i.SessionID || v.Incarnation != i.Incarnation || v.Event != "renew" || v.RenewedClaims == nil {
		return RenewResult{}, errors.New("Buddy renewal receipt is not the captured identity's")
	}
	return RenewResult{Renewed: *v.RenewedClaims, Placed: v.PlacedPaths, Unresolved: v.Unresolved}, nil
}

func (l CLILedger) Inspect(ctx context.Context, i Identity) (json.RawMessage, error) {
	b, err := l.call(ctx, "adapter", "inspect", "--session", i.SessionID, "--incarnation", i.Incarnation)
	if err != nil {
		return nil, err
	}
	var v struct {
		Schema   string `json:"schema"`
		Identity struct {
			SessionID   string `json:"session_id"`
			Incarnation string `json:"incarnation"`
		} `json:"identity"`
		Lifecycle struct {
			Value string `json:"value"`
		} `json:"lifecycle"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if v.Schema != "buddy.adapter.v1" || v.Identity.SessionID != i.SessionID || v.Identity.Incarnation != i.Incarnation || v.Lifecycle.Value != "live" {
		return nil, errors.New("Buddy receipt is not the captured live identity")
	}
	return b, nil
}
func (l CLILedger) Queued(ctx context.Context, i Identity, id int64) (bool, error) {
	b, err := l.call(ctx, "adapter", "inbox", "--session", i.SessionID, "--incarnation", i.Incarnation, "--message", strconv.FormatInt(id, 10))
	if err != nil {
		return false, err
	}
	var v struct {
		Schema         string `json:"schema"`
		QueuedMessages struct {
			Items []struct {
				MessageID int64 `json:"message_id"`
			} `json:"items"`
		} `json:"messages"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return false, err
	}
	if v.Schema != "buddy.adapter.v1" {
		return false, errors.New("unknown Buddy adapter schema")
	}
	for _, m := range v.QueuedMessages.Items {
		if m.MessageID == id {
			return true, nil
		}
	}
	return false, nil
}
