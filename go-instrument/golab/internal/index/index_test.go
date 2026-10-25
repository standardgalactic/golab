package index

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golab/internal/ledger"
)

// chain builds a valid ledger of the given actions without touching disk.
func chain(t *testing.T, specs ...[3]string) []ledger.Record {
	t.Helper()
	path := filepath.Join(t.TempDir(), "l.jsonl")
	now := time.Unix(1_700_000_000, 0)
	for _, s := range specs {
		var d map[string]string
		if s[0] == "verify-file" {
			d = map[string]string{"sha256": s[2]}
		}
		if _, err := ledger.Append(path, s[0], s[1], d, now); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := ledger.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func enc(t *testing.T, x *Index) []byte {
	t.Helper()
	b, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var sample = [][3]string{
	{"verify-file", "a.txt", "d1"},
	{"verify-file", "b.txt", "d2"},
	{"run", "echo hi", ""},
	{"verify-file-failed", "b.txt", ""},
	{"verify-file", "a.txt", "d3"},
	{"verify", "dir", ""},
}

func TestBuildSummarizesPerPath(t *testing.T) {
	x, err := Build(chain(t, sample...))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := x.Lookup("a.txt")
	if a.LastDigest != "d3" || !a.Verified || a.Attempts != 2 || a.Seq != 5 {
		t.Fatalf("a = %+v", a)
	}
	b, _ := x.Lookup("b.txt")
	if b.LastDigest != "d2" || b.Verified || b.Failures != 1 || b.Seq != 4 {
		t.Fatalf("b = %+v", b)
	}
	if x.Records() != 6 || x.Len() != 2 {
		t.Fatalf("records=%d len=%d", x.Records(), x.Len())
	}
}

// Folding the records one at a time, from any prefix, gives the same index as
// building from all of them at once.
func TestIncrementalEqualsRebuild(t *testing.T) {
	rs := chain(t, sample...)
	want := enc(t, mustBuild(t, rs))
	for cut := 0; cut <= len(rs); cut++ {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			x := mustBuild(t, rs[:cut])
			if err := x.CatchUp(rs); err != nil {
				t.Fatal(err)
			}
			if got := enc(t, x); !bytes.Equal(got, want) {
				t.Fatalf("cut %d differs:\n%s\n%s", cut, got, want)
			}
		})
	}
}

func mustBuild(t *testing.T, rs []ledger.Record) *Index {
	t.Helper()
	x, err := Build(rs)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestSaveLoadRoundTripIsByteIdentical(t *testing.T) {
	rs := chain(t, sample...)
	p := filepath.Join(t.TempDir(), "idx.json")
	if err := mustBuild(t, rs).Save(p); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(p)

	x, src, _, err := Open(p, rs)
	if err != nil || src != Loaded {
		t.Fatalf("src=%v err=%v", src, err)
	}
	if err := x.Save(p); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(p)
	if !bytes.Equal(first, second) {
		t.Fatal("round trip changed the bytes")
	}

	// Deleting the file and rebuilding gives the same bytes again.
	os.Remove(p)
	y, src, reason, err := Open(p, rs)
	if err != nil || src != Rebuilt || reason == "" {
		t.Fatalf("src=%v reason=%q err=%v", src, reason, err)
	}
	y.Save(p)
	third, _ := os.ReadFile(p)
	if !bytes.Equal(first, third) {
		t.Fatal("rebuilt index differs from the original")
	}
}

func TestApplyRejectsGapsAndForeignRecords(t *testing.T) {
	rs := chain(t, sample...)
	x := New()
	if err := x.Apply(rs[1]); !errors.Is(err, ErrOutOfSequence) {
		t.Fatalf("skipping a record: %v", err)
	}
	x.Apply(rs[0])
	other := chain(t, [3]string{"run", "other", ""}, [3]string{"run", "other2", ""})
	if err := x.Apply(other[1]); !errors.Is(err, ErrOutOfSequence) {
		t.Fatalf("record from another ledger: %v", err)
	}
}

func TestOpenCatchesUpAndDetectsForeignLedger(t *testing.T) {
	rs := chain(t, sample...)
	p := filepath.Join(t.TempDir(), "idx.json")
	mustBuild(t, rs[:3]).Save(p)

	x, src, _, err := Open(p, rs)
	if err != nil || src != CaughtUp || x.Records() != len(rs) {
		t.Fatalf("src=%v n=%d err=%v", src, x.Records(), err)
	}

	// An index saved from a different ledger of the same length is stale.
	other := chain(t, [3]string{"run", "x", ""}, [3]string{"run", "y", ""}, [3]string{"run", "z", ""},
		[3]string{"run", "w", ""}, [3]string{"run", "v", ""}, [3]string{"run", "u", ""})
	mustBuild(t, other).Save(p)
	_, src, reason, err := Open(p, rs)
	if err != nil || src != Rebuilt || reason == "" {
		t.Fatalf("src=%v reason=%q err=%v", src, reason, err)
	}
}

func TestOpenRebuildsFromDamagedOrOldFiles(t *testing.T) {
	rs := chain(t, sample...)
	dir := t.TempDir()
	for name, content := range map[string]string{
		"garbage":  "not json",
		"version":  `{"version":99,"records":0,"head":"","entries":[]}`,
		"dupe":     `{"version":1,"records":0,"head":"","entries":[{"path":"a"},{"path":"a"}]}`,
		"too-long": `{"version":1,"records":50,"head":"zz","entries":[]}`,
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		_, src, reason, err := Open(p, rs)
		if err != nil || src != Rebuilt || reason == "" {
			t.Errorf("%s: src=%v reason=%q err=%v", name, src, reason, err)
		}
	}
}

func TestBuildRefusesABrokenLedger(t *testing.T) {
	rs := chain(t, sample...)
	rs[2].Subject = "tampered"
	if _, err := Build(rs); !errors.Is(err, ledger.ErrBroken) {
		t.Fatalf("err = %v", err)
	}
	p := filepath.Join(t.TempDir(), "idx.json")
	mustBuild(t, chain(t, sample...)).Save(p)
	if _, _, _, err := Open(p, rs); !errors.Is(err, ledger.ErrBroken) {
		t.Fatalf("Open on a broken ledger: %v", err)
	}
}
