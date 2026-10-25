// Package manifest records what an experiment ran on: the digests of its
// input files, its parameters, and the build of the program that ran it. Two
// runs with equal manifests used the same inputs and the same build, which is
// a precondition for comparing their results and does not by itself make the
// results equal.
//
// A manifest contains no clock reading and no host name. The time of a run is
// recorded by the ledger entry that refers to the manifest, so the manifest
// is a function of its inputs and the same inputs always give the same bytes.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"sort"

	"golab/internal/artifact"
)

// Schema is the version of the manifest layout.
const Schema = 1

// Input is one input file, named relative to the input directory.
type Input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest describes one experiment run.
type Manifest struct {
	Schema   int               `json:"schema"`
	Name     string            `json:"name"`
	Params   map[string]string `json:"params"`
	Go       string            `json:"go"`
	Module   string            `json:"module"`
	Settings map[string]string `json:"settings"`
	Inputs   []Input           `json:"inputs"`
}

// buildInfo reads the facts the Go toolchain embedded in the running binary.
// It reports unknown values as empty strings when the binary carries none.
func buildInfo() (goVersion, module string, settings map[string]string) {
	settings = map[string]string{}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", "", settings
	}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	return info.GoVersion, info.Main.Path, settings
}

// Collect builds the manifest of an experiment whose inputs are the files
// under dir. An empty dir means the experiment has no input files.
func Collect(name string, params map[string]string, dir string) (Manifest, error) {
	goVersion, module, settings := buildInfo()
	m := Manifest{
		Schema:   Schema,
		Name:     name,
		Params:   copyMap(params),
		Go:       goVersion,
		Module:   module,
		Settings: settings,
		Inputs:   []Input{},
	}
	if dir == "" {
		return m, nil
	}
	found, err := artifact.Discover(dir)
	if err != nil {
		return Manifest{}, err
	}
	for _, d := range found {
		v, err := artifact.Verify(d)
		if err != nil {
			return Manifest{}, fmt.Errorf("input %s: %w", d.Path, err)
		}
		rel, err := filepath.Rel(dir, d.Path)
		if err != nil {
			return Manifest{}, err
		}
		m.Inputs = append(m.Inputs, Input{Path: filepath.ToSlash(rel), SHA256: v.SHA256(), Size: v.Size()})
	}
	sort.Slice(m.Inputs, func(i, j int) bool { return m.Inputs[i].Path < m.Inputs[j].Path })
	return m, nil
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Encode returns the canonical bytes of the manifest. Maps are written with
// sorted keys and the inputs are already sorted, so equal manifests have
// equal encodings.
func (m Manifest) Encode() ([]byte, error) { return json.MarshalIndent(m, "", " ") }

// Digest is the SHA-256 of the canonical encoding.
func (m Manifest) Digest() (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Decode reads a manifest and rejects other schema versions.
func Decode(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, err
	}
	if m.Schema != Schema {
		return Manifest{}, fmt.Errorf("manifest schema %d, want %d", m.Schema, Schema)
	}
	return m, nil
}

// Differences lists, in a fixed order, how b departs from a. An empty result
// means the manifests are equal.
func Differences(a, b Manifest) []string {
	var out []string
	if a.Name != b.Name {
		out = append(out, fmt.Sprintf("name: %q -> %q", a.Name, b.Name))
	}
	if a.Go != b.Go {
		out = append(out, fmt.Sprintf("go: %s -> %s", a.Go, b.Go))
	}
	if a.Module != b.Module {
		out = append(out, fmt.Sprintf("module: %s -> %s", a.Module, b.Module))
	}
	out = append(out, diffMaps("param", a.Params, b.Params)...)
	out = append(out, diffMaps("setting", a.Settings, b.Settings)...)

	ai, bi := map[string]Input{}, map[string]Input{}
	paths := map[string]bool{}
	for _, x := range a.Inputs {
		ai[x.Path], paths[x.Path] = x, true
	}
	for _, x := range b.Inputs {
		bi[x.Path], paths[x.Path] = x, true
	}
	var names []string
	for p := range paths {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		x, inA := ai[p]
		y, inB := bi[p]
		switch {
		case inA && !inB:
			out = append(out, "input removed: "+p)
		case !inA && inB:
			out = append(out, "input added: "+p)
		case x != y:
			out = append(out, "input changed: "+p)
		}
	}
	return out
}

func diffMaps(kind string, a, b map[string]string) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		av, inA := a[k]
		bv, inB := b[k]
		switch {
		case inA && !inB:
			out = append(out, fmt.Sprintf("%s removed: %s", kind, k))
		case !inA && inB:
			out = append(out, fmt.Sprintf("%s added: %s", kind, k))
		case av != bv:
			out = append(out, fmt.Sprintf("%s %s: %q -> %q", kind, k, av, bv))
		}
	}
	return out
}
