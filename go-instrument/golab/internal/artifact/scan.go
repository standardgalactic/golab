package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Discover walks root and returns every regular file in lexical order.
// Directories whose names begin with a dot are skipped.
func Discover(root string) ([]Discovered, error) {
	var found []Discovered
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		found = append(found, Discovered{Path: path, Kind: Classify(path), Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", root, err)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found, nil
}

// Verify reads the file and records its SHA-256 digest.
func Verify(d Discovered) (Verified, error) {
	f, err := os.Open(d.Path)
	if err != nil {
		return Verified{}, fmt.Errorf("verify %s: %w", d.Path, err)
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Verified{}, fmt.Errorf("verify %s: %w", d.Path, err)
	}
	if n != d.Size {
		return Verified{}, fmt.Errorf("verify %s: size changed from %d to %d during read", d.Path, d.Size, n)
	}
	return Verified{found: d, sum: hex.EncodeToString(h.Sum(nil))}, nil
}
