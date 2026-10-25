// Command particlesim runs a 2D Lennard-Jones particle simulation and serves
// a browser visualizer for it on the local machine.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	_ "embed"

	"particlesim/sim"
)

//go:embed web/index.html
var indexHTML []byte

const (
	worldW      = 120.0
	worldH      = 72.0
	headerBytes = 32
	tickRate    = 60
)

// Server owns the world and the goroutine that advances it.
type Server struct {
	mu     sync.Mutex
	world  *sim.World
	stepMs float64
}

type stateJSON struct {
	Params  sim.Params `json:"params"`
	N       int        `json:"n"`
	Width   float64    `json:"width"`
	Height  float64    `json:"height"`
	Preset  string     `json:"preset"`
	Presets []string   `json:"presets"`
}

func (s *Server) run(ctx context.Context) {
	t := time.NewTicker(time.Second / tickRate)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		if !s.world.P.Paused {
			start := time.Now()
			for k := 0; k < s.world.P.Substeps; k++ {
				s.world.Step()
			}
			ms := float64(time.Since(start).Microseconds()) / 1000
			s.stepMs = 0.9*s.stepMs + 0.1*ms
		}
		s.mu.Unlock()
	}
}

// stateLocked must be called with s.mu held.
func (s *Server) stateLocked() stateJSON {
	w := s.world
	return stateJSON{Params: w.P, N: w.N, Width: w.W, Height: w.H, Preset: w.Preset, Presets: sim.Presets}
}

// frame layout, little endian: a 32 byte header of eight 4 byte fields
// (n uint32, width, height, time, kinetic, potential, stepMs float32,
// steps uint32) followed by n records of x, y, speed as float32.
func (s *Server) encodeFrame() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.world
	buf := make([]byte, headerBytes+w.N*12)
	le := binary.LittleEndian
	f32 := func(off int, v float64) { le.PutUint32(buf[off:], math.Float32bits(float32(v))) }
	le.PutUint32(buf[0:], uint32(w.N))
	f32(4, w.W)
	f32(8, w.H)
	f32(12, w.Time)
	f32(16, w.KE)
	f32(20, w.PE)
	f32(24, s.stepMs)
	le.PutUint32(buf[28:], uint32(w.Steps))
	off := headerBytes
	for i := 0; i < w.N; i++ {
		f32(off, w.X[i])
		f32(off+4, w.Y[i])
		f32(off+8, math.Hypot(w.VX[i], w.VY[i]))
		off += 12
	}
	return buf
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(rw).Encode(v)
}

func readBody(rw http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, 1<<16))
	if err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		_, _ = rw.Write(indexHTML)
	})

	mux.HandleFunc("GET /frame", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/octet-stream")
		rw.Header().Set("Cache-Control", "no-store")
		_, _ = rw.Write(s.encodeFrame())
	})

	mux.HandleFunc("GET /state", func(rw http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		st := s.stateLocked()
		s.mu.Unlock()
		writeJSON(rw, st)
	})

	// Partial update: fields missing from the body keep their value.
	mux.HandleFunc("POST /params", func(rw http.ResponseWriter, r *http.Request) {
		body, ok := readBody(rw, r)
		if !ok {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		p := s.world.P
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(rw, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		p.Clamp()
		s.world.P = p
		writeJSON(rw, s.stateLocked())
	})

	mux.HandleFunc("POST /reset", func(rw http.ResponseWriter, r *http.Request) {
		body, ok := readBody(rw, r)
		if !ok {
			return
		}
		req := struct {
			Preset string `json:"preset"`
			N      int    `json:"n"`
		}{Preset: "drop", N: 2500}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(rw, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if !sim.ValidPreset(req.Preset) {
			http.Error(rw, "unknown preset", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.world.Load(req.Preset, req.N)
		writeJSON(rw, s.stateLocked())
	})

	mux.HandleFunc("POST /pointer", func(rw http.ResponseWriter, r *http.Request) {
		body, ok := readBody(rw, r)
		if !ok {
			return
		}
		var raw struct {
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
			Mode int     `json:"mode"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			http.Error(rw, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		p := sim.Pointer{X: raw.X, Y: raw.Y}
		switch {
		case raw.Mode > 0:
			p.Mode = 1
		case raw.Mode < 0:
			p.Mode = -1
		}
		s.mu.Lock()
		s.world.Ptr = p
		s.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (loopback by default)")
	n := flag.Int("n", 2500, "number of particles")
	preset := flag.String("preset", "drop", "starting preset: drop, crystal, collision or gas")
	seed := flag.Int64("seed", 1, "random seed for initial conditions")
	flag.Parse()

	if !sim.ValidPreset(*preset) {
		log.Fatalf("unknown preset %q", *preset)
	}

	world := sim.New(worldW, worldH, *seed)
	world.Load(*preset, *n)
	s := &Server{world: world}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go s.run(ctx)

	srv := &http.Server{Addr: *addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	fmt.Printf("particlesim running at http://%s  (%d particles, preset %s)\nPress Ctrl+C to stop.\n", *addr, world.N, world.Preset)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
