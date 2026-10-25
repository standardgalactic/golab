package world

import "math"

// Frame is an immutable picture of the world for display. Every layer is
// one byte per cell in row-major order.
type Frame struct {
	Version uint64    `json:"version"`
	W       int       `json:"w"`
	H       int       `json:"h"`
	Stats   Stats     `json:"stats"`
	Paused  bool      `json:"paused"`
	Speed   int       `json:"speed"`
	Ants    []float32 `json:"ants"`  // x, y, carrying (0 or 1) for each ant
	Walls   []byte    `json:"walls"` // 1 where there is a wall
	Food    []byte    `json:"food"`  // food units, saturating at 255
	Home    []byte    `json:"home"`  // pheromone of searching ants, scaled to the cap
	Trail   []byte    `json:"trail"` // pheromone of carrying ants, scaled to the cap
}

// Snapshot copies the displayable state. The result shares nothing with the
// world, so it may be handed to other goroutines.
func (w *World) Snapshot() *Frame {
	n := w.cfg.W * w.cfg.H
	f := &Frame{
		W: w.cfg.W, H: w.cfg.H, Stats: w.Stats(),
		Ants:  make([]float32, 0, 3*len(w.ants)),
		Walls: make([]byte, n), Food: make([]byte, n), Home: make([]byte, n), Trail: make([]byte, n),
	}
	for _, a := range w.ants {
		c := float32(0)
		if a.Carry {
			c = 1
		}
		f.Ants = append(f.Ants, round1(a.X), round1(a.Y), c)
	}
	for i := 0; i < n; i++ {
		f.Walls[i] = w.grid[i]
		if w.food[i] > 255 {
			f.Food[i] = 255
		} else {
			f.Food[i] = byte(w.food[i])
		}
		f.Home[i] = quantize(w.home[i], w.cfg.Cap)
		f.Trail[i] = quantize(w.trail[i], w.cfg.Cap)
	}
	return f
}

func round1(v float64) float32 { return float32(math.Round(v*10) / 10) }

func quantize(v, cap float32) byte {
	if v <= 0 {
		return 0
	}
	q := v / cap * 255
	if q > 255 {
		return 255
	}
	return byte(q)
}
