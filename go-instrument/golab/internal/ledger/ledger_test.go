package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func TestAppendBuildsChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	for i, action := range []string{"discover", "verify", "run"} {
		r, err := Append(path, action, "papers", map[string]string{"n": "1"}, t0.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if r.Seq != i+1 {
			t.Fatalf("seq %d, want %d", r.Seq, i+1)
		}
	}
	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("loaded %d records", len(records))
	}
	if err := Check(records); err != nil {
		t.Fatalf("chain should hold: %v", err)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	for _, a := range []string{"discover", "verify"} {
		if _, err := Append(path, a, "papers", nil, t0); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(string(raw), `"discover"`, `"discoveR"`, 1)
	if altered == string(raw) {
		t.Fatal("test setup failed to alter the ledger")
	}
	if err := os.WriteFile(path, []byte(altered), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(records); !errors.Is(err, ErrBroken) {
		t.Fatalf("want ErrBroken, got %v", err)
	}
	if _, err := Append(path, "run", "papers", nil, t0); !errors.Is(err, ErrBroken) {
		t.Fatalf("append to a broken ledger should fail with ErrBroken, got %v", err)
	}
}

func TestMissingLedgerIsEmpty(t *testing.T) {
	records, err := Load(filepath.Join(t.TempDir(), "none.jsonl"))
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%v err=%v", records, err)
	}
}

func TestOversizedRecordIsRefusedAndLedgerStaysReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.jsonl")
	now := time.Unix(1_700_000_000, 0)
	if _, err := Append(path, "a", "s", nil, now); err != nil {
		t.Fatal(err)
	}
	big := map[string]string{"blob": strings.Repeat("x", MaxLine)}
	if _, err := Append(path, "b", "s", big, now); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := Append(path, "c", "s", nil, now); err != nil {
		t.Fatalf("ledger unusable after refusal: %v", err)
	}
	rs, err := Load(path)
	if err != nil || len(rs) != 2 {
		t.Fatalf("len=%d err=%v", len(rs), err)
	}
}
