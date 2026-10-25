package sim

import (
	"math"
	"testing"
)

func TestCellListMatchesBruteForce(t *testing.T) {
	for _, preset := range Presets {
		w := New(60, 40, 3)
		w.Load(preset, 700)
		w.P.Gravity = 0
		w.computeForces()

		n := w.N
		bx, by := make([]float64, n), make([]float64, n)
		var bpe float64
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				fx, fy, pe := pairForce(w.X[i]-w.X[j], w.Y[i]-w.Y[j], w.P.Epsilon)
				bx[i] += fx
				by[i] += fy
				bx[j] -= fx
				by[j] -= fy
				bpe += pe
			}
		}
		for i := 0; i < n; i++ {
			if math.Abs(bx[i]-w.AX[i]) > 1e-9 || math.Abs(by[i]-w.AY[i]) > 1e-9 {
				t.Fatalf("%s: particle %d force mismatch: cell (%g,%g) brute (%g,%g)",
					preset, i, w.AX[i], w.AY[i], bx[i], by[i])
			}
		}
		if math.Abs(bpe-w.PE) > 1e-6*math.Max(1, math.Abs(bpe)) {
			t.Fatalf("%s: potential energy mismatch: cell %g brute %g", preset, w.PE, bpe)
		}
	}
}

func TestEnergyConservation(t *testing.T) {
	w := New(40, 30, 1)
	w.Load("gas", 300)
	w.P.Thermostat = false
	w.P.Gravity = 0
	w.P.Dt = 0.002
	w.computeForces()
	w.measure()
	e0 := w.KE + w.PE
	for i := 0; i < 3000; i++ {
		w.Step()
	}
	e1 := w.KE + w.PE
	if drift := math.Abs(e1-e0) / w.KE; drift > 0.02 {
		t.Fatalf("energy drift %.3f%% of kinetic energy (E0=%g E1=%g)", drift*100, e0, e1)
	}
}

func TestStaysInsideBoxWithPointer(t *testing.T) {
	for _, mode := range []int{1, -1} {
		w := New(120, 72, 5)
		w.Load("drop", 1500)
		w.Ptr = Pointer{X: 60, Y: 36, Mode: mode}
		w.P.PointerStrength = 300
		for i := 0; i < 600; i++ {
			w.Step()
		}
		for i := 0; i < w.N; i++ {
			x, y := w.X[i], w.Y[i]
			if math.IsNaN(x) || math.IsNaN(y) ||
				x < wallMargin-1e-9 || x > w.W-wallMargin+1e-9 ||
				y < wallMargin-1e-9 || y > w.H-wallMargin+1e-9 {
				t.Fatalf("mode %d: particle %d escaped or went NaN: (%g, %g)", mode, i, x, y)
			}
		}
	}
}

func TestThermostatReachesTarget(t *testing.T) {
	w := New(60, 40, 7)
	w.Load("gas", 400)
	w.P.Gravity = 0
	w.P.Thermostat = true
	w.P.Temperature = 0.5
	for i := 0; i < 4000; i++ {
		w.Step()
	}
	if math.Abs(w.Temp-0.5) > 0.05 {
		t.Fatalf("temperature %g did not settle near 0.5", w.Temp)
	}
}

func TestPresetsAreFiniteAndInBounds(t *testing.T) {
	for _, preset := range Presets {
		for _, n := range []int{2, 50, 1000, MaxN} {
			w := New(120, 72, 1)
			w.Load(preset, n)
			if w.N != n {
				t.Fatalf("%s: asked for %d particles, got %d", preset, n, w.N)
			}
			for i := 0; i < w.N; i++ {
				if math.IsNaN(w.X[i]) || w.X[i] < 0 || w.X[i] > w.W || w.Y[i] < 0 || w.Y[i] > w.H {
					t.Fatalf("%s n=%d: particle %d out of bounds at (%g, %g)", preset, n, i, w.X[i], w.Y[i])
				}
			}
			if math.IsNaN(w.PE) || math.IsNaN(w.KE) {
				t.Fatalf("%s n=%d: NaN energy", preset, n)
			}
		}
	}
}

func BenchmarkStep4000(b *testing.B) {
	w := New(120, 72, 1)
	w.Load("drop", MaxN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Step()
	}
}
