package pool

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"golab/internal/artifact"
)

// BenchmarkVerifyAll hashes 64 files of one mebibyte each with different
// numbers of workers. The files fit in the operating system's cache after
// the first pass, so the benchmark measures hashing and scheduling and says
// little about a cold disk.
func BenchmarkVerifyAll(b *testing.B) {
	dir := b.TempDir()
	rng := rand.New(rand.NewSource(1))
	buf := make([]byte, 1<<20)
	for i := 0; i < 64; i++ {
		rng.Read(buf)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.dat", i)), buf, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	files, err := artifact.Discover(dir)
	if err != nil || len(files) != 64 {
		b.Fatalf("discover: %v (%d files)", err, len(files))
	}
	for _, w := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			b.SetBytes(64 << 20)
			for i := 0; i < b.N; i++ {
				for _, o := range VerifyAll(context.Background(), files, Options{Workers: w}) {
					if o.Err != nil {
						b.Fatal(o.Err)
					}
				}
			}
		})
	}
}
