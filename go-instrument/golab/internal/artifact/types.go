// Package artifact separates files that have merely been found from files
// whose content has been read and hashed. The separation is carried by the
// type system: code that requires evidence of content can ask for a
// Verified value instead of a path.
package artifact

import (
	"path/filepath"
	"strings"
)

// Kind classifies an artifact by role. A summary is deliberately a
// different kind from a source: it is an interpretation of evidence, not
// evidence.
type Kind string

const (
	KindSource  Kind = "source"
	KindOutput  Kind = "output"
	KindSummary Kind = "summary"
	KindOther   Kind = "other"
)

// Discovered is a path that exists. Nothing is claimed about its content.
type Discovered struct {
	Path string
	Kind Kind
	Size int64
}

// Verified is a Discovered file whose bytes were read and hashed. Its
// fields are unexported, so code outside this package cannot assemble one
// with an invented digest. Go always permits the zero value, however, so a
// Verified whose digest is empty must be treated as "nothing verified".
type Verified struct {
	found Discovered
	sum   string
}

// Path returns the location of the verified file.
func (v Verified) Path() string { return v.found.Path }

// Kind returns the classification of the verified file.
func (v Verified) Kind() Kind { return v.found.Kind }

// Size returns the number of bytes that were hashed.
func (v Verified) Size() int64 { return v.found.Size }

// SHA256 returns the hexadecimal digest, or the empty string for the zero
// value.
func (v Verified) SHA256() string { return v.sum }

// IsZero reports whether v is the zero value and therefore carries no
// evidence of any content.
func (v Verified) IsZero() bool { return v.sum == "" }

// Classify assigns a Kind from the file name alone. It is a convention,
// and is documented as one in Chapter 3.
func Classify(path string) Kind {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(base, ".summary.md") || strings.HasSuffix(base, ".summary.txt"):
		return KindSummary
	case strings.HasSuffix(base, ".tex") || strings.HasSuffix(base, ".go") ||
		strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".txt"):
		return KindSource
	case strings.HasSuffix(base, ".pdf") || strings.HasSuffix(base, ".html"):
		return KindOutput
	default:
		return KindOther
	}
}
