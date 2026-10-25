// Package runner gives a world a single owner. One goroutine steps the
// simulation at a steady rate, applies commands sent to it, and publishes an
// immutable frame after every change. Everything else reads frames and sends
// commands, so the world itself is never shared between goroutines.
package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"trailsim/internal/world"
)

// ErrStopped is returned for commands sent after the runner has stopped.
var ErrStopped = errors.New("runner stopped")

// Limits on the speed control.
const (
	MinSpeed = 1
	MaxSpeed = 40
)

type command struct {
	fn    func(*state) error
	reply chan error
}

type state struct {
	w      *world.World
	paused bool
	speed  int // simulation steps per published frame
}

// Runner owns a world and publishes frames of it.
type Runner struct {
	cmds    chan command
	done    chan struct{}
	frame   atomic.Pointer[world.Frame]
	version atomic.Uint64
	period  time.Duration
	initial *world.World
}

// New creates a runner around w. The period is the time between frames.
func New(w *world.World, period time.Duration) *Runner {
	r := &Runner{
		cmds:    make(chan command),
		done:    make(chan struct{}),
		period:  period,
		initial: w,
	}
	return r
}

// Frame returns the most recent frame. It is never nil once Run has started.
func (r *Runner) Frame() *world.Frame { return r.frame.Load() }

// Run drives the simulation until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	defer close(r.done)
	st := &state{w: r.initial, speed: 2}
	r.publish(st)
	t := time.NewTicker(r.period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-r.cmds:
			err := c.fn(st)
			r.publish(st)
			c.reply <- err
		case <-t.C:
			if st.paused {
				continue
			}
			for i := 0; i < st.speed; i++ {
				st.w.Step()
			}
			r.publish(st)
		}
	}
}

func (r *Runner) publish(st *state) {
	f := st.w.Snapshot()
	f.Paused, f.Speed = st.paused, st.speed
	f.Version = r.version.Add(1)
	r.frame.Store(f)
}

func (r *Runner) do(fn func(*state) error) error {
	c := command{fn: fn, reply: make(chan error, 1)}
	select {
	case r.cmds <- c:
		return <-c.reply
	case <-r.done:
		return ErrStopped
	}
}

// SetPaused pauses or resumes stepping.
func (r *Runner) SetPaused(p bool) error {
	return r.do(func(s *state) error { s.paused = p; return nil })
}

// SetSpeed sets how many steps run per frame, clamped to the allowed range.
func (r *Runner) SetSpeed(n int) error {
	if n < MinSpeed {
		n = MinSpeed
	}
	if n > MaxSpeed {
		n = MaxSpeed
	}
	return r.do(func(s *state) error { s.speed = n; return nil })
}

// Paint applies a brush stroke to the world.
func (r *Runner) Paint(kind world.PaintKind, x, y, radius int) error {
	return r.do(func(s *state) error { return s.w.Paint(kind, x, y, radius) })
}

// Reset replaces the world with a new one built from cfg. If cfg is invalid
// the old world is kept.
func (r *Runner) Reset(cfg world.Config) error {
	return r.do(func(s *state) error {
		w, err := world.New(cfg)
		if err != nil {
			return err
		}
		s.w = w
		return nil
	})
}

// Config returns the configuration of the world as of the last frame.
func (r *Runner) Config() world.Config {
	var cfg world.Config
	if err := r.do(func(s *state) error { cfg = s.w.Config(); return nil }); err != nil {
		return world.Default()
	}
	return cfg
}
