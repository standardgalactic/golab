// Package runner executes an external program and returns its outcome as
// a value, so that a nonzero exit status, a timeout, and a failure to start
// are three distinguishable results rather than one undifferentiated error.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Outcome describes how a run ended.
type Outcome string

const (
	Succeeded Outcome = "succeeded"
	Failed    Outcome = "failed"
	TimedOut  Outcome = "timed-out"
	NotStated Outcome = "did-not-start"
)

// Result is everything observed about one run.
type Result struct {
	Outcome  Outcome
	ExitCode int
	Stdout   string
	Stderr   string
	Elapsed  time.Duration
}

// Run starts name with args in dir and waits for it, killing it when the
// timeout elapses. A non-nil error is returned only for conditions in which
// no meaningful Result exists; every other ending is reported in the Result.
func Run(dir string, timeout time.Duration, name string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// When the context expires only the started process is killed. A
	// grandchild may still hold the output pipes open, so Wait is given a
	// bounded grace period instead of waiting for the pipes indefinitely.
	cmd.WaitDelay = 500 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	res := Result{
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
		Elapsed: time.Since(start),
	}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.Outcome = Succeeded
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Outcome = TimedOut
		res.ExitCode = -1
	case errors.As(err, &exitErr):
		res.Outcome = Failed
		res.ExitCode = exitErr.ExitCode()
	default:
		res.Outcome = NotStated
		res.ExitCode = -1
		return res, fmt.Errorf("run %s: %w", name, err)
	}
	return res, nil
}
