// Package sim is a small 2D molecular dynamics engine: Lennard-Jones
// particles in a box, integrated with velocity Verlet and a cell list.
//
// Units are reduced: sigma = 1, mass = 1, epsilon scales the well depth,
// Boltzmann constant = 1. The y axis points down so that world coordinates
// match canvas coordinates in the browser.
package sim

import (
	"math"
	"math/rand"
)

const (
	// Cutoff is the interaction range in units of sigma.
	Cutoff = 2.5
	// MaxN is the largest particle count a world accepts.
	MaxN = 4000

	minR2      = 0.55 // pair distances below this are clamped to cap the force
	wallMargin = 0.5  // particle centres stay this far from the walls
	maxSpeed   = 40.0 // hard speed cap, a safety net for pointer forces
	tau        = 0.2  // thermostat coupling time
	latticeA   = 1.12 // hexagonal lattice spacing, near the LJ minimum
	rowStep    = latticeA * 0.8660254037844386
)

// vShift makes the pair potential zero at the cutoff.
var vShift = 4 * (math.Pow(1/Cutoff, 12) - math.Pow(1/Cutoff, 6))

// Presets lists the starting conditions Load understands.
var Presets = []string{"drop", "crystal", "collision", "gas"}

// ValidPreset reports whether name is a known preset.
func ValidPreset(name string) bool {
	for _, p := range Presets {
		if p == name {
			return true
		}
	}
	return false
}

// Params are the live, user adjustable settings.
type Params struct {
	Gravity         float64 `json:"gravity"`
	Epsilon         float64 `json:"epsilon"`
	Dt              float64 `json:"dt"`
	Damping         float64 `json:"damping"`
	Thermostat      bool    `json:"thermostat"`
	Temperature     float64 `json:"temperature"`
	PointerStrength float64 `json:"pointerStrength"`
	PointerRadius   float64 `json:"pointerRadius"`
	Substeps        int     `json:"substeps"`
	Paused          bool    `json:"paused"`
}

// DefaultParams returns the settings a fresh world starts with.
func DefaultParams() Params {
	return Params{
		Gravity:         0.4,
		Epsilon:         1,
		Dt:              0.004,
		Damping:         0,
		Thermostat:      true,
		Temperature:     0.4,
		PointerStrength: 60,
		PointerRadius:   10,
		Substeps:        6,
	}
}

