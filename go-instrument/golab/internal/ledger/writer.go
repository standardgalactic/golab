package ledger

import (
	"errors"
	"sync"
	"time"
)

// ErrClosed is returned by Append on a Writer that has been closed.
var ErrClosed = errors.New("ledger writer closed")

// Writer owns a ledger file for the lifetime of a process. Every append is
// sent over a channel to one goroutine, which is the only code in the
// process that touches the file. Many goroutines may call Append at once;
// the writer serializes them, so the chain of digests cannot fork inside
// this process.
//
// A Writer protects nothing against other processes. Two processes that
// each hold a Writer on the same file can still fork the chain.
type Writer struct {
	path string
	reqs chan request
	done chan struct{}

	mu     sync.RWMutex // guards closed, and orders sends on reqs against Close
	closed bool
}

type request struct {
	snapshot bool // a read of the whole ledger, not an append
	action   string
	subject  string
	detail   map[string]string
	reply    chan result
}

type result struct {
	rec  Record
	recs []Record
	err  error
}

// NewWriter starts the goroutine that owns the file at path. The buffer
// size is the number of requests that may queue before Append callers wait.
func NewWriter(path string, buffer int) *Writer {
	w := &Writer{
		path: path,
		reqs: make(chan request, buffer),
		done: make(chan struct{}),
	}
	go w.loop()
	return w
}

func (w *Writer) loop() {
	defer close(w.done)
	for req := range w.reqs {
		if req.snapshot {
			recs, err := Load(w.path)
			req.reply <- result{recs: recs, err: err}
			continue
		}
		rec, err := Append(w.path, req.action, req.subject, req.detail, time.Now())
		req.reply <- result{rec: rec, err: err} // reply is buffered, so this never blocks
	}
}

// Append writes one record and returns it once it is on disk. It may be
// called from any number of goroutines. The detail map is copied before it
// is queued, so the caller may reuse it.
func (w *Writer) Append(action, subject string, detail map[string]string) (Record, error) {
	req := request{
		action:  action,
		subject: subject,
		detail:  copyDetail(detail),
		reply:   make(chan result, 1),
	}

	// Holding the read lock while sending guarantees that Close cannot
	// close the channel under a sender. Close takes the write lock, so it
	// waits for sends already in progress, and the loop keeps receiving, so
	// those sends do complete.
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return Record{}, ErrClosed
	}
	w.reqs <- req
	w.mu.RUnlock()

	res := <-req.reply
	return res.rec, res.err
}

// Snapshot returns every record in the ledger as of a moment between two
// appends. The read is performed by the owning goroutine, so it never sees a
// record that is only partly written. It does not check the chain.
func (w *Writer) Snapshot() ([]Record, error) {
	req := request{snapshot: true, reply: make(chan result, 1)}
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil, ErrClosed
	}
	w.reqs <- req
	w.mu.RUnlock()
	res := <-req.reply
	return res.recs, res.err
}

// Close stops accepting appends, writes every request already accepted,
// and waits for the owning goroutine to finish. It is safe to call more
// than once.
func (w *Writer) Close() error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.reqs)
	}
	w.mu.Unlock()
	<-w.done
	return nil
}

func copyDetail(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
