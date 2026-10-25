package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// buildLedger writes n records to a new file and returns its path.
func buildLedger(b *testing.B, n int) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "base.jsonl")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	var recs []Record
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < n; i++ {
		r, err := Extend(recs, "verify-file", fmt.Sprintf("papers/f%05d.txt", i%500), map[string]string{"sha256": "00"}, now)
		if err != nil {
			b.Fatal(err)
		}
		recs = append(recs, r)
		line, _ := json.Marshal(r)
		f.Write(append(line, '\n'))
	}
	return path
}

// BenchmarkAppendBySize measures one append to a ledger that already holds n
// records. The file is restored before each iteration, outside the timer, so
// the size stays at n.
func BenchmarkAppendBySize(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			base := buildLedger(b, n)
			data, _ := os.ReadFile(base)
			work := filepath.Join(b.TempDir(), "work.jsonl")
			now := time.Unix(1_700_000_000, 0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				os.WriteFile(work, data, 0o644)
				b.StartTimer()
				if _, err := Append(work, "run", "x", nil, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type noSync struct{ *os.File }

func (noSync) Sync() error { return nil }

// BenchmarkSyncCost compares appending with and without the flush to stable
// storage, on a small ledger so that the fixed cost of the flush dominates.
func BenchmarkSyncCost(b *testing.B) {
	for _, mode := range []string{"sync", "nosync"} {
		b.Run(mode, func(b *testing.B) {
			old := openAppend
			defer func() { openAppend = old }()
			if mode == "nosync" {
				openAppend = func(p string) (appendFile, error) {
					f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
					return noSync{f}, err
				}
			}
			base := buildLedger(b, 100)
			data, _ := os.ReadFile(base)
			work := filepath.Join(b.TempDir(), "work.jsonl")
			now := time.Unix(1_700_000_000, 0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				os.WriteFile(work, data, 0o644)
				b.StartTimer()
				if _, err := Append(work, "run", "x", nil, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
