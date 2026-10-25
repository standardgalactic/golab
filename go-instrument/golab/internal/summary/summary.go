// Package summary studies what is lost when a ledger is replaced by a smaller
// description of it. A summary is a function of the ledger. A decision is a
// question about the ledger whose answer someone may later need. A decision
// is available from a summary exactly when no two ledgers with the same
// summary give different answers, because only then can the answer be
// computed from the summary alone. The package searches all ledgers up to a
// given length for a pair that breaks this, and reports the shortest pair it
// finds. The results concern records and ledgers; they say nothing about any
// learned model.
package summary

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"golab/internal/index"
	"golab/internal/ledger"
)

// Event is one thing that can happen in the small world the search explores.
type Event struct {
	Action  string // verify-file, verify-file-failed, or run
	Subject string // the path, or the program
	Detail  string // the digest for verify-file, the outcome for run
}

func (e Event) String() string {
	switch e.Action {
	case "verify-file":
		return fmt.Sprintf("verify %s=%s", e.Subject, e.Detail)
	case "verify-file-failed":
		return fmt.Sprintf("fail %s", e.Subject)
	}
	return "run " + e.Detail
}

// Alphabet is the set of events the search combines. Two paths, two digests
// for the first, one for the second, a failure for each, and a run that
// succeeds or fails.
var Alphabet = []Event{
	{"verify-file", "a", "d1"},
	{"verify-file", "a", "d2"},
	{"verify-file", "b", "d1"},
	{"verify-file-failed", "a", ""},
	{"verify-file-failed", "b", ""},
	{"run", "prog", "succeeded"},
	{"run", "prog", "failed"},
}

