package summary

import (
	"sync"
	"testing"
)

// The expected table, written out by hand from what each summary keeps.
// Rows follow Summarizers and columns follow Decisions.
var want = map[string][]bool{
	"every record":        {true, true, true, true, true, true},
	"action counts":       {false, false, false, false, false, true},
	"per-path index":      {true, true, true, false, false, false},
	"index and counts":    {true, true, true, false, false, true},
	"last three records":  {false, false, false, false, false, false},
	"index and two flags": {true, true, true, true, true, false},
}

var (
	once sync.Once
	run4 Result
	err4 error
)

// shared runs the search once for all tests, over ledgers of up to four
// records, which is long enough to expose every counterexample in the table.
func shared(t *testing.T) Result {
	t.Helper()
	once.Do(func() { run4, err4 = Run(4) })
	if err4 != nil {
		t.Fatal(err4)
	}
	return run4
}

func TestAvailabilityMatrix(t *testing.T) {
	res := shared(t)
	if res.Ledgers != 1+7+49+343+2401 {
		t.Fatalf("ledgers = %d", res.Ledgers)
	}
	for i, s := range res.Summarizers {
		for d := range res.Decisions {
			if got := res.Cells[i][d].Available; got != want[s][d] {
				t.Errorf("%s / D%d (%s): available=%v, want %v", s, d+1, res.Decisions[d], got, want[s][d])
			}
		}
	}
}

// Every reported counterexample is checked from scratch: the two ledgers
// have equal summaries and different answers.
func TestCounterexamplesAreGenuine(t *testing.T) {
	res := shared(t)
	sums, decs := Summarizers(), Decisions()
	found := 0
	for i := range sums {
		for d := range decs {
			c := res.Cells[i][d]
			if c.Available {
				if c.Counterexample != nil {
					t.Errorf("available cell has a counterexample")
				}
				continue
			}
			found++
			ra, _ := Records(c.Counterexample.A)
			rb, _ := Records(c.Counterexample.B)
			if sums[i].Fn(ra) != sums[i].Fn(rb) {
				t.Errorf("%s / D%d: summaries differ", sums[i].Name, d+1)
			}
			if decs[d].Fn(ra) == decs[d].Fn(rb) {
				t.Errorf("%s / D%d: answers agree", sums[i].Name, d+1)
			}
		}
	}
	if found == 0 {
		t.Fatal("no counterexamples at all")
	}
}

func TestCompressionActuallyCompresses(t *testing.T) {
	res := shared(t)
	full := res.Distinct[0]
	if full != res.Ledgers {
		t.Fatalf("the full record should distinguish every ledger: %d of %d", full, res.Ledgers)
	}
	for i := 1; i < len(res.Distinct); i++ {
		if res.Distinct[i] >= full {
			t.Errorf("%s does not reduce the number of distinct states", res.Summarizers[i])
		}
	}
}

// The experiment is deterministic: running it twice gives the same table, so
// a manifest of its parameters is enough to reproduce its result.
func TestSearchIsDeterministic(t *testing.T) {
	a, err := Run(3)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Run(3)
	if a.Table() != b.Table() {
		t.Fatal("two runs of the same search gave different tables")
	}
}
