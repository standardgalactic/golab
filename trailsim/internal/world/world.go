// Package world is the simulation itself: a grid with walls, food and a nest,
// ants that wander, carry food home, and lay pheromone, and two pheromone
// fields that evaporate and diffuse. A World is not safe for concurrent use;
// the runner package gives it a single owner. Given the same configuration
// and seed, a World produces exactly the same sequence of states.
package world

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
)

// Config holds everything that shapes a run.
type Config struct {
	W, H         int
	Ants         int
	Seed         int64
	Evaporation  float32 // fraction of pheromone lost per tick
	Diffusion    float32 // fraction of each cell shared with its neighbours per tick
	Deposit      float32 // pheromone an ant lays on its first step after pickup or delivery
	DepositDecay float32 // factor applied to an ant's deposit strength each tick
	Cap          float32 // largest amount of pheromone a cell can hold
	Sense        float64 // how far ahead an ant samples the trail, in cells
	Jitter       float64 // largest random turn per tick, in radians
	TurnRate     float64 // turn toward a stronger trail, in radians
	Trails       bool    // when false ants neither lay nor follow pheromone (the control run)
}

// Default returns the configuration used by the browser view.
func Default() Config {
	return Config{
		W: 160, H: 100, Ants: 300, Seed: 1,
		Evaporation: 0.01, Diffusion: 0.10,
		Deposit: 1, DepositDecay: 0.995, Cap: 8,
		Sense: 4, Jitter: 0.35, TurnRate: 0.5,
		Trails: true,
	}
}

// Validate reports a configuration the simulation cannot run.
func (c Config) Validate() error {
	switch {
	case c.W < 16 || c.W > 400 || c.H < 16 || c.H > 400:
		return fmt.Errorf("grid %dx%d outside 16..400", c.W, c.H)
	case c.Ants < 0 || c.Ants > 20000:
		return fmt.Errorf("ants %d outside 0..20000", c.Ants)
	case c.Evaporation < 0 || c.Evaporation > 1, c.Diffusion < 0 || c.Diffusion > 1:
		return fmt.Errorf("evaporation and diffusion must lie in [0,1]")
	case c.Cap <= 0, c.Deposit < 0, c.DepositDecay < 0 || c.DepositDecay > 1:
		return fmt.Errorf("cap must be positive, deposit non-negative, deposit decay in [0,1]")
	case c.Sense <= 0 || c.Jitter < 0 || c.TurnRate < 0:
		return fmt.Errorf("sense must be positive; jitter and turn rate non-negative")
	}
	return nil
}

// Ant is one agent.
type Ant struct {
	X, Y     float64
	Dir      float64
	Carry    bool
	Strength float32
}

// Cell kinds in the wall grid.
const (
	Open uint8 = 0
	Wall uint8 = 1
)

const (
	nestRadius = 4.0
	foodSense  = 4
	nestSense  = 12.0
)

// World is the simulation state.
type World struct {
	cfg  Config
	rng  *rand.Rand
	tick int

	grid  []uint8   // Open or Wall
	food  []int32   // units of food per cell
	home  []float32 // pheromone laid by ants searching for food
	trail []float32 // pheromone laid by ants carrying food
	tmp   []float32

	nestX, nestY float64
	ants         []Ant

	collected int
	initial   int // food present at layout
	injected  int // food added by painting
	destroyed int // food removed by painting walls or erasing
}

// New builds a world with the standard layout.
func New(cfg Config) (*World, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	w := &World{cfg: cfg, rng: rand.New(rand.NewSource(cfg.Seed))}
	n := cfg.W * cfg.H
	w.grid = make([]uint8, n)
	w.food = make([]int32, n)
	w.home = make([]float32, n)
	w.trail = make([]float32, n)
	w.tmp = make([]float32, n)
	w.layout()
	w.ants = make([]Ant, cfg.Ants)
	for i := range w.ants {
		w.ants[i] = Ant{X: w.nestX, Y: w.nestY, Dir: w.rng.Float64() * 2 * math.Pi, Strength: cfg.Deposit}
	}
	return w, nil
}

