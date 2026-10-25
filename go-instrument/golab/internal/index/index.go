// Package index derives a lookup table from the ledger. The table is a
// convenience and never an authority: it is computed entirely from the ledger
// records, it carries the digest of the last record it has seen so that its
// age can be checked, and it can be deleted at any time and rebuilt to
// exactly the same bytes.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"golab/internal/atomicfile"
	"golab/internal/ledger"
)

// Version is written into every index file. A file with another version is
// not trusted and is rebuilt.
const Version = 1

// Errors reported when an index cannot be used as it stands. None of them is
// fatal to a caller that can fall back on Build.
var (
	ErrOutOfSequence = errors.New("record does not follow the index")
	ErrStale         = errors.New("index does not belong to this ledger")
)

// Entry summarizes everything the ledger says about one subject path.
type Entry struct {
	Path string `json:"path"`
	// LastDigest is the digest from the most recent successful verification
	// of the path, whether or not a later attempt failed.
	LastDigest string `json:"last_digest,omitempty"`
	// Verified is true when the most recent attempt succeeded.
	Verified bool `json:"verified"`
	Attempts int  `json:"attempts"`
	Failures int  `json:"failures"`
	// Seq is the sequence number of the last record that touched the path.
	Seq int `json:"seq"`
}

// Index is the derived table. Records and Head identify the prefix of the
// ledger it was computed from.
type Index struct {
	records int
	head    string
	byPath  map[string]*Entry
}

type wire struct {
	Version int     `json:"version"`
	Records int     `json:"records"`
	Head    string  `json:"head"`
	Entries []Entry `json:"entries"`
}

// New returns the index of an empty ledger.
func New() *Index { return &Index{byPath: map[string]*Entry{}} }

// Records is the number of ledger records the index has consumed.
func (x *Index) Records() int { return x.records }

// Head is the digest of the last record consumed, empty for none.
func (x *Index) Head() string { return x.head }

// Len is the number of distinct paths in the index.
func (x *Index) Len() int { return len(x.byPath) }

// Lookup returns a copy of the entry for path.
func (x *Index) Lookup(path string) (Entry, bool) {
	e, ok := x.byPath[path]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Entries returns copies of all entries sorted by path.
func (x *Index) Entries() []Entry {
	out := make([]Entry, 0, len(x.byPath))
	for _, e := range x.byPath {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Apply consumes the next ledger record. The record must be the immediate
// successor of the last one consumed, which is how the index guarantees that
// it describes a contiguous prefix of one chain.
func (x *Index) Apply(r ledger.Record) error {
	if r.Seq != x.records+1 || r.Prev != x.head {
		return fmt.Errorf("record %d: %w", r.Seq, ErrOutOfSequence)
	}
	switch r.Action {
	case "verify-file", "verify-file-failed":
		e := x.byPath[r.Subject]
		if e == nil {
			e = &Entry{Path: r.Subject}
			x.byPath[r.Subject] = e
		}
		e.Attempts++
		e.Seq = r.Seq
		if r.Action == "verify-file" {
			e.Verified = true
			e.LastDigest = r.Detail["sha256"]
		} else {
			e.Verified = false
			e.Failures++
		}
	}
	x.records = r.Seq
	x.head = r.Digest
	return nil
}

// Build computes the index of a whole ledger. It refuses a ledger whose
// chain does not hold, because a table derived from a damaged record would
// look authoritative and be nothing of the kind.
func Build(records []ledger.Record) (*Index, error) {
	if err := ledger.Check(records); err != nil {
		return nil, err
	}
	x := New()
	for _, r := range records {
		if err := x.Apply(r); err != nil {
			return nil, err
		}
	}
	return x, nil
}

// CatchUp brings an index forward to the end of records. It first checks that
// the index belongs to this ledger, meaning that the record at its position
// has the digest it remembers, and returns ErrStale if not. The remaining
// records are checked as the chain is extended.
func (x *Index) CatchUp(records []ledger.Record) error {
	if x.records > len(records) {
		return fmt.Errorf("index has seen %d records, ledger holds %d: %w", x.records, len(records), ErrStale)
	}
	if x.records > 0 && records[x.records-1].Digest != x.head {
		return fmt.Errorf("record %d has a different digest: %w", x.records, ErrStale)
	}
	if err := ledger.Check(records); err != nil {
		return err
	}
	for _, r := range records[x.records:] {
		if err := x.Apply(r); err != nil {
			return err
		}
	}
	return nil
}

// MarshalJSON writes the index with entries sorted by path, so equal indexes
// always produce equal bytes.
func (x *Index) MarshalJSON() ([]byte, error) {
	return json.Marshal(wire{Version: Version, Records: x.records, Head: x.head, Entries: x.Entries()})
}

// UnmarshalJSON reads an index and rejects files it does not understand.
func (x *Index) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	if w.Version != Version {
		return fmt.Errorf("index version %d, want %d", w.Version, Version)
	}
	m := make(map[string]*Entry, len(w.Entries))
	for i := range w.Entries {
		e := w.Entries[i]
		if _, dup := m[e.Path]; dup {
			return fmt.Errorf("duplicate entry for %q", e.Path)
		}
		m[e.Path] = &e
	}
	x.records, x.head, x.byPath = w.Records, w.Head, m
	return nil
}

// Save writes the index to path atomically.
func (x *Index) Save(path string) error {
	b, err := json.MarshalIndent(x, "", " ")
	if err != nil {
		return err
	}
	return atomicfile.WriteBytes(path, 0o644, append(b, '\n'))
}

// Source says how Open obtained an index.
type Source string

const (
	Loaded   Source = "loaded"
	CaughtUp Source = "caught up"
	Rebuilt  Source = "rebuilt"
)

// Open returns an index consistent with records, using the file at path when
// it is usable and rebuilding from the ledger when it is missing, damaged, of
// another version, or not a prefix of this ledger. The returned string gives
// the reason when the index was rebuilt. Open does not write the file.
func Open(path string, records []ledger.Record) (*Index, Source, string, error) {
	b, err := os.ReadFile(path)
	reason := ""
	if err != nil {
		reason = "no index file"
		if !errors.Is(err, os.ErrNotExist) {
			reason = err.Error()
		}
	} else {
		x := New()
		if uerr := json.Unmarshal(b, x); uerr != nil {
			reason = "unreadable index: " + uerr.Error()
		} else {
			behind := x.records < len(records)
			if cerr := x.CatchUp(records); cerr == nil {
				if behind {
					return x, CaughtUp, "", nil
				}
				return x, Loaded, "", nil
			} else if errors.Is(cerr, ledger.ErrBroken) {
				return nil, "", "", cerr
			} else {
				reason = cerr.Error()
			}
		}
	}
	x, err := Build(records)
	if err != nil {
		return nil, "", "", err
	}
	return x, Rebuilt, reason, nil
}
