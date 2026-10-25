package artifact

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverClassifiesAndSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "paper.tex", "x")
	write(t, dir, "paper.pdf", "x")
	write(t, dir, "paper.summary.md", "x")
	write(t, dir, ".git/config", "x")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("found %d files, want 3: %+v", len(got), got)
	}
	want := map[string]Kind{
		"paper.pdf":        KindOutput,
		"paper.summary.md": KindSummary,
		"paper.tex":        KindSource,
	}
	for _, d := range got {
		if want[filepath.Base(d.Path)] != d.Kind {
			t.Errorf("%s classified %s", d.Path, d.Kind)
		}
	}
}

func TestVerifyKnownDigest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "abc")
	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Verify(found[0])
	if err != nil {
		t.Fatal(err)
	}
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if v.SHA256() != abc || v.IsZero() {
		t.Fatalf("digest %s", v.SHA256())
	}
}

func TestZeroVerifiedCarriesNoEvidence(t *testing.T) {
	var v Verified
	if !v.IsZero() || v.SHA256() != "" {
		t.Fatalf("zero value should carry no digest: %+v", v)
	}
}

func TestVerifyMissingFileKeepsItsCause(t *testing.T) {
	_, err := Verify(Discovered{Path: filepath.Join(t.TempDir(), "gone.txt")})
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the cause was lost in wrapping: %v", err)
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe.Op != "open" {
		t.Fatalf("want a *fs.PathError for the open call, got %v", err)
	}
}
