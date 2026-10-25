package pipeline

import "strings"

// Verdict is what a policy may say about a record. The values are ordered
// by severity, so that the most restrictive of several verdicts is simply
// the largest.
type Verdict int

const (
	Allow Verdict = iota
	Unscoped
	Gate
	Quarantine
	Refuse
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Unscoped:
		return "unscoped"
	case Gate:
		return "gate"
	case Quarantine:
		return "quarantine"
	case Refuse:
		return "refuse"
	}
	return "unknown"
}

// valid reports whether v is one of the declared verdicts.
func (v Verdict) valid() bool { return v >= Allow && v <= Refuse }

func (v Verdict) disposition() Disposition {
	switch v {
	case Allow:
		return DispAdmitted
	case Unscoped:
		return DispUnscoped
	case Gate:
		return DispGated
	case Quarantine:
		return DispQuarantined
	default:
		// Refuse, and any value outside the declared set, fail closed.
		return DispRefused
	}
}

// Policy decides what verdict a record receives.
type Policy interface {
	Decide(Record) Verdict
}

// PolicyFunc adapts an ordinary function to the Policy interface.
type PolicyFunc func(Record) Verdict

// Decide calls f.
func (f PolicyFunc) Decide(r Record) Verdict { return f(r) }

// AllOf combines policies. The most restrictive verdict wins. With no
// policies at all it allows everything, so callers that want a default of
// refusal must say so.
func AllOf(ps ...Policy) Policy {
	return PolicyFunc(func(r Record) Verdict {
		worst := Allow
		for _, p := range ps {
			v := p.Decide(r)
			if !v.valid() {
				v = Refuse // an undeclared verdict fails closed, on either side of the range
			}
			if v > worst {
				worst = v
			}
		}
		return worst
	})
}

// DenyAuthors refuses records written by any of the named authors.
func DenyAuthors(names ...string) Policy {
	denied := make(map[string]bool, len(names))
	for _, n := range names {
		denied[n] = true
	}
	return PolicyFunc(func(r Record) Verdict {
		if denied[r.Author] {
			return Refuse
		}
		return Allow
	})
}

// RequireScope marks records outside the given scope prefix as unscoped.
func RequireScope(prefix string) Policy {
	return PolicyFunc(func(r Record) Verdict {
		if strings.HasPrefix(r.Scope, prefix) {
			return Allow
		}
		return Unscoped
	})
}

// GateContaining holds back records whose body contains the phrase, for
// review by someone outside the pipeline.
func GateContaining(phrase string) Policy {
	return PolicyFunc(func(r Record) Verdict {
		if strings.Contains(r.Body, phrase) {
			return Gate
		}
		return Allow
	})
}
