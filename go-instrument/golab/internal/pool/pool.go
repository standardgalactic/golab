// Package pool verifies many files at once with a bounded number of
// goroutines. The result for every input file is always present and is
// returned in input order, whatever order the workers happened to finish in.
package pool

import (
	"context"
	"sync"

	"golab/internal/artifact"
)

// Outcome is what happened to one file. Exactly one of Verified and Err is
// meaningful: a nil Err means Verified holds the digest.
type Outcome struct {
	File     artifact.Discovered
	Verified artifact.Verified
	Err      error
}

// Options configures VerifyAll. The zero value is usable and verifies with
// one worker using artifact.Verify.
type Options struct {
	// Workers is the number of goroutines. Values below one mean one.
	Workers int
	// Verify performs the work for one file. It defaults to artifact.Verify
	// and is a field so that tests can substitute a controllable function.
	Verify func(artifact.Discovered) (artifact.Verified, error)
	// OnResult, if set, is called once per file as soon as its outcome is
	// known. It is called from the worker goroutines, so it must be safe for
	// concurrent use, and calls for different files may overlap and may
	// arrive in any order.
	OnResult func(Outcome)
}

// VerifyAll verifies every file, using at most opts.Workers goroutines at
// a time, and returns one Outcome per input file in input order.
//
// If ctx is cancelled, files that no worker has started are not verified
// and receive ctx.Err() as their error, so the caller still learns about
// every file. A verification already in progress is not interrupted, since
// artifact.Verify takes no context.
func VerifyAll(ctx context.Context, files []artifact.Discovered, opts Options) []Outcome {
	workers := opts.Workers
	if workers < 1 {
		workers = 1
	}
	verify := opts.Verify
	if verify == nil {
		verify = artifact.Verify
	}

	results := make([]Outcome, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				// Each index is received by exactly one worker, so each
				// element of results is written by exactly one goroutine.
				out := Outcome{File: files[i]}
				if err := ctx.Err(); err != nil {
					out.Err = err
				} else {
					out.Verified, out.Err = verify(files[i])
				}
				results[i] = out
				if opts.OnResult != nil {
					opts.OnResult(out)
				}
			}
		}()
	}

	// Feed every index, even after cancellation: the workers record the
	// cancellation as each file's outcome, and no file is silently dropped.
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}