// layout places the nest, a wall with a gap, and two food sources.
func (w *World) layout() {
	W, H := w.cfg.W, w.cfg.H
	w.nestX, w.nestY = float64(W)/5, float64(H)/2
	// A vertical barrier in the middle of the map with a gap, so that the
	// straight line from the nest to the food is blocked.
	bx := W / 2
	gapLo, gapHi := H*55/100, H*70/100
	for y := H * 10 / 100; y < H*90/100; y++ {
		if y >= gapLo && y < gapHi {
			continue
		}
		for dx := 0; dx < 2; dx++ {
			w.grid[y*W+bx+dx] = Wall
		}
	}
	w.addFood(W*80/100, H*25/100, 5, 40)
	w.addFood(W*75/100, H*80/100, 5, 40)
	w.initial = w.totalFood()
}

func (w *World) idx(x, y int) int { return y*w.cfg.W + x }

func (w *World) inside(x, y int) bool { return x >= 0 && y >= 0 && x < w.cfg.W && y < w.cfg.H }

func (w *World) nearNest(x, y int) bool {
	dx, dy := float64(x)-w.nestX, float64(y)-w.nestY
	return dx*dx+dy*dy <= (nestRadius+1)*(nestRadius+1)
}

// addFood puts amount units on each open cell within radius r of (cx,cy).
func (w *World) addFood(cx, cy, r int, amount int32) int {
	added := 0
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if !w.inside(x, y) || (x-cx)*(x-cx)+(y-cy)*(y-cy) > r*r {
				continue
			}
			i := w.idx(x, y)
			if w.grid[i] == Wall || w.nearNest(x, y) {
				continue
			}
			w.food[i] += amount
			added += int(amount)
		}
	}
	return added
}

func (w *World) totalFood() int {
	t := 0
	for _, f := range w.food {
		t += int(f)
	}
	return t
}

// Config returns the configuration the world was built with.
func (w *World) Config() Config { return w.cfg }

// Tick is the number of steps taken.
func (w *World) Tick() int { return w.tick }

// sample returns the field value at a point, or -1 for walls and outside.
func (w *World) sample(f []float32, x, y float64) float32 {
	ix, iy := int(math.Floor(x)), int(math.Floor(y))
	if !w.inside(ix, iy) || w.grid[w.idx(ix, iy)] == Wall {
		return -1
	}
	return f[w.idx(ix, iy)]
}

// Step advances the simulation by one tick.
func (w *World) Step() {
	for i := range w.ants {
		w.stepAnt(&w.ants[i])
	}
	if w.cfg.Trails {
		w.diffuseAndEvaporate(w.home)
		w.diffuseAndEvaporate(w.trail)
	}
	w.tick++
}

