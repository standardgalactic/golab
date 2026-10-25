package service

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// Principal is who a request says it comes from, once the credential has
// been checked. It carries what the adjudicator needs and nothing else.
type Principal struct {
	Name     string
	CanWrite bool
}

// Tokens maps bearer tokens to principals.
type Tokens struct {
	entries []tokenEntry
}

type tokenEntry struct {
	token string
	p     Principal
}

// ParseTokens reads a comma-separated list of name=token:mode entries, where
// mode is "r" or "rw". Empty and duplicate tokens are rejected, because a
// token shared by two principals would make the credential ambiguous.
func ParseTokens(s string) (*Tokens, error) {
	t := &Tokens{}
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, rest, ok := strings.Cut(item, "=")
		tok, mode, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 || name == "" || tok == "" || (mode != "r" && mode != "rw") {
			return nil, fmt.Errorf("token entry %q: want name=token:r or name=token:rw", name)
		}
		if seen[tok] {
			return nil, fmt.Errorf("token entry %q: token already assigned", name)
		}
		seen[tok] = true
		t.entries = append(t.entries, tokenEntry{tok, Principal{Name: name, CanWrite: mode == "rw"}})
	}
	if len(t.entries) == 0 {
		return nil, fmt.Errorf("no tokens configured")
	}
	return t, nil
}

// Authenticate extracts a bearer token from the request and looks it up.
// Every configured token is compared, in constant time, whether or not an
// earlier one matched, so that the time taken does not reveal how much of a
// guess was right.
func (t *Tokens) Authenticate(r *http.Request) (Principal, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return Principal{}, false
	}
	got := []byte(h[len(prefix):])
	var found Principal
	ok := false
	for _, e := range t.entries {
		if subtle.ConstantTimeCompare(got, []byte(e.token)) == 1 {
			found, ok = e.p, true
		}
	}
	return found, ok
}

// Need is what a route requires of the caller.
type Need int

const (
	NeedNothing Need = iota // anyone, with or without a credential
	NeedRead                // any authenticated principal
	NeedWrite               // a principal allowed to write
)

// Permit is the adjudication: whether this principal may do what the route
// requires. It is a pure function of its two arguments.
func Permit(p Principal, authenticated bool, n Need) (status int, ok bool) {
	switch n {
	case NeedNothing:
		return http.StatusOK, true
	case NeedRead:
		if !authenticated {
			return http.StatusUnauthorized, false
		}
		return http.StatusOK, true
	case NeedWrite:
		if !authenticated {
			return http.StatusUnauthorized, false
		}
		if !p.CanWrite {
			return http.StatusForbidden, false
		}
		return http.StatusOK, true
	}
	return http.StatusForbidden, false // an undeclared need is refused
}