// Records turns a sequence of events into a real ledger chain, in memory.
func Records(events []Event) ([]ledger.Record, error) {
	var recs []ledger.Record
	t := time.Unix(1_700_000_000, 0)
	for _, e := range events {
		var detail map[string]string
		switch e.Action {
		case "verify-file":
			detail = map[string]string{"sha256": e.Detail}
		case "run":
			detail = map[string]string{"outcome": e.Detail}
		}
		r, err := ledger.Extend(recs, e.Action, e.Subject, detail, t)
		if err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	return recs, nil
}

// A Summarizer reduces a ledger to a canonical string. Two ledgers have the
// same summary exactly when the strings are equal.
type Summarizer struct {
	Name string
	Fn   func([]ledger.Record) string
}

// A Decision answers one question about a ledger.
type Decision struct {
	Name string
	Fn   func([]ledger.Record) string
}

func counts(rs []ledger.Record) map[string]int {
	m := map[string]int{"total": len(rs)}
	for _, r := range rs {
		m[r.Action]++
	}
	return m
}

func entries(rs []ledger.Record) []index.Entry {
	x, err := index.Build(rs)
	if err != nil {
		panic(err) // the search builds only valid chains
	}
	es := x.Entries()
	for i := range es {
		es[i].Seq = 0 // the sequence number of the last touch records position, which is not part of the summary
	}
	return es
}

func j(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Summarizers returns the summaries compared in Chapter 22.
func Summarizers() []Summarizer {
	return []Summarizer{
		{"every record", func(rs []ledger.Record) string {
			var sb strings.Builder
			for _, r := range rs {
				sb.WriteString(r.Digest)
			}
			return sb.String()
		}},
		{"action counts", func(rs []ledger.Record) string { return j(counts(rs)) }},
		{"per-path index", func(rs []ledger.Record) string { return j(entries(rs)) }},
		{"index and counts", func(rs []ledger.Record) string { return j([]any{entries(rs), counts(rs)}) }},
		{"last three records", func(rs []ledger.Record) string {
			if len(rs) > 3 {
				rs = rs[len(rs)-3:]
			}
			var parts []string
			for _, r := range rs {
				parts = append(parts, r.Action+"|"+r.Subject+"|"+r.Detail["sha256"]+"|"+r.Detail["outcome"])
			}
			return strings.Join(parts, ";")
		}},
		{"index and two flags", func(rs []ledger.Record) string {
			return j([]any{entries(rs), failAfterSuccess(rs, "a"), runFailedSinceLastAttempt(rs, "b")})
		}},
	}
}

func failAfterSuccess(rs []ledger.Record, path string) bool {
	seen := false
	for _, r := range rs {
		if r.Subject != path {
			continue
		}
		if r.Action == "verify-file" {
			seen = true
		} else if r.Action == "verify-file-failed" && seen {
			return true
		}
	}
	return false
}

func runFailedSinceLastAttempt(rs []ledger.Record, path string) bool {
	last := -1
	for i, r := range rs {
		if r.Subject == path && (r.Action == "verify-file" || r.Action == "verify-file-failed") {
			last = i
		}
	}
	for _, r := range rs[last+1:] {
		if r.Action == "run" && r.Detail["outcome"] == "failed" {
			return true
		}
	}
	return false
}

// Decisions returns the questions asked of every summary.
func Decisions() []Decision {
	return []Decision{
		{"last digest of a", func(rs []ledger.Record) string {
			d := "none"
			for _, r := range rs {
				if r.Action == "verify-file" && r.Subject == "a" {
					d = r.Detail["sha256"]
				}
			}
			return d
		}},
		{"is b verified now", func(rs []ledger.Record) string {
			s := "never"
			for _, r := range rs {
				if r.Subject == "b" {
					switch r.Action {
					case "verify-file":
						s = "yes"
					case "verify-file-failed":
						s = "no"
					}
				}
			}
			return s
		}},
		{"failures of a", func(rs []ledger.Record) string {
			n := 0
			for _, r := range rs {
				if r.Action == "verify-file-failed" && r.Subject == "a" {
					n++
				}
			}
			return fmt.Sprint(n)
		}},
		{"a failed after a success", func(rs []ledger.Record) string { return fmt.Sprint(failAfterSuccess(rs, "a")) }},
		{"run failed since b's last attempt", func(rs []ledger.Record) string {
			return fmt.Sprint(runFailedSinceLastAttempt(rs, "b"))
		}},
		{"number of records", func(rs []ledger.Record) string { return fmt.Sprint(len(rs)) }},
	}
}

// Pair is two ledgers that have the same summary and give different answers.
type Pair struct {
	A, B       []Event
	AnsA, AnsB string
}

// Cell is the result for one summary and one decision.
type Cell struct {
	Available      bool
	Counterexample *Pair
}

// Result is the whole table.
type Result struct {
	N           int
	Ledgers     int
	Summarizers []string
	Decisions   []string
	Distinct    []int    // number of distinct summaries, per summarizer
	Cells       [][]Cell // [summarizer][decision]
}

// Run examines every sequence of events of length 0 through n. Lengths are
// visited in increasing order, so a counterexample, once found, is among the
// shortest that exist.
func Run(n int) (Result, error) {
	sums, decs := Summarizers(), Decisions()
	res := Result{N: n}
	for _, s := range sums {
		res.Summarizers = append(res.Summarizers, s.Name)
	}
	for _, d := range decs {
		res.Decisions = append(res.Decisions, d.Name)
	}
	type seen struct {
		ans string
		ev  []Event
	}
	table := make([][]map[string]seen, len(sums)) // [summarizer][decision] summary -> first answer
	res.Cells = make([][]Cell, len(sums))
	for i := range sums {
		table[i] = make([]map[string]seen, len(decs))
		res.Cells[i] = make([]Cell, len(decs))
		for k := range decs {
			table[i][k] = map[string]seen{}
			res.Cells[i][k].Available = true
		}
	}
	distinct := make([]map[string]bool, len(sums))
	for i := range distinct {
		distinct[i] = map[string]bool{}
	}

	k := len(Alphabet)
	for length := 0; length <= n; length++ {
		idx := make([]int, length)
		for {
			events := make([]Event, length)
			for p, a := range idx {
				events[p] = Alphabet[a]
			}
			recs, err := Records(events)
			if err != nil {
				return Result{}, err
			}
			res.Ledgers++
			answers := make([]string, len(decs))
			for d := range decs {
				answers[d] = decs[d].Fn(recs)
			}
			for i, s := range sums {
				key := s.Fn(recs)
				distinct[i][key] = true
				for d := range decs {
					prev, ok := table[i][d][key]
					if !ok {
						table[i][d][key] = seen{answers[d], events}
						continue
					}
					if prev.ans != answers[d] && res.Cells[i][d].Available {
						res.Cells[i][d] = Cell{false, &Pair{prev.ev, events, prev.ans, answers[d]}}
					}
				}
			}
			// advance the counter
			p := length - 1
			for p >= 0 {
				idx[p]++
				if idx[p] < k {
					break
				}
				idx[p] = 0
				p--
			}
			if p < 0 {
				break
			}
		}
	}
	for i := range distinct {
		res.Distinct = append(res.Distinct, len(distinct[i]))
	}
	return res, nil
}

// Describe writes a sequence of events on one line.
func Describe(es []Event) string {
	if len(es) == 0 {
		return "(empty ledger)"
	}
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.String()
	}
	return strings.Join(parts, "; ")
}

// Table renders the result as text: one row per summary, one column per
// decision, with "yes" where the decision is available and "no" where a
// counterexample exists, followed by the counterexamples.
func (r Result) Table() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d ledgers of up to %d records\n\n", r.Ledgers, r.N)
	fmt.Fprintf(&sb, "%-21s %8s", "summary", "distinct")
	for i := range r.Decisions {
		fmt.Fprintf(&sb, "%5s", fmt.Sprintf("D%d", i+1))
	}
	sb.WriteString("\n")
	for i, s := range r.Summarizers {
		fmt.Fprintf(&sb, "%-21s %8d", s, r.Distinct[i])
		for d := range r.Decisions {
			if r.Cells[i][d].Available {
				sb.WriteString("  yes")
			} else {
				sb.WriteString("   no")
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	for i, d := range r.Decisions {
		fmt.Fprintf(&sb, "D%d  %s\n", i+1, d)
	}
	var shown []string
	for i, s := range r.Summarizers {
		for d := range r.Decisions {
			if c := r.Cells[i][d]; c.Counterexample != nil {
				p := c.Counterexample
				shown = append(shown, fmt.Sprintf("%s, D%d: [%s] and [%s] give %q and %q", s, d+1, Describe(p.A), Describe(p.B), p.AnsA, p.AnsB))
			}
		}
	}
	sort.Strings(shown)
	sb.WriteString("\nshortest counterexamples found, for summaries other than \"every record\":\n")
	// Keep the output readable: show one counterexample per decision that fails somewhere,
	// for the most informative summary (the per-path index).
	for _, line := range shown {
		if strings.HasPrefix(line, "per-path index") {
			sb.WriteString("  " + line + "\n")
		}
	}
	return sb.String()
}
