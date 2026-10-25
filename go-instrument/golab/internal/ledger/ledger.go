// Package ledger keeps an append-only record of what the tool did. Each
// record carries the digest of its predecessor, so any later alteration of
// an earlier line is detectable. The ledger records events; it does not
// decide whether they were permitted.
package ledger

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Record is one line of the ledger.
type Record struct {
	Seq     int               `json:"seq"`
	Time    time.Time         `json:"time"`
	Action  string            `json:"action"`
	Subject string            `json:"subject"`
	Detail  map[string]string `json:"detail,omitempty"`
	Prev    string            `json:"prev"`
	Digest  string            `json:"digest"`
}

// ErrBroken reports that the chain of digests does not hold.
var ErrBroken = errors.New("ledger chain broken")

// MaxLine is the longest encoded record, newline excluded, that Load can
// read back. Append refuses a longer one, because a record that can be
// written but not read would make every later append fail.
const MaxLine = 1 << 20

// ErrTooLarge reports a record whose encoding exceeds MaxLine.
var ErrTooLarge = errors.New("ledger record too large")

// digest computes the record digest over every field except Digest itself.
// json.Marshal sorts map keys, so the encoding is deterministic.
func digest(r Record) (string, error) {
	r.Digest = ""
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Load reads every record from path. A missing file is an empty ledger.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	defer f.Close()

	var records []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLine+1)
	for line := 1; sc.Scan(); line++ {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("ledger line %d: %w", line, err)
		}
		records = append(records, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	return records, nil
}

// Check walks the chain and returns ErrBroken, wrapped with the position,
// at the first record that does not match.
func Check(records []Record) error {
	prev := ""
	for i, r := range records {
		if r.Seq != i+1 {
			return fmt.Errorf("record %d has seq %d: %w", i+1, r.Seq, ErrBroken)
		}
		if r.Prev != prev {
			return fmt.Errorf("record %d has wrong predecessor: %w", r.Seq, ErrBroken)
		}
		want, err := digest(r)
		if err != nil {
			return err
		}
		if r.Digest != want {
			return fmt.Errorf("record %d digest mismatch: %w", r.Seq, ErrBroken)
		}
		prev = r.Digest
	}
	return nil
}

// Extend computes the record that would follow the existing ones. It does no
// input or output and does not check the chain; Append does both around it.
// The experiments of Part VI use it to build ledgers in memory.
func Extend(existing []Record, action, subject string, detail map[string]string, now time.Time) (Record, error) {
	r := Record{
		Seq:     len(existing) + 1,
		Time:    now.UTC().Truncate(time.Second),
		Action:  action,
		Subject: subject,
		Detail:  detail,
	}
	if n := len(existing); n > 0 {
		r.Prev = existing[n-1].Digest
	}
	var err error
	if r.Digest, err = digest(r); err != nil {
		return Record{}, err
	}
	return r, nil
}

// appendFile is the part of *os.File that writing a record uses. It exists
// so that tests can substitute a file that fails in chosen ways.
type appendFile interface {
	Write([]byte) (int, error)
	Stat() (os.FileInfo, error)
	Truncate(int64) error
	Sync() error
	Close() error
}

// openAppend opens the ledger for appending. Tests replace it; nothing else
// should. Because it is a variable, tests that replace it must not run in
// parallel with one another.
var openAppend = func(path string) (appendFile, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// Append adds one record to the ledger at path. It refuses to extend a
// ledger whose existing chain is already broken.
//
// If the write fails part-way, as it does when the disk fills, Append cuts
// the file back to the length it had before, so a failed append leaves no
// fragment behind. After a failure of the final flush to stable storage the
// record may or may not survive a crash: an error from Append means that the
// record is not guaranteed to be present, not that it is absent.
func Append(path, action, subject string, detail map[string]string, now time.Time) (Record, error) {
	existing, err := Load(path)
	if err != nil {
		return Record{}, err
	}
	if err := Check(existing); err != nil {
		return Record{}, fmt.Errorf("refusing to append: %w", err)
	}
	r, err := Extend(existing, action, subject, detail, now)
	if err != nil {
		return Record{}, err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	if len(line) > MaxLine {
		return Record{}, fmt.Errorf("record of %d bytes exceeds %d: %w", len(line), MaxLine, ErrTooLarge)
	}
	if err := writeLine(path, append(line, '\n')); err != nil {
		return Record{}, err
	}
	return r, nil
}

// writeLine appends one line and flushes it, restoring the previous length
// of the file if the write does not complete.
func writeLine(path string, line []byte) error {
	f, err := openAppend(path)
	if err != nil {
		return fmt.Errorf("open ledger for append: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat ledger: %w", err)
	}
	if n, werr := f.Write(line); werr != nil || n != len(line) {
		if werr == nil {
			werr = io.ErrShortWrite
		}
		err := fmt.Errorf("write ledger: %w", werr)
		if terr := f.Truncate(st.Size()); terr != nil {
			err = fmt.Errorf("%w; and the partial record could not be removed: %v", err, terr)
		}
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync ledger: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close ledger: %w", err)
	}
	return nil
}
