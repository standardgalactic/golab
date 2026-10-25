// Package server exposes a runner over HTTP: a page, a stream of frames
// (server-sent events) and a few JSON commands.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"trailsim/internal/runner"
	"trailsim/internal/world"
)

//go:embed web
var webFiles embed.FS

const maxBody = 4 << 10

// Server handles requests for one runner.
type Server struct {
	run *runner.Runner
	log *slog.Logger
	mux *http.ServeMux
}

// New builds the handler tree.
func New(run *runner.Runner, log *slog.Logger) *Server {
	s := &Server{run: run, log: log, mux: http.NewServeMux()}
	sub, _ := fs.Sub(webFiles, "web")
	s.mux.Handle("GET /{$}", http.FileServer(http.FS(sub)))
	s.mux.HandleFunc("GET /api/frame", s.frame)
	s.mux.HandleFunc("GET /api/stream", s.stream)
	s.mux.HandleFunc("POST /api/pause", s.pause)
	s.mux.HandleFunc("POST /api/speed", s.speed)
	s.mux.HandleFunc("POST /api/paint", s.paint)
	s.mux.HandleFunc("POST /api/reset", s.reset)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) frame(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.run.Frame())
}

// stream sends a frame whenever the version changes, until the client leaves.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	var last uint64
	t := time.NewTicker(33 * time.Millisecond)
	defer t.Stop()
	for {
		if f := s.run.Frame(); f != nil && f.Version != last {
			last = f.Version
			b, err := json.Marshal(f)
			if err != nil {
				s.log.Error("encode frame", "err", err)
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paused bool `json:"paused"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	s.reply(w, s.run.SetPaused(in.Paused))
}

func (s *Server) speed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Speed int `json:"speed"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	s.reply(w, s.run.SetSpeed(in.Speed))
}

func (s *Server) paint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind   world.PaintKind `json:"kind"`
		X      int             `json:"x"`
		Y      int             `json:"y"`
		Radius int             `json:"radius"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	s.reply(w, s.run.Paint(in.Kind, in.X, in.Y, in.Radius))
}

// reset overlays any fields present in the body on the current configuration.
func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	cfg := s.run.Config()
	in := struct {
		Ants        *int     `json:"ants"`
		Seed        *int64   `json:"seed"`
		Trails      *bool    `json:"trails"`
		Evaporation *float32 `json:"evaporation"`
		Diffusion   *float32 `json:"diffusion"`
	}{}
	if !s.decode(w, r, &in) {
		return
	}
	if in.Ants != nil {
		cfg.Ants = *in.Ants
	}
	if in.Seed != nil {
		cfg.Seed = *in.Seed
	}
	if in.Trails != nil {
		cfg.Trails = *in.Trails
	}
	if in.Evaporation != nil {
		cfg.Evaporation = *in.Evaporation
	}
	if in.Diffusion != nil {
		cfg.Diffusion = *in.Diffusion
	}
	s.reply(w, s.run.Reset(cfg))
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		}
		return false
	}
	return true
}

func (s *Server) reply(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case errors.Is(err, runner.ErrStopped):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// Serve listens on addr until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	log.Info("shutting down")
	if err := srv.Shutdown(sctx); err != nil {
		srv.Close()
	}
	return nil
}
