package ledger

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriterSerializesConcurrentAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	w := NewWriter(path, 8)

	const goroutines, each = 20, 10
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				_, err := w.Append("event", fmt.Sprintf("g%d-%d", g, i), map[string]string{"g": fmt.Sprint(g)})
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != goroutines*each {
		t.Fatalf("%d records, want %d", len(records), goroutines*each)
	}
	if err := Check(records); err != nil {
		t.Fatalf("chain broken by concurrent writers: %v", err)
	}
	subjects := map[string]bool{}
	for _, r := range records {
		subjects[r.Subject] = true
	}
	if len(subjects) != goroutines*each {
		t.Fatalf("%d distinct subjects, want %d: records were lost or repeated", len(subjects), goroutines*each)
	}
}

func TestAppendAfterCloseFails(t *testing.T) {
	w := NewWriter(filepath.Join(t.TempDir(), "l.jsonl"), 1)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append("x", "y", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseWhileAppendersAreActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	w := NewWriter(path, 2)

	var wg sync.WaitGroup
	var accepted, refused sync.Map
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				subject := fmt.Sprintf("g%d-%d", g, i)
				if _, err := w.Append("event", subject, nil); err == nil {
					accepted.Store(subject, true)
				} else if errors.Is(err, ErrClosed) {
					refused.Store(subject, true)
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(g)
	}
	go w.Close() // closes at an arbitrary moment during the appends
	wg.Wait()
	w.Close()

	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(records); err != nil {
		t.Fatal(err)
	}
	// Every append that reported success is on disk, and nothing else is.
	n := 0
	accepted.Range(func(_, _ any) bool { n++; return true })
	if n != len(records) {
		t.Fatalf("%d appends reported success but the ledger holds %d records", n, len(records))
	}
}

func TestSnapshotSeesCompletedAppendsAndNeverATornRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.jsonl")
	w := NewWriter(path, 8)
	defer w.Close()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, err := w.Append("a", fmt.Sprintf("g%d-%d", g, i), nil); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	last := 0
	for i := 0; i < 50; i++ {
		recs, err := w.Snapshot()
		if err != nil {
			t.Fatalf("snapshot during appends: %v", err)
		}
		if err := Check(recs); err != nil {
			t.Fatal(err)
		}
		if len(recs) < last {
			t.Fatalf("snapshot shrank from %d to %d", last, len(recs))
		}
		last = len(recs)
	}
	wg.Wait()
	recs, _ := w.Snapshot()
	if len(recs) != 200 {
		t.Fatalf("len = %d, want 200", len(recs))
	}
	w.Close()
	if _, err := w.Snapshot(); !errors.Is(err, ErrClosed) {
		t.Fatalf("snapshot after close: %v", err)
	}
}