func clampF(v, lo, hi float64) float64 {
	if math.IsNaN(v) || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Clamp forces every field into a range the integrator tolerates.
func (p *Params) Clamp() {
	p.Gravity = clampF(p.Gravity, -3, 3)
	p.Epsilon = clampF(p.Epsilon, 0.05, 5)
	p.Dt = clampF(p.Dt, 0.0005, 0.008)
	p.Damping = clampF(p.Damping, 0, 5)
	p.Temperature = clampF(p.Temperature, 0, 10)
	p.PointerStrength = clampF(p.PointerStrength, 0, 500)
	p.PointerRadius = clampF(p.PointerRadius, 1, 40)
	p.Substeps = min(max(p.Substeps, 1), 40)
}

// Pointer is an optional force source controlled by the mouse.
// Mode 1 attracts, -1 repels, 0 is off.
type Pointer struct {
	X, Y float64
	Mode int
}

// World holds the full simulation state.
type World struct {
	W, H   float64
	N      int
	X, Y   []float64
	VX, VY []float64
	AX, AY []float64
	P      Params
	Ptr    Pointer
	Preset string

	Time  float64
	Steps uint64
	KE    float64 // total kinetic energy
	PE    float64 // pair plus gravitational potential energy
	Temp  float64 // kinetic energy per particle with bulk motion removed

	rng    *rand.Rand
	nx, ny int
	cw, ch float64
	head   []int32
	next   []int32
}

// New creates an empty world of the given size.
func New(width, height float64, seed int64) *World {
	w := &World{
		W:   width,
		H:   height,
		P:   DefaultParams(),
		rng: rand.New(rand.NewSource(seed)),
	}
	w.nx = max(1, int(width/Cutoff))
	w.ny = max(1, int(height/Cutoff))
	w.cw = width / float64(w.nx)
	w.ch = height / float64(w.ny)
	w.head = make([]int32, w.nx*w.ny)
	return w
}

// Load replaces the particles with a preset arrangement and resets time.
// It also sets the preset's recommended gravity and thermostat settings.
func (w *World) Load(preset string, n int) {
	n = min(max(n, 2), MaxN)
	w.N = n
	w.Preset = preset
	w.X, w.Y = make([]float64, n), make([]float64, n)
	w.VX, w.VY = make([]float64, n), make([]float64, n)
	w.AX, w.AY = make([]float64, n), make([]float64, n)
	w.next = make([]int32, n)
	w.Ptr = Pointer{}
	w.Time, w.Steps = 0, 0

	switch preset {
	case "gas":
		w.fillGas(1.0)
		w.P.Gravity, w.P.Thermostat, w.P.Temperature = 0, false, 1
	case "crystal":
		cols, rows := blobDims(n, 2.0)
		cy := w.H - wallMargin - float64(rows)*rowStep/2 - 0.6
		w.fillBlob(0, n, cols, rows, w.W/2, cy, 0, 0)
		w.P.Gravity, w.P.Thermostat, w.P.Temperature = 0.4, true, 0.2
	case "collision":
		half := n / 2
		cols, rows := blobDims(half, 1.0)
		w.fillBlob(0, half, cols, rows, w.W*0.25, w.H/2, 4, 0)
		cols, rows = blobDims(n-half, 1.0)
		w.fillBlob(half, n-half, cols, rows, w.W*0.75, w.H/2, -4, 0)
		w.P.Gravity, w.P.Thermostat, w.P.Temperature = 0, false, 0.4
	default: // "drop"
		w.Preset = "drop"
		cols, rows := blobDims(n, 1.3)
		w.fillBlob(0, n, cols, rows, w.W/2, w.H*0.4, 0, 0)
		w.P.Gravity, w.P.Thermostat, w.P.Temperature = 0.4, true, 0.4
	}

	w.computeForces()
	w.measure()
}

// blobDims returns the lattice columns and rows for a blob of count
// particles whose physical width/height ratio is about aspect.
func blobDims(count int, aspect float64) (cols, rows int) {
	cols = int(math.Ceil(math.Sqrt(float64(count) * aspect * 0.8660254)))
	cols = max(cols, 1)
	rows = (count + cols - 1) / cols
	return
}

// fillBlob places count particles on a hexagonal lattice centred at (cx, cy)
// with a bulk velocity plus a little thermal noise.
func (w *World) fillBlob(start, count, cols, rows int, cx, cy, vx, vy float64) {
	for k := 0; k < count; k++ {
		r, c := k/cols, k%cols
		x := cx + (float64(c)-float64(cols-1)/2)*latticeA + (float64(r%2)-0.5)*latticeA/2
		y := cy + (float64(r)-float64(rows-1)/2)*rowStep
		i := start + k
		w.X[i] = clampF(x, wallMargin, w.W-wallMargin)
		w.Y[i] = clampF(y, wallMargin, w.H-wallMargin)
		w.VX[i] = vx + 0.2*w.rng.NormFloat64()
		w.VY[i] = vy + 0.2*w.rng.NormFloat64()
	}
}

// fillGas scatters particles on a jittered grid with Gaussian velocities.
func (w *World) fillGas(sigmaV float64) {
	n := w.N
	cols := int(math.Ceil(math.Sqrt(float64(n) * w.W / w.H)))
	rows := (n + cols - 1) / cols
	sx := (w.W - 2*wallMargin) / float64(cols)
	sy := (w.H - 2*wallMargin) / float64(rows)
	for k := 0; k < n; k++ {
		c, r := k%cols, k/cols
		w.X[k] = wallMargin + (float64(c)+0.5)*sx + (w.rng.Float64()-0.5)*0.4*sx
		w.Y[k] = wallMargin + (float64(r)+0.5)*sy + (w.rng.Float64()-0.5)*0.4*sy
		w.VX[k] = sigmaV * w.rng.NormFloat64()
		w.VY[k] = sigmaV * w.rng.NormFloat64()
	}
}

// pairForce returns the force on particle i (and minus the force on j) for a
// separation vector d = ri - rj, plus the pair potential energy.
func pairForce(dx, dy, eps float64) (fx, fy, pe float64) {
	r2 := dx*dx + dy*dy
	if r2 >= Cutoff*Cutoff {
		return 0, 0, 0
	}
	if r2 < 1e-12 {
		dx, r2 = 1e-6, 1e-12
	}
	if r2 < minR2 {
		r2 = minR2
	}
	inv2 := 1 / r2
	inv6 := inv2 * inv2 * inv2
	fOverR := 24 * eps * inv2 * inv6 * (2*inv6 - 1)
	pe = eps * (4*inv6*(inv6-1) - vShift)
	return fOverR * dx, fOverR * dy, pe
}

// neighbour cells visited from each cell so that every pair is seen once.
var stencil = [4][2]int{{1, 0}, {-1, 1}, {0, 1}, {1, 1}}

func (w *World) cellOf(i int) int {
	cx := min(max(int(w.X[i]/w.cw), 0), w.nx-1)
	cy := min(max(int(w.Y[i]/w.ch), 0), w.ny-1)
	return cy*w.nx + cx
}

func (w *World) addPair(i, j int32, eps float64) float64 {
	fx, fy, pe := pairForce(w.X[i]-w.X[j], w.Y[i]-w.Y[j], eps)
	w.AX[i] += fx
	w.AY[i] += fy
	w.AX[j] -= fx
	w.AY[j] -= fy
	return pe
}

// computeForces fills AX, AY and PE from the current positions.
func (w *World) computeForces() {
	n := w.N
	g := w.P.Gravity
	for i := 0; i < n; i++ {
		w.AX[i], w.AY[i] = 0, g
	}
	for c := range w.head {
		w.head[c] = -1
	}
	for i := 0; i < n; i++ {
		c := w.cellOf(i)
		w.next[i] = w.head[c]
		w.head[c] = int32(i)
	}

	eps := w.P.Epsilon
	var pe float64
	for cy := 0; cy < w.ny; cy++ {
		for cx := 0; cx < w.nx; cx++ {
			for i := w.head[cy*w.nx+cx]; i >= 0; i = w.next[i] {
				for j := w.next[i]; j >= 0; j = w.next[j] {
					pe += w.addPair(i, j, eps)
				}
				for _, d := range stencil {
					ncx, ncy := cx+d[0], cy+d[1]
					if ncx < 0 || ncx >= w.nx || ncy >= w.ny {
						continue
					}
					for j := w.head[ncy*w.nx+ncx]; j >= 0; j = w.next[j] {
						pe += w.addPair(i, j, eps)
					}
				}
			}
		}
	}

	for i := 0; i < n; i++ {
		pe += g * (w.H - w.Y[i])
	}
	w.PE = pe

	if w.Ptr.Mode != 0 && w.P.PointerStrength > 0 {
		radius := w.P.PointerRadius
		r2max := radius * radius
		s := float64(w.Ptr.Mode) * w.P.PointerStrength
		for i := 0; i < n; i++ {
			dx, dy := w.Ptr.X-w.X[i], w.Ptr.Y-w.Y[i]
			r2 := dx*dx + dy*dy
			if r2 >= r2max || r2 < 1e-12 {
				continue
			}
			r := math.Sqrt(r2)
			f := s * (1 - r/radius) / r
			w.AX[i] += f * dx
			w.AY[i] += f * dy
		}
	}
}

// measure recomputes KE and Temp and returns the centre of mass velocity.
func (w *World) measure() (cvx, cvy float64) {
	n := float64(w.N)
	var sx, sy, k float64
	for i := 0; i < w.N; i++ {
		sx += w.VX[i]
		sy += w.VY[i]
		k += w.VX[i]*w.VX[i] + w.VY[i]*w.VY[i]
	}
	cvx, cvy = sx/n, sy/n
	w.KE = 0.5 * k
	pec := w.KE - 0.5*n*(cvx*cvx+cvy*cvy)
	w.Temp = math.Max(pec, 0) / n
	return
}

// Step advances the world by one velocity Verlet step.
func (w *World) Step() {
	dt := w.P.Dt
	half := 0.5 * dt
	lo, hiX, hiY := wallMargin, w.W-wallMargin, w.H-wallMargin

	for i := 0; i < w.N; i++ {
		w.VX[i] += half * w.AX[i]
		w.VY[i] += half * w.AY[i]
		x := w.X[i] + dt*w.VX[i]
		y := w.Y[i] + dt*w.VY[i]
		if x < lo {
			x, w.VX[i] = 2*lo-x, -w.VX[i]
		} else if x > hiX {
			x, w.VX[i] = 2*hiX-x, -w.VX[i]
		}
		if y < lo {
			y, w.VY[i] = 2*lo-y, -w.VY[i]
		} else if y > hiY {
			y, w.VY[i] = 2*hiY-y, -w.VY[i]
		}
		w.X[i] = clampF(x, lo, hiX)
		w.Y[i] = clampF(y, lo, hiY)
	}

	w.computeForces()

	damp := 1 - w.P.Damping*dt
	for i := 0; i < w.N; i++ {
		vx := (w.VX[i] + half*w.AX[i]) * damp
		vy := (w.VY[i] + half*w.AY[i]) * damp
		if s2 := vx*vx + vy*vy; s2 > maxSpeed*maxSpeed {
			f := maxSpeed / math.Sqrt(s2)
			vx, vy = vx*f, vy*f
		}
		w.VX[i], w.VY[i] = vx, vy
	}

	cvx, cvy := w.measure()
	if w.P.Thermostat && w.Temp > 1e-12 {
		// Berendsen scaling of the velocity relative to the centre of mass,
		// so a falling blob is not mistaken for a hot one.
		lam := math.Sqrt(1 + dt/tau*(w.P.Temperature/w.Temp-1))
		lam = clampF(lam, 0.95, 1.05)
		for i := 0; i < w.N; i++ {
			w.VX[i] = cvx + (w.VX[i]-cvx)*lam
			w.VY[i] = cvy + (w.VY[i]-cvy)*lam
		}
		w.measure()
	}

	w.Time += dt
	w.Steps++
}