func (w *World) stepAnt(a *Ant) {
	cfg := w.cfg

	// Choose a heading.
	switch {
	case !a.Carry && w.steerToFood(a):
	case a.Carry && w.steerToNest(a):
	case cfg.Trails:
		field := w.trail
		if a.Carry {
			field = w.home
		}
		const spread = math.Pi / 4
		l := w.sample(field, a.X+math.Cos(a.Dir-spread)*cfg.Sense, a.Y+math.Sin(a.Dir-spread)*cfg.Sense)
		c := w.sample(field, a.X+math.Cos(a.Dir)*cfg.Sense, a.Y+math.Sin(a.Dir)*cfg.Sense)
		r := w.sample(field, a.X+math.Cos(a.Dir+spread)*cfg.Sense, a.Y+math.Sin(a.Dir+spread)*cfg.Sense)
		if best := max32(l, max32(c, r)); best > 0 {
			switch {
			case c >= l && c >= r:
			case l > r:
				a.Dir -= cfg.TurnRate
			default:
				a.Dir += cfg.TurnRate
			}
		}
	}
	a.Dir += (w.rng.Float64()*2 - 1) * cfg.Jitter

	// Move one cell, or bounce off a wall or the edge.
	nx, ny := a.X+math.Cos(a.Dir), a.Y+math.Sin(a.Dir)
	ix, iy := int(math.Floor(nx)), int(math.Floor(ny))
	if !w.inside(ix, iy) || w.grid[w.idx(ix, iy)] == Wall {
		a.Dir += math.Pi + (w.rng.Float64()*2-1)*0.5
	} else {
		a.X, a.Y = nx, ny
	}

	// Interact with the cell the ant is standing in.
	cx, cy := int(math.Floor(a.X)), int(math.Floor(a.Y))
	ci := w.idx(cx, cy)
	if !a.Carry && w.food[ci] > 0 {
		w.food[ci]--
		a.Carry, a.Strength = true, cfg.Deposit
		a.Dir += math.Pi
	} else if a.Carry && w.distNest(a.X, a.Y) <= nestRadius {
		w.collected++
		a.Carry, a.Strength = false, cfg.Deposit
		a.Dir += math.Pi
	}

	// Lay pheromone. The home field records freshness: an ant writes the
	// larger of what is there and its own strength, so the value falls with
	// time since the ant left the nest and the field forms a gradient that
	// points back to it. The food trail accumulates, so traffic reinforces it.
	if cfg.Trails {
		if a.Carry {
			if v := w.trail[ci] + a.Strength; v < cfg.Cap {
				w.trail[ci] = v
			} else {
				w.trail[ci] = cfg.Cap
			}
		} else if cfg.Deposit > 0 {
			if v := a.Strength / cfg.Deposit * cfg.Cap; v > w.home[ci] {
				w.home[ci] = v
			}
		}
		a.Strength *= cfg.DepositDecay
	}
}

func (w *World) distNest(x, y float64) float64 { return math.Hypot(x-w.nestX, y-w.nestY) }

// steerToFood points a searching ant at the nearest food within a short
// range, which stands for smell. It reports whether it found any.
func (w *World) steerToFood(a *Ant) bool {
	cx, cy := int(math.Floor(a.X)), int(math.Floor(a.Y))
	best, bx, by := math.MaxFloat64, 0, 0
	for y := cy - foodSense; y <= cy+foodSense; y++ {
		for x := cx - foodSense; x <= cx+foodSense; x++ {
			if !w.inside(x, y) || w.food[w.idx(x, y)] <= 0 {
				continue
			}
			if d := float64((x-cx)*(x-cx) + (y-cy)*(y-cy)); d < best {
				best, bx, by = d, x, y
			}
		}
	}
	if best == math.MaxFloat64 {
		return false
	}
	a.Dir = math.Atan2(float64(by)+0.5-a.Y, float64(bx)+0.5-a.X)
	return true
}

// steerToNest points a carrying ant at the nest once it is close.
func (w *World) steerToNest(a *Ant) bool {
	if w.distNest(a.X, a.Y) > nestSense {
		return false
	}
	a.Dir = math.Atan2(w.nestY-a.Y, w.nestX-a.X)
	return true
}

// diffuseAndEvaporate lets each open cell share the fraction Diffusion of its
// pheromone equally among its open neighbours, then removes the evaporated
// fraction. Walls hold nothing and pass nothing, so without evaporation the
// total amount of pheromone is exactly conserved.
func (w *World) diffuseAndEvaporate(f []float32) {
	W, H := w.cfg.W, w.cfg.H
	d, keep := w.cfg.Diffusion, 1-w.cfg.Evaporation
	for i := range w.tmp {
		w.tmp[i] = 0
	}
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			i := y*W + x
			if w.grid[i] == Wall || f[i] == 0 {
				continue
			}
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if (dx != 0 || dy != 0) && w.open(x+dx, y+dy) {
						n++
					}
				}
			}
			if n == 0 {
				w.tmp[i] += f[i]
				continue
			}
			w.tmp[i] += (1 - d) * f[i]
			share := d * f[i] / float32(n)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if (dx != 0 || dy != 0) && w.open(x+dx, y+dy) {
						w.tmp[(y+dy)*W+x+dx] += share
					}
				}
			}
		}
	}
	for i := range f {
		f[i] = keep * w.tmp[i]
		if f[i] > w.cfg.Cap {
			f[i] = w.cfg.Cap
		}
	}
}

