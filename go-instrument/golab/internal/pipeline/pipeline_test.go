package pipeline

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func rec(id, author, scope, body string, refs ...string) Record {
	return Record{ID: id, Author: author, Scope: scope, Body: body, Refs: refs}
}

func inscribeAll(rs ...Record) []Inscribed {
	out := make([]Inscribed, len(rs))
	for i, r := range rs {
		out[i] = Inscribe(r)
	}
	return out
}

func TestInscriptionIgnoresContentQuality(t *testing.T) {
	bad := rec("", "", "", "")
	in := Inscribe(bad)
	if in.Receipt() == "" {
		t.Fatal("even a malformed record receives a receipt")
	}
	if Inscribe(bad).Receipt() != in.Receipt() {
		t.Fatal("receipts must be deterministic")
	}
}

func TestInscribeCopiesRefs(t *testing.T) {
	refs := []string{"a"}
	in := Inscribe(rec("b", "x", "s", "t", refs...))
	refs[0] = "changed"
	if in.Record().Refs[0] != "a" {
		t.Fatal("inscription shares memory with the caller")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		set  []Inscribed
		err  error
	}{
		{"ok", inscribeAll(rec("a", "x", "s", "t"), rec("b", "x", "s", "t2", "a")), nil},
		{"empty id", inscribeAll(rec("", "x", "s", "t")), ErrInconsistent},
		{"conflicting id", inscribeAll(rec("a", "x", "s", "t"), rec("a", "x", "s", "other")), ErrInconsistent},
		{"repeat is idempotent", inscribeAll(rec("a", "x", "s", "t"), rec("a", "x", "s", "t")), nil},
		{"dangling", inscribeAll(rec("a", "x", "s", "t", "zzz")), ErrDangling},
		{"self cycle", inscribeAll(rec("a", "x", "s", "t", "a")), ErrCycle},
		{"two cycle", inscribeAll(rec("a", "x", "s", "t", "b"), rec("b", "x", "s", "u", "a")), ErrCycle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(c.set)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
		})
	}
}

func sample() Valid {
	v, err := Validate(inscribeAll(
		rec("r1", "ann", "lab/a", "claim one"),
		rec("r2", "bob", "lab/a", "claim two", "r1"),
		rec("r3", "bob", "lab/b", "claim three"),
		rec("r4", "ann", "lab/a", "claim one"), // repeats r1
		rec("r5", "", "lab/a", "no author"),
		rec("r6", "cy", "lab/a", "needs REVIEW before use"),
	))
	if err != nil {
		panic(err)
	}
	return v
}

func dispositions(a Admitted) map[string]Disposition {
	m := map[string]Disposition{}
	for _, j := range a.Judgments() {
		m[j.Record.ID] = j.Disposition
	}
	return m
}

func TestAdmitDispositions(t *testing.T) {
	p := AllOf(DenyAuthors("bob"), RequireScope("lab/a"), GateContaining("REVIEW"))
	got := dispositions(Admit(sample(), p))
	want := map[string]Disposition{
		"r1": DispAdmitted,
		"r2": DispRefused,
		"r3": DispRefused, // refused outranks unscoped
		"r4": DispDuplicate,
		"r5": DispMalformed,
		"r6": DispGated,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestPolicyDoesNotAffectInscriptionOrValidity(t *testing.T) {
	rs := []Record{rec("a", "x", "s", "t"), rec("b", "y", "s", "u", "a")}
	policies := []Policy{
		AllOf(),
		DenyAuthors("x", "y"),
		PolicyFunc(func(Record) Verdict { return Quarantine }),
	}
	var receipts [][]Receipt
	var lens []int
	for _, p := range policies {
		set := inscribeAll(rs...)
		v, err := Validate(set)
		if err != nil {
			t.Fatal(err)
		}
		_ = Admit(v, p) // policy enters only here
		var rr []Receipt
		for _, in := range set {
			rr = append(rr, in.Receipt())
		}
		receipts = append(receipts, rr)
		lens = append(lens, v.Len())
	}
	for i := 1; i < len(policies); i++ {
		if !reflect.DeepEqual(receipts[0], receipts[i]) || lens[0] != lens[i] {
			t.Fatalf("policy %d changed receipts or validity", i)
		}
	}
}

func TestUnknownVerdictFailsClosed(t *testing.T) {
	for _, bad := range []Verdict{99, -1} {
		raw := PolicyFunc(func(Record) Verdict { return bad })
		for name, p := range map[string]Policy{"direct": raw, "combined": AllOf(raw, AllOf())} {
			a := Admit(sample(), p)
			if a.Counts()[DispAdmitted] != 0 {
				t.Fatalf("verdict %d (%s) admitted something: %v", bad, name, a.Counts())
			}
		}
	}
}

func TestAllOfIsOrderIndependent(t *testing.T) {
	a, b, c := DenyAuthors("bob"), RequireScope("lab/a"), GateContaining("REVIEW")
	orders := [][]Policy{{a, b, c}, {c, b, a}, {b, a, c}, {a, a, b, c}}
	var first map[string]Disposition
	for i, ps := range orders {
		got := dispositions(Admit(sample(), AllOf(ps...)))
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(first, got) {
			t.Fatalf("order %d changed the outcome", i)
		}
	}
}

func TestAddingAPolicyNeverAdmitsMore(t *testing.T) {
	base := AllOf(RequireScope("lab/"))
	more := AllOf(RequireScope("lab/"), DenyAuthors("ann"), GateContaining("claim two"))
	nb := Admit(sample(), base).Counts()[DispAdmitted]
	nm := Admit(sample(), more).Counts()[DispAdmitted]
	if nm > nb {
		t.Fatalf("adding policies admitted more: %d > %d", nm, nb)
	}
}

func TestActivateDetectsOrphans(t *testing.T) {
	// r2 refers to r1; project only a scope that contains r2 but not r1.
	v, err := Validate(inscribeAll(
		rec("r1", "ann", "lab/a", "base"),
		rec("r2", "ann", "lab/b", "built on base", "r1"),
	))
	if err != nil {
		t.Fatal(err)
	}
	a := Admit(v, AllOf())
	if _, err := Activate(Project(a, "lab/b")); !errors.Is(err, ErrOrphaned) {
		t.Fatalf("want ErrOrphaned, got %v", err)
	}
	act, err := Activate(Project(a, "lab/"))
	if err != nil || len(act.Records()) != 2 {
		t.Fatalf("err=%v records=%v", err, act.Records())
	}
}

func TestLoadJSONL(t *testing.T) {
	in := `{"id":"a","author":"x","scope":"s","body":"t"}` + "\n\n" +
		`{"id":"b","author":"y","scope":"s","body":"u","refs":["a"]}` + "\n"
	got, err := LoadJSONL(strings.NewReader(in))
	if err != nil || len(got) != 2 || got[1].Refs[0] != "a" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if _, err := LoadJSONL(strings.NewReader("{bad")); err == nil {
		t.Fatal("malformed JSON must be an error")
	}
}
