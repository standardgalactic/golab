package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// faultyFile wraps a real file and fails in chosen ways.
type faultyFile struct {
	*os.File
	writeOnly   int // write this many bytes of the line, then fail; -1 for no fault
	failTrunc   bool
	failSync    bool
	truncations int
}

var errDisk = errors.New("no space left on device")

func (f *faultyFile) Write(b []byte) (int, error) {
	if f.writeOnly < 0 {
		return f.File.Write(b)
	}
	n := f.writeOnly
	if n > len(b) {
		n = len(b)
	}
	f.File.Write(b[:n])
	return n, errDisk
}

func (f *faultyFile) Truncate(n int64) error {
	f.truncations++
	if f.failTrunc {
		return errors.New("truncate refused")
	}
	return f.File.Truncate(n)
}

func (f *faultyFile) Sync() error {
	if f.failSync {
		return errors.New("sync refused")
	}
	return f.File.Sync()
}

func TestPartialWriteIsCutBackAndLedgerStaysUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.jsonl")
	now := time.Unix(1_700_000_000, 0)
	if _, err := Append(path, "ok", "first", nil, now); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	var ff *faultyFile
	old := openAppend
	openAppend = func(p string) (appendFile, error) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		ff = &faultyFile{File: f, writeOnly: 40}
		return ff, err
	}
	_, err := Append(path, "ok", "second", nil, now)
	openAppend = old
	if !errors.Is(err, errDisk) {
		t.Fatalf("err = %v, want the disk error", err)
	}
	if ff.truncations != 1 {
		t.Fatalf("truncations = %d", ff.truncations)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("file changed by a failed append:\n%q\n%q", before, after)
	}
	if _, err := Append(path, "ok", "third", nil, now); err != nil {
		t.Fatalf("ledger unusable after a failed append: %v", err)
	}
	recs, err := Load(path)
	if err != nil || len(recs) != 2 || Check(recs) != nil {
		t.Fatalf("len=%d err=%v", len(recs), err)
	}
}

func TestFailureToRemoveTheFragmentIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.jsonl")
	old := openAppend
	defer func() { openAppend = old }()
	openAppend = func(p string) (appendFile, error) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		return &faultyFile{File: f, writeOnly: 10, failTrunc: true}, err
	}
	_, err := Append(path, "ok", "x", nil, time.Unix(1, 0))
	if err == nil || !errors.Is(err, errDisk) {
		t.Fatalf("err = %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "could not be removed") {
		t.Fatalf("message hides the damage: %q", got)
	}
	if _, lerr := Load(path); lerr == nil {
		t.Fatal("expected the fragment to be visible to Load")
	}
}

// An error from Append does not mean the record is absent. After a failed
// sync the record is in the file, and the caller has been told of a failure.
func TestSyncFailureReportsAnErrorButTheRecordIsPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.jsonl")
	old := openAppend
	defer func() { openAppend = old }()
	openAppend = func(p string) (appendFile, error) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		return &faultyFile{File: f, writeOnly: -1, failSync: true}, err
	}
	if _, err := Append(path, "ok", "x", nil, time.Unix(1, 0)); err == nil {
		t.Fatal("expected an error")
	}
	recs, err := Load(path)
	if err != nil || len(recs) != 1 {
		t.Fatalf("len=%d err=%v", len(recs), err)
	}
}

func TestExtendMatchesAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.jsonl")
	now := time.Unix(1_700_000_000, 0)
	var mem []Record
	for _, a := range []string{"a", "b", "c"} {
		disk, err := Append(path, a, "s", map[string]string{"k": a}, now)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Extend(mem, a, "s", map[string]string{"k": a}, now)
		if err != nil {
			t.Fatal(err)
		}
		if r.Digest != disk.Digest || r.Seq != disk.Seq || r.Prev != disk.Prev {
			t.Fatalf("Extend and Append disagree: %+v %+v", r, disk)
		}
		mem = append(mem, r)
	}
	if err := Check(mem); err != nil {
		t.Fatal(err)
	}
}
