package index

import (
	"fmt"
	"testing"
	"time"

	"golab/internal/ledger"
)

func records(b *testing.B, n int) []ledger.Record {
	b.Helper()
	var recs []ledger.Record
	for i := 0; i < n; i++ {
		r, err := ledger.Extend(recs, "verify-file", fmt.Sprintf("papers/f%05d.txt", i%500),
			map[string]string{"sha256": fmt.Sprint(i)}, time.Unix(1_700_000_000, 0))
		if err != nil {
			b.Fatal(err)
		}
		recs = append(recs, r)
	}
	return recs
}

// BenchmarkBuild measures rebuilding an index from a whole ledger in memory,
// including the check of the chain.
func BenchmarkBuild(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		recs := records(b, n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := Build(recs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkApplyOne measures extending an index by one record, which is the
// cost the index exists to keep small. The index is reset, outside the timer,
// when the prepared records run out.
func BenchmarkApplyOne(b *testing.B) {
	recs := records(b, 10000)
	x, k := New(), 0
	for i := 0; i < b.N; i++ {
		if k == len(recs) {
			b.StopTimer()
			x, k = New(), 0
			b.StartTimer()
		}
		if err := x.Apply(recs[k]); err != nil {
			b.Fatal(err)
		}
		k++
	}
}
