package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"trailsim/internal/world"
)

func start(t *testing.T) (*Runner, context.CancelFunc) {
	t.Helper()
	c := world.Default()
	c.W, c.H, c.Ants = 48, 32, 40
	w, err := world.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := New(w, 2*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	waitFor(t, func() bool { return r.Frame() != nil })
	t.Cleanup(cancel)
	return r, cancel
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

func TestAdvances(t *testing.T) {
	r, _ := start(t)
	waitFor(t, func() bool { return r.Frame().Stats.Tick > 10 })
}

func TestPauseStopsTicks(t *testing.T) {
	r, _ := start(t)
	if err := r.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	a := r.Frame().Stats.Tick
	time.Sleep(30 * time.Millisecond)
	if b := r.Frame().Stats.Tick; b != a {
		t.Fatalf("ticks advanced while paused: %d to %d", a, b)
	}
	if !r.Frame().Paused {
		t.Fatal("frame does not report the pause")
	}
	r.SetPaused(false)
	waitFor(t, func() bool { return r.Frame().Stats.Tick > a })
}

func TestSpeedIsClamped(t *testing.T) {
	r, _ := start(t)
	r.SetSpeed(9999)
	if got := r.Frame().Speed; got != MaxSpeed {
		t.Fatalf("speed %d, want %d", got, MaxSpeed)
	}
	r.SetSpeed(-4)
	if got := r.Frame().Speed; got != MinSpeed {
		t.Fatalf("speed %d, want %d", got, MinSpeed)
	}
}

func TestPaintAppearsInNextFrame(t *testing.T) {
	r, _ := start(t)
	r.SetPaused(true)
	if err := r.Paint(world.PaintWall, 20, 5, 1); err != nil {
		t.Fatal(err)
	}
	f := r.Frame()
	if f.Walls[5*f.W+20] != world.Wall {
		t.Fatal("wall missing from the frame published after the command")
	}
	if err := r.Paint(world.PaintWall, 20, 5, 99); err == nil {
		t.Fatal("expected an error for a bad radius")
	}
}

func TestResetKeepsOldWorldOnError(t *testing.T) {
	r, _ := start(t)
	r.SetPaused(true)
	bad := r.Config()
	bad.Ants = -5
	if err := r.Reset(bad); err == nil {
		t.Fatal("expected an error")
	}
	if r.Frame().Stats.Ants != 40 {
		t.Fatal("failed reset replaced the world")
	}
	good := r.Config()
	good.Ants = 10
	if err := r.Reset(good); err != nil {
		t.Fatal(err)
	}
	if r.Frame().Stats.Ants != 10 || r.Frame().Stats.Tick != 0 {
		t.Fatalf("reset did not produce a fresh world: %+v", r.Frame().Stats)
	}
}

func TestVersionsIncrease(t *testing.T) {
	r, _ := start(t)
	a := r.Frame().Version
	waitFor(t, func() bool { return r.Frame().Version > a })
}

func TestCommandsAfterStop(t *testing.T) {
	r, cancel := start(t)
	cancel()
	waitFor(t, func() bool { return errors.Is(r.SetPaused(true), ErrStopped) })
}
