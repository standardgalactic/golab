package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func inputs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("beta"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("gamma"), 0o644)
	return dir
}

func TestCollectIsDeterministicAndRelative(t *testing.T) {
	dir := inputs(t)
	p := map[string]string{"n": "6"}
	m1, err := Collect("exp", p, dir)
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := Collect("exp", p, dir)
	d1, _ := m1.Digest()
	d2, _ := m2.Digest()
	if d1 != d2 {
		t.Fatal("two collections of the same inputs differ")
	}
	var paths []string
	for _, in := range m1.Inputs {
		paths = append(paths, in.Path)
	}
	if !reflect.DeepEqual(paths, []string{"a.txt", "b.txt", "sub/c.txt"}) {
		t.Fatalf("paths = %v", paths)
	}
	if m1.Inputs[0].SHA256 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("digest of abc: %s", m1.Inputs[0].SHA256)
	}
	// Moving the same files to another directory does not change the manifest.
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "sub"), 0o755)
	for _, f := range []string{"a.txt", "b.txt", "sub/c.txt"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		os.WriteFile(filepath.Join(other, f), b, 0o644)
	}
	m3, _ := Collect("exp", p, other)
	if d3, _ := m3.Digest(); d3 != d1 {
		t.Fatal("manifest depends on where the inputs live")
	}
}

func TestDifferencesNameWhatChanged(t *testing.T) {
	dir := inputs(t)
	base, _ := Collect("exp", map[string]string{"n": "6"}, dir)

	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("changed"), 0o644)
	os.WriteFile(filepath.Join(dir, "d.txt"), []byte("new"), 0o644)
	os.Remove(filepath.Join(dir, "a.txt"))
	now, _ := Collect("exp", map[string]string{"n": "7", "extra": "x"}, dir)

	got := Differences(base, now)
	want := []string{
		"param added: extra",
		`param n: "6" -> "7"`,
		"input removed: a.txt",
		"input changed: b.txt",
		"input added: d.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("differences:\n%q\nwant\n%q", got, want)
	}
	if len(Differences(base, base)) != 0 {
		t.Fatal("a manifest differs from itself")
	}
}

func TestDecodeRoundTripAndSchema(t *testing.T) {
	m, _ := Collect("exp", nil, "")
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if d1, _ := m.Digest(); func() string { d, _ := back.Digest(); return d }() != d1 {
		t.Fatal("round trip changed the digest")
	}
	if _, err := Decode([]byte(`{"schema":9}`)); err == nil {
		t.Fatal("accepted another schema")
	}
	if m.Go == "" {
		t.Fatal("build information missing")
	}
}
