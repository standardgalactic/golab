// Package transition models a program as an explicit state transition
// system with an admissibility condition, as developed in Chapter 1.
package transition

import (
	"errors"
	"fmt"
)

// Max is the largest counter value the system admits.
const Max = 1000

// ErrNegative reports a proposed transition that would leave the
// counter below zero.
var ErrNegative = errors.New("negative state prohibited")

// ErrOverflow reports a proposed transition that would exceed Max.
var ErrOverflow = errors.New("state exceeds maximum")

// State is the whole state of the counter system.
type State struct {
	Counter int
}

// Increment returns a new state. The argument is copied on entry, so the
// caller's value is never modified.
func Increment(s State) State {
	s.Counter++
	return s
}

// Decrement proposes a transition and reports whether it is admissible.
func Decrement(s State) (State, error) {
	if s.Counter == 0 {
		return s, ErrNegative
	}
	s.Counter--
	return s, nil
}

// Add proposes adding delta to the counter. The admissibility test is
// written so that it cannot itself overflow: it compares delta against the
// remaining headroom instead of computing Counter+delta first. The lower
// bound is tested as delta < -Counter rather than -delta > Counter, because
// Counter is never negative, so -Counter is always representable, whereas
// -delta is not when delta is the most negative integer.
func Add(s State, delta int) (State, error) {
	switch {
	case delta >= 0 && delta > Max-s.Counter:
		return s, fmt.Errorf("add %d to %d: %w", delta, s.Counter, ErrOverflow)
	case delta < 0 && delta < -s.Counter:
		return s, fmt.Errorf("add %d to %d: %w", delta, s.Counter, ErrNegative)
	}
	s.Counter += delta
	return s, nil
}
