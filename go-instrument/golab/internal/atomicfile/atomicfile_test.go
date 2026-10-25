package atomicfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := WriteBytes(p, 0o640, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteBytes(p, 0o640, []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "two" {
		t.Fatalf("got %q", got)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	if n := names(t, dir); len(n) != 1 {
		t.Fatalf("leftover files: %v", n)
	}
}

func TestFailedFillLeavesOldFileAndNoTemporary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := WriteBytes(p, 0o644, []byte("old contents")); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := Write(p, 0o644, func(w io.Writer) error {
		w.Write(bytes.Repeat([]byte("partial"), 100000)) // more than the buffer holds
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "old contents" {
		t.Fatalf("old file damaged: %d bytes", len(got))
	}
	if n := names(t, dir); len(n) != 1 {
		t.Fatalf("leftover files: %v", n)
	}
}

func TestMissingDirectoryIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope", "x")
	if err := WriteBytes(p, 0o644, []byte("a")); err == nil {
		t.Fatal("expected an error")
	}
}

// A reader that runs while the file is being replaced must see one whole
// version or the other, never a mixture or a prefix.
func TestReadersNeverSeeAPartialFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	a := bytes.Repeat([]byte("a"), 1<<20)
	b := bytes.Repeat([]byte("b"), 1<<20)
	if err := WriteBytes(p, 0o644, a); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	bad := make(chan string, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := os.ReadFile(p)
			if err != nil {
				select {
				case bad <- err.Error():
				default:
				}
				return
			}
			if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
				select {
				case bad <- "mixed or truncated contents":
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 40; i++ {
		data := b
		if i%2 == 1 {
			data = a
		}
		if err := WriteBytes(p, 0o644, data); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case msg := <-bad:
		t.Fatal(msg)
	default:
	}
}
