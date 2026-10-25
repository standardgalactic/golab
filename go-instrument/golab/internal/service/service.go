// Package service exposes golab over HTTP. The structure keeps three
// questions apart. Routing chooses a handler from the method and path alone.
// Adjudication decides whether the caller may use that handler. Admission,
// inside the handler for records, decides what becomes of the records
// submitted. Only the last is a judgment about content.
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golab/internal/index"
	"golab/internal/ledger"
	"golab/internal/pipeline"
)

// MaxBody is the largest request body the service reads.
const MaxBody = 1 << 20

// Server holds what the handlers share. The ledger is reached only through
// the Writer, which makes the service the single owner of the ledger within
// its process.
type Server struct {
	Ledger *ledger.Writer
	Policy pipeline.Policy
	Tokens *Tokens
	// Logger receives one structured entry per request. It never receives a
	// credential. A nil Logger discards everything.
	Logger *slog.Logger
	router *Router
}

// New builds the server and its routing table.
func New(w *ledger.Writer, p pipeline.Policy, t *Tokens) *Server {
	s := &Server{Ledger: w, Policy: p, Tokens: t, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.router = NewRouter([]Route{
		{http.MethodGet, "/healthz", NeedNothing, http.HandlerFunc(s.health)},
		{http.MethodGet, "/v1/ledger/head", NeedRead, http.HandlerFunc(s.head)},
		{http.MethodGet, "/v1/ledger", NeedRead, http.HandlerFunc(s.list)},
		{http.MethodGet, "/v1/index", NeedRead, http.HandlerFunc(s.lookup)},
		{http.MethodPost, "/v1/admit", NeedWrite, http.HandlerFunc(s.admit)},
	})
	return s
}

// statusRecorder remembers the status and size of a response so that the
// request can be logged after the handler has finished.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ServeHTTP routes, then adjudicates, then runs the handler, and logs the
// outcome of every request including the refused ones. Routing comes first,
// so a request for a path that does not exist receives 404 whether or not it
// carries a credential.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	principal := ""
	defer func() {
		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		s.Logger.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", float64(time.Since(start).Microseconds())/1000,
			"principal", principal,
		)
	}()
	w = rec

	route, status, allow := s.router.Match(r.Method, r.URL.Path)
	if route == nil {
		if allow != nil {
			w.Header().Set("Allow", allowHeader(allow))
		}
		writeError(w, status, http.StatusText(status))
		return
	}
	p, authenticated := s.Tokens.Authenticate(r)
	if authenticated {
		principal = p.Name
	}
	if status, ok := Permit(p, authenticated, route.Need); !ok {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="golab"`)
		}
		writeError(w, status, http.StatusText(status))
		return
	}
	route.Handler.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) // the status is already sent; nothing useful can be done with a failure here
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// internal answers a failure of the server's own machinery. The client is
// told only that something went wrong; the cause, which may contain file
// names and system messages, goes to the log.
func (s *Server) internal(w http.ResponseWriter, err error) {
	s.Logger.Error("internal error", "error", err.Error())
	writeError(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) head(w http.ResponseWriter, r *http.Request) {
	recs, err := s.Ledger.Snapshot()
	if err != nil {
		s.internal(w, err)
		return
	}
	h := map[string]any{"records": len(recs), "head": ""}
	if n := len(recs); n > 0 {
		h["head"] = recs[n-1].Digest
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	after, err := intParam(r, "after", 0, 0, 1<<31-1)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := intParam(r, "limit", 100, 1, 1000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	recs, err := s.Ledger.Snapshot()
	if err != nil {
		s.internal(w, err)
		return
	}
	if after > len(recs) {
		after = len(recs)
	}
	recs = recs[after:]
	if len(recs) > limit {
		recs = recs[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"after": after, "records": recs})
}

func intParam(r *http.Request, name string, def, lo, hi int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be an integer from %d to %d", name, lo, hi)
	}
	return n, nil
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	recs, err := s.Ledger.Snapshot()
	if err != nil {
		s.internal(w, err)
		return
	}
	x, err := index.Build(recs)
	if err != nil {
		s.internal(w, err)
		return
	}
	e, ok := x.Lookup(path)
	if !ok {
		writeError(w, http.StatusNotFound, "no ledger record mentions that path")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

type judgment struct {
	ID          string `json:"id"`
	Disposition string `json:"disposition"`
	Receipt     string `json:"receipt"`
}

// admit runs a batch of records through the pipeline under the server's
// policy and writes one ledger record saying what happened and on whose
// request. A batch that fails validation is recorded too.
func (s *Server) admit(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBody))
	dec.DisallowUnknownFields()
	var batch []pipeline.Record
	if err := dec.Decode(&batch); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body exceeds limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	inscribed := make([]pipeline.Inscribed, len(batch))
	for i, rec := range batch {
		inscribed[i] = pipeline.Inscribe(rec)
	}
	valid, err := pipeline.Validate(inscribed)
	if err != nil {
		detail := map[string]string{"stage": "validate", "error": err.Error(), "principal": p.Name}
		if _, lerr := s.Ledger.Append("admit-failed", "http", detail); lerr != nil {
			s.internal(w, lerr)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	admitted := pipeline.Admit(valid, s.Policy)
	var out []judgment
	for _, j := range admitted.Judgments() {
		out = append(out, judgment{j.Record.ID, string(j.Disposition), string(j.Receipt)})
	}
	counts := map[string]int{}
	detail := map[string]string{"records": strconv.Itoa(valid.Len()), "principal": p.Name}
	for d, n := range admitted.Counts() {
		counts[string(d)] = n
		detail[string(d)] = strconv.Itoa(n)
	}
	rec, err := s.Ledger.Append("admit", "http", detail)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ledger_seq": rec.Seq, "judgments": out, "counts": counts})
}
