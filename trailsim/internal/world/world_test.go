package world

import (
	"math"
	"testing"
)

func small() Config {
	c := Default()
	c.W, c.H, c.Ants = 64, 40, 80
	return c
}

func mustNew(t *testing.T, c Config) *World {
	t.Helper()
	w, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func steps(w *World, n int) {
	for i := 0; i < n; i++ {
		w.Step()
	}
}

func TestValidate(t *testing.T) {
	bad := []func(*Config){
		func(c *Config) { c.W = 5 },
		func(c *Config) { c.H = 1000 },
		func(c *Config) { c.Ants = -1 },
		func(c *Config) { c.Ants = 50000 },
		func(c *Config) { c.Evaporation = 2 },
		func(c *Config) { c.Diffusion = -0.1 },
		func(c *Config) { c.Cap = 0 },
		func(c *Config) { c.Sense = 0 },
	}
	for i, mut := range bad {
		c := small()
		mut(&c)
		if _, err := New(c); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
	if _, err := New(small()); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministic(t *testing.T) {
	a, b := mustNew(t, small()), mustNew(t, small())
	steps(a, 300)
	steps(b, 300)
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("same configuration and seed produced different states")
	}
	c := small()
	c.Seed = 2
	d := mustNew(t, c)
	steps(d, 300)
	if a.Fingerprint() == d.Fingerprint() {
		t.Fatal("different seeds produced the same state")
	}
}

func TestSnapshotDoesNotAffectRun(t *testing.T) {
	a, b := mustNew(t, small()), mustNew(t, small())
	for i := 0; i < 200; i++ {
		a.Step()
		b.Step()
		_ = a.Snapshot()
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("taking snapshots changed the run")
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	w := mustNew(t, small())
	f := w.Snapshot()
	before := append([]float32(nil), f.Ants...)
	steps(w, 50)
	for i := range before {
		if f.Ants[i] != before[i] {
			t.Fatal("snapshot changed after the world advanced")
		}
	}
	if len(f.Ants) != 3*80 || len(f.Walls) != 64*40 {
		t.Fatalf("unexpected frame sizes: %d ants values, %d wall bytes", len(f.Ants), len(f.Walls))
	}
}

func TestFoodConserved(t *testing.T) {
	w := mustNew(t, small())
	for round := 0; round < 10; round++ {
		steps(w, 150)
		if s := w.Stats(); !s.Balanced() {
			t.Fatalf("round %d: unbalanced %+v", round, s)
		}
		switch round % 3 {
		case 0:
			w.Paint(PaintFood, 40, 20, 3)
		case 1:
			w.Paint(PaintWall, 50, 10, 4)
		default:
			w.Paint(PaintErase, 50, 30, 5)
		}
		if s := w.Stats(); !s.Balanced() {
			t.Fatalf("round %d after paint: unbalanced %+v", round, s)
		}
	}
}

func TestInvariants(t *testing.T) {
	c := small()
	w := mustNew(t, c)
	w.Paint(PaintWall, 20, 20, 5)
	for i := 0; i < 400; i++ {
		w.Step()
		for _, a := range w.ants {
			x, y := int(math.Floor(a.X)), int(math.Floor(a.Y))
			if !w.inside(x, y) {
				t.Fatalf("tick %d: ant outside the grid at (%v,%v)", i, a.X, a.Y)
			}
			if w.grid[w.idx(x, y)] == Wall {
				t.Fatalf("tick %d: ant inside a wall at (%d,%d)", i, x, y)
			}
		}
		for j := range w.home {
			for _, v := range []float32{w.home[j], w.trail[j]} {
				if v < 0 || v > c.Cap || v != v {
					t.Fatalf("tick %d: pheromone %v outside [0,%v]", i, v, c.Cap)
				}
			}
			if w.grid[j] == Wall && (w.home[j] != 0 || w.trail[j] != 0) {
				t.Fatalf("tick %d: pheromone inside a wall", i)
			}
		}
	}
}

func TestPaintErrors(t *testing.T) {
	w := mustNew(t, small())
	if err := w.Paint(PaintWall, -1, 0, 2); err == nil {
		t.Error("expected an error for a point outside the grid")
	}
	if err := w.Paint(PaintWall, 5, 5, 0); err == nil {
		t.Error("expected an error for radius 0")
	}
	if err := w.Paint(PaintWall, 5, 5, 13); err == nil {
		t.Error("expected an error for radius 13")
	}
	if err := w.Paint("lava", 5, 5, 2); err == nil {
		t.Error("expected an error for an unknown brush")
	}
}

func TestPaintWallRelocatesAnts(t *testing.T) {
	w := mustNew(t, small())
	w.ants[0].X, w.ants[0].Y = 30.5, 20.5
	if err := w.Paint(PaintWall, 30, 20, 3); err != nil {
		t.Fatal(err)
	}
	if w.ants[0].X != w.nestX || w.ants[0].Y != w.nestY {
		t.Fatal("ant caught in a new wall was not moved to the nest")
	}
}

func TestNestIsProtected(t *testing.T) {
	w := mustNew(t, small())
	w.Paint(PaintWall, int(w.nestX), int(w.nestY), 6)
	if w.grid[w.idx(int(w.nestX), int(w.nestY))] == Wall {
		t.Fatal("a wall was painted over the nest")
	}
}

func TestEraseRemovesWall(t *testing.T) {
	w := mustNew(t, small())
	w.Paint(PaintWall, 40, 25, 2)
	i := w.idx(40, 25)
	if w.grid[i] != Wall {
		t.Fatal("wall not painted")
	}
	w.Paint(PaintErase, 40, 25, 2)
	if w.grid[i] != Open {
		t.Fatal("wall not erased")
	}
}

func TestDiffusionConservesAwayFromEvaporation(t *testing.T) {
	c := small()
	c.Evaporation = 0
	w := mustNew(t, c)
	w.home[w.idx(30, 20)] = 5
	sum := func() float64 {
		var s float64
		for _, v := range w.home {
			s += float64(v)
		}
		return s
	}
	before := sum()
	w.diffuseAndEvaporate(w.home)
	if after := sum(); math.Abs(after-before) > 1e-4 {
		t.Fatalf("diffusion without evaporation changed the total from %v to %v", before, after)
	}
}

func TestEvaporationDecays(t *testing.T) {
	c := small()
	c.Diffusion = 0
	w := mustNew(t, c)
	w.trail[w.idx(30, 20)] = 4
	for i := 0; i < 100; i++ {
		w.diffuseAndEvaporate(w.trail)
	}
	want := 4 * math.Pow(1-float64(c.Evaporation), 100)
	if got := float64(w.trail[w.idx(30, 20)]); math.Abs(got-want) > 0.01 {
		t.Fatalf("after 100 ticks got %v, want %v", got, want)
	}
}

// The central claim of the model: trails make foraging faster. The margin
// asserted is far below what the sweep shows, so the test is not brittle.
func TestTrailsBeatControl(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("long-running and single-goroutine, so the race detector adds nothing")
	}
	var with, without int
	for seed := int64(1); seed <= 2; seed++ {
		for _, trails := range []bool{true, false} {
			c := Default()
			c.Ants, c.Seed, c.Trails = 150, seed, trails
			w := mustNew(t, c)
			steps(w, 800)
			if trails {
				with += w.Stats().Collected
			} else {
				without += w.Stats().Collected
			}
		}
	}
	if with < 3*without {
		t.Fatalf("trails delivered %d against %d without; expected at least three times as many", with, without)
	}
}

func TestControlLaysNoPheromone(t *testing.T) {
	c := small()
	c.Trails = false
	w := mustNew(t, c)
	steps(w, 200)
	for i := range w.home {
		if w.home[i] != 0 || w.trail[i] != 0 {
			t.Fatal("control run laid pheromone")
		}
	}
}

func BenchmarkStep(b *testing.B) {
	w, _ := New(Default())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Step()
	}
}
