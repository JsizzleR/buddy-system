package harness

// F-23. The native executable used to be resolved twice: once when its
// --version was checked and again when it was launched. ~/.local/bin/claude
// is a symlink the auto-updater re-points (it moved 2.1.291 -> 2.1.292 on
// 2026-10-06), so the version that was checked and the binary that ran could
// differ. A relative path was also checked against the adapter's cwd but
// executed relative to the worktree. Now the constructors require an absolute
// path, resolve every symlink ONCE, and both the version probe and the launch
// use that same resolved file. Its identity (device, inode, size, mtime,
// mode) is taken before the probe and re-checked just before the launch.
//
// REMAINING RACE, stated rather than claimed closed: a same-user process can
// still rewrite the resolved file's bytes in place between that re-check and
// exec, keeping inode and (with care) size and mtime. Nothing short of
// executing from a sealed copy closes that, and the ledger trusts the machine
// user (invariant 6). This narrows the updater case; it is not tamper-proofing.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func resolveExecutable(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("native harness executable must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve native harness executable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect native harness executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("native harness executable must resolve to an executable regular file")
	}
	return resolved, nil
}

func observeExecutable(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect native harness executable: %w", err)
	}
	return info, nil
}

// sameExecutable refuses a launch when the file whose version was checked is
// no longer the file at that path.
func sameExecutable(path string, checked os.FileInfo) error {
	now, err := os.Stat(path)
	if err != nil || !os.SameFile(checked, now) || checked.Size() != now.Size() || !checked.ModTime().Equal(now.ModTime()) || checked.Mode() != now.Mode() {
		return errors.New("native harness executable changed between its version check and launch; refusing to run an unchecked binary")
	}
	return nil
}

// probeNativeVersion owns its diagnostic child just as the live transport owns
// its worker (V-08). CommandContext.Output killed only the direct child, then
// waited forever for a background descendant holding stdout. The direct child
// is observed WITHOUT reaping, so its group id stays reserved until the final
// group signal. This is the only Wait; no later signal uses that group number.
// Version output is small, bounded, and diagnostic: no grace is owed to a
// --version child at its deadline. An escaped stdout holder cannot hold the
// reader open indefinitely either; its absence is not inferred from this close.
func probeNativeVersion(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	cmd := exec.Command(executable, "--version")
	// Even a version probe of a pinned build must not let it update itself
	// (D-079); the worker's own environment carries the same setting.
	cmd.Env = append(os.Environ(), "DISABLE_AUTOUPDATER=1")
	cmd.Stdout, cmd.Stderr = writer, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		writer.Close()
		return "", err
	}
	writer.Close()
	const limit = 4096
	type readResult struct {
		data []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(reader, limit+1))
		read <- readResult{data, err}
	}()
	exited := make(chan error, 1)
	go func() { exited <- awaitExit(cmd.Process.Pid) }()
	var result *readResult
	var observedErr error
wait:
	for {
		select {
		case err := <-exited:
			observedErr = err
			break wait
		case out := <-read:
			result = &out
			if out.err != nil || len(out.data) > limit {
				break wait
			}
		case <-ctx.Done():
			break wait
		}
	}
	// The leader remains unreaped through this final group signal.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	waitErr := cmd.Wait()
	if result == nil {
		select {
		case out := <-read:
			result = &out
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
			return "", errors.New("native version output remained open after its process exited")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if observedErr != nil {
		return "", fmt.Errorf("observe native version process exit: %w", observedErr)
	}
	if len(result.data) > limit {
		return "", errors.New("native version output exceeds its 4096-byte bound")
	}
	if result.err != nil {
		return "", result.err
	}
	if waitErr != nil {
		return "", waitErr
	}
	return strings.TrimSpace(string(result.data)), nil
}
