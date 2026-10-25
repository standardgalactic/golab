// Package pipeline is a teaching model of a five-stage record pipeline:
// inscribe, validate, admit, project, activate. Each stage consumes the
// type produced by the previous stage and produces a type of its own, so
// the compiler refuses to confuse a record that has merely been stored
// with one that has been admitted.
package pipeline

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Record is the content that an author inscribes.
type Record struct {
	ID     string   `json:"id"`
	Author string   `json:"author"`
	Scope  string   `json:"scope"`
	Body   string   `json:"body"`
	Refs   []string `json:"refs,omitempty"`
}

// Receipt is the SHA-256 digest of the canonical encoding of a Record.
type Receipt string

// Inscribed is a record that has been stored and receipted. Inscription
// makes no judgment of any kind about the record.
type Inscribed struct {
	rec     Record
	receipt Receipt
}

// Record returns a copy of the inscribed record.
func (i Inscribed) Record() Record { return copyRecord(i.rec) }

// Receipt returns the receipt issued at inscription.
func (i Inscribed) Receipt() Receipt { return i.receipt }

func copyRecord(r Record) Record {
	r.Refs = append([]string(nil), r.Refs...)
	return r
}

// Inscribe stores a record and returns its receipt. The function takes no
// policy and returns no error: whatever is inscribed is inscribed, and
// whether it is acceptable is decided by later stages.
func Inscribe(r Record) Inscribed {
	r = copyRecord(r)
	b, _ := json.Marshal(r) // a struct of strings and a string slice cannot fail to encode
	sum := sha256.Sum256(b)
	return Inscribed{rec: r, receipt: Receipt(hex.EncodeToString(sum[:]))}
}

// LoadJSONL reads one Record per non-empty line.
func LoadJSONL(r io.Reader) ([]Record, error) {
	var out []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("records line %d: %w", line, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	return out, nil
}

func sortedIDs(m map[string]Inscribed) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
