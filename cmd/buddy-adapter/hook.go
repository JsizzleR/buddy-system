package main

// The launch's only safety hook reads its immutable receipt. Native lifecycle
// handlers are intentionally not installed alongside the controller: duplicate
// unfenced hello/bye hooks could lend delayed events a successor's registration.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/JsizzleR/buddy-system/internal/harness"
)

func runHook(args []string, in io.Reader, out, errout io.Writer) error {
	fs := flag.NewFlagSet("buddy-adapter hook", flag.ContinueOnError)
	fs.SetOutput(errout)
	receipt := fs.String("receipt", "", "immutable lifecycle receipt path")
	buddy := fs.String("buddy", "", "absolute Buddy binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*receipt) || !filepath.IsAbs(*buddy) {
		return errors.New("hook requires absolute receipt and buddy paths")
	}
	b, err := io.ReadAll(io.LimitReader(in, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("hook input too large")
	}
	var input struct {
		SessionID       string `json:"session_id"`
		Cwd             string `json:"cwd"`
		Event           string `json:"hook_event_name"`
		AgentID         string `json:"agent_id"`
		ParentSessionID string `json:"parent_session_id"`
		ParentThreadID  string `json:"parent_thread_id"`
	}
	if err = json.Unmarshal(b, &input); err != nil {
		return err
	}
	if input.Event != "PreToolUse" || input.AgentID != "" || input.ParentSessionID != "" || input.ParentThreadID != "" {
		return errors.New("adapter hook supports only top-level PreToolUse")
	}
	data, err := os.ReadFile(*receipt)
	if err != nil {
		return err
	}
	var saved harness.Saved
	if err = json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if saved.Schema != harness.Schema || !saved.Identity.Valid() || saved.Identity.SessionID != input.SessionID || filepath.Clean(saved.Launch.Worktree) != filepath.Clean(input.Cwd) {
		return errors.New("hook does not match captured worker and worktree")
	}
	// The safety steps (identity check, then gate) share one budget and fail
	// closed when it runs out. The advisory endpoint step between them runs on
	// its own clock, which is not charged to the gate (F-29): it used to share
	// the deadline, so a slow lock-holder on its write transaction made the
	// gate start late or not at all, and the tool call was denied for an
	// observation that is allowed to fail.
	started := time.Now()
	inspectCtx, cancel := context.WithTimeout(context.Background(), harness.HookGateBudget)
	l := harness.CLILedger{Binary: *buddy, Worktree: saved.Launch.Worktree}
	_, err = l.Inspect(inspectCtx, saved.Identity)
	cancel()
	if err != nil {
		return err
	}
	spent := time.Since(started)
	if saved.Identity.Harness == "claude" {
		// Endpoint discovery is advisory. No exported inbox is observed absence;
		// an unbound/unsupported process is unknown and never an invented socket.
		endpointCtx, endpointCancel := context.WithTimeout(context.Background(), harness.HookEndpointBudget)
		cmd := exec.CommandContext(endpointCtx, *buddy, "adapter", "endpoint", "--session", saved.Identity.SessionID, "--incarnation", saved.Identity.Incarnation)
		cmd.Dir = saved.Launch.Worktree
		// This is the native payload already checked against the captured
		// receipt, not an id inferred from an ancestor or the sender's env.
		cmd.Env = append(os.Environ(), "BUDDY_HARNESS=claude", "CLAUDE_CODE_SESSION_ID="+input.SessionID)
		cmd.Stderr = errout
		// A killed child's inherited stderr must not hold the step open.
		cmd.WaitDelay = harness.HookEndpointBudget
		_ = cmd.Run()
		endpointCancel()
	}
	gateCtx, gateCancel := context.WithTimeout(context.Background(), harness.HookGateBudget-spent)
	defer gateCancel()
	cmd := exec.CommandContext(gateCtx, *buddy, "gate")
	cmd.Dir = saved.Launch.Worktree
	cmd.Env = append(os.Environ(), "BUDDY_HARNESS="+saved.Identity.Harness)
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stdout = out
	cmd.Stderr = errout
	return cmd.Run()
}
