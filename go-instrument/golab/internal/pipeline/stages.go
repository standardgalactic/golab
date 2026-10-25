package pipeline

import (
	"errors"
	"fmt"
	"strings"
)

// Errors reported by validation and activation.
var (
	ErrInconsistent = errors.New("inconsistent identifiers")
	ErrDangling     = errors.New("reference to an unknown record")
	ErrCycle        = errors.New("reference cycle")
	ErrOrphaned     = errors.New("active record refers to an inactive record")
)

// Valid is a set of inscribed records with consistent identifiers and
// acyclic, closed references. Only Validate can produce a non-zero Valid.
type Valid struct {
	items []Inscribed // sorted by ID
}

// Validate checks the structure of a set of inscriptions. Its signature
// has no policy parameter, so validity cannot depend on policy.
func Validate(set []Inscribed) (Valid, error) {
	byID := make(map[string]Inscribed, len(set))
	for _, in := range set {
		id := in.rec.ID
		if id == "" {
			return Valid{}, fmt.Errorf("record with empty id: %w", ErrInconsistent)
		}
		if prev, ok := byID[id]; ok {
			if prev.receipt != in.receipt {
				return Valid{}, fmt.Errorf("id %q inscribed with two different contents: %w", id, ErrInconsistent)
			}
			continue // the same inscription repeated is idempotent
		}
		byID[id] = in
	}
	ids := sortedIDs(byID)
	for _, id := range ids {
		for _, ref := range byID[id].rec.Refs {
			if _, ok := byID[ref]; !ok {
				return Valid{}, fmt.Errorf("%q refers to %q: %w", id, ref, ErrDangling)
			}
		}
	}
	const (
		white = iota
		grey
		black
	)
	colour := make(map[string]int, len(ids))
	var visit func(id string) error
	visit = func(id string) error {
		colour[id] = grey
		for _, ref := range byID[id].rec.Refs {
			switch colour[ref] {
			case grey:
				return fmt.Errorf("%q and %q: %w", id, ref, ErrCycle)
			case white:
				if err := visit(ref); err != nil {
					return err
				}
			}
		}
		colour[id] = black
		return nil
	}
	for _, id := range ids {
		if colour[id] == white {
			if err := visit(id); err != nil {
				return Valid{}, err
			}
		}
	}
	items := make([]Inscribed, 0, len(ids))
	for _, id := range ids {
		items = append(items, byID[id])
	}
	return Valid{items: items}, nil
}

// Len reports how many distinct records the set holds.
func (v Valid) Len() int { return len(v.items) }

// Disposition is the outcome of admission for one record.
type Disposition string

const (
	DispAdmitted    Disposition = "admitted"
	DispDuplicate   Disposition = "duplicate"
	DispMalformed   Disposition = "malformed"
	DispUnscoped    Disposition = "unscoped"
	DispQuarantined Disposition = "quarantined"
	DispGated       Disposition = "gated"
	DispRefused     Disposition = "refused"
)

// Judgment pairs a record with the disposition it received.
type Judgment struct {
	Record      Record
	Receipt     Receipt
	Disposition Disposition
}

// Admitted is the outcome of running a Valid set through a policy.
type Admitted struct {
	judgments []Judgment
}

// Judgments returns a copy of the judgments in identifier order.
func (a Admitted) Judgments() []Judgment {
	out := make([]Judgment, len(a.judgments))
	for i, j := range a.judgments {
		j.Record = copyRecord(j.Record)
		out[i] = j
	}
	return out
}

// Counts tallies dispositions.
func (a Admitted) Counts() map[Disposition]int {
	m := make(map[Disposition]int)
	for _, j := range a.judgments {
		m[j.Disposition]++
	}
	return m
}

// Admit applies the policy to every record of a valid set. Two checks are
// made by the pipeline itself and are not left to policy: a record without
// an author or body is malformed, and a record whose author, scope and
// body repeat those of an earlier record (in identifier order) is a
// duplicate. A policy can return only a Verdict, which has no way to
// express either of those dispositions.
func Admit(v Valid, p Policy) Admitted {
	seen := make(map[string]string)
	out := make([]Judgment, 0, len(v.items))
	for _, in := range v.items {
		r := in.rec
		j := Judgment{Record: copyRecord(r), Receipt: in.receipt}
		key := r.Author + "\x00" + r.Scope + "\x00" + r.Body
		switch {
		case r.Author == "" || r.Body == "":
			j.Disposition = DispMalformed
		case seen[key] != "":
			j.Disposition = DispDuplicate
		default:
			seen[key] = r.ID
			j.Disposition = p.Decide(copyRecord(r)).disposition()
		}
		out = append(out, j)
	}
	return Admitted{judgments: out}
}

// Projection is the set of admitted records visible on one surface.
type Projection struct {
	scope string
	items []Record
}

// Project selects the admitted records whose scope begins with scope.
func Project(a Admitted, scope string) Projection {
	p := Projection{scope: scope}
	for _, j := range a.judgments {
		if j.Disposition == DispAdmitted && strings.HasPrefix(j.Record.Scope, scope) {
			p.items = append(p.items, copyRecord(j.Record))
		}
	}
	return p
}

// Active is a projection that has been made live.
type Active struct {
	scope string
	items []Record
}

// Scope returns the surface on which the records are active.
func (a Active) Scope() string { return a.scope }

// Records returns a copy of the active records.
func (a Active) Records() []Record {
	out := make([]Record, len(a.items))
	for i, r := range a.items {
		out[i] = copyRecord(r)
	}
	return out
}

// Activate makes a projection live, provided that no active record refers
// to a record that the projection left out. Projection can cut references
// that validation found intact, so this check cannot be done earlier.
func Activate(p Projection) (Active, error) {
	present := make(map[string]bool, len(p.items))
	for _, r := range p.items {
		present[r.ID] = true
	}
	for _, r := range p.items {
		for _, ref := range r.Refs {
			if !present[ref] {
				return Active{}, fmt.Errorf("%q refers to %q, which is not in scope %q: %w", r.ID, ref, p.scope, ErrOrphaned)
			}
		}
	}
	return Active{scope: p.scope, items: p.items}, nil
}