// open reports whether (x,y) is inside the grid and not a wall.
func (w *World) open(x, y int) bool {
	return w.inside(x, y) && w.grid[y*w.cfg.W+x] != Wall
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// PaintKind says what a brush stroke does.
type PaintKind string

const (
	PaintWall  PaintKind = "wall"
	PaintFood  PaintKind = "food"
	PaintErase PaintKind = "erase"
)

// Paint applies a circular brush of radius r centred on (cx,cy). Walls and
// the erase brush destroy food in the cells they cover, and the loss is
// counted, so the food balance of Stats always holds.
func (w *World) Paint(kind PaintKind, cx, cy, r int) error {
	if !w.inside(cx, cy) {
		return fmt.Errorf("point (%d,%d) outside the grid", cx, cy)
	}
	if r < 1 || r > 12 {
		return fmt.Errorf("radius %d outside 1..12", r)
	}
	switch kind {
	case PaintFood:
		w.injected += w.addFood(cx, cy, r, 20)
		return nil
	case PaintWall, PaintErase:
	default:
		return fmt.Errorf("unknown brush %q", kind)
	}
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if !w.inside(x, y) || (x-cx)*(x-cx)+(y-cy)*(y-cy) > r*r || w.nearNest(x, y) {
				continue
			}
			i := w.idx(x, y)
			w.destroyed += int(w.food[i])
			w.food[i] = 0
			if kind == PaintWall {
				w.grid[i] = Wall
				w.home[i], w.trail[i] = 0, 0
			} else {
				w.grid[i] = Open
			}
		}
	}
	// An ant caught inside a new wall is moved to the nest.
	for i := range w.ants {
		a := &w.ants[i]
		if w.grid[w.idx(int(math.Floor(a.X)), int(math.Floor(a.Y)))] == Wall {
			a.X, a.Y = w.nestX, w.nestY
		}
	}
	return nil
}

// Stats summarizes the world.
type Stats struct {
	Tick      int `json:"tick"`
	Ants      int `json:"ants"`
	Carrying  int `json:"carrying"`
	Collected int `json:"collected"`
	Remaining int `json:"remaining"`
	Initial   int `json:"initial"`
	Injected  int `json:"injected"`
	Destroyed int `json:"destroyed"`
}

// Stats computes the current figures.
func (w *World) Stats() Stats {
	s := Stats{Tick: w.tick, Ants: len(w.ants), Collected: w.collected, Remaining: w.totalFood(),
		Initial: w.initial, Injected: w.injected, Destroyed: w.destroyed}
	for _, a := range w.ants {
		if a.Carry {
			s.Carrying++
		}
	}
	return s
}

// Balanced reports whether every unit of food is accounted for: the food at
// the start plus the food painted in equals what remains on the map, what is
// being carried, what has been delivered, and what was destroyed.
func (s Stats) Balanced() bool {
	return s.Initial+s.Injected == s.Remaining+s.Carrying+s.Collected+s.Destroyed
}

// Fingerprint is a digest of the whole state, for comparing runs.
func (w *World) Fingerprint() string {
	h := sha256.New()
	put := func(v uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		h.Write(b[:])
	}
	put(uint64(w.tick))
	put(uint64(w.collected))
	for _, a := range w.ants {
		put(math.Float64bits(a.X))
		put(math.Float64bits(a.Y))
		put(math.Float64bits(a.Dir))
		if a.Carry {
			put(1)
		} else {
			put(0)
		}
	}
	for i := range w.grid {
		put(uint64(w.grid[i]))
		put(uint64(uint32(w.food[i])))
		put(uint64(math.Float32bits(w.home[i])))
		put(uint64(math.Float32bits(w.trail[i])))
	}
	return hex.EncodeToString(h.Sum(nil))
}
