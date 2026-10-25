package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golab/internal/artifact"
)

func files(n int) []artifact.Discovered {
	out := make([]artifact.Discovered, n)
	for i := range out {
		out[i] = artifact.Discovered{Path: fmt.Sprintf("f%02d", i), Kind: artifact.KindSource, Size: int64(i)}
	}
	return out
}

func okVerify(d artifact.Discovered) (artifact.Verified, error) {
	return artifact.Verified{}, nil
}

func TestResultsAreInInputOrderAndComplete(t *testing.T) {
	in := files(100)
	out := VerifyAll(context.Background(), in, Options{Workers: 8, Verify: okVerify})
	if len(out) != len(in) {
		t.Fatalf("got %d outcomes for %d files", len(out), len(in))
	}
	for i, o := range out {
		if o.File.Path != in[i].Path {
			t.Fatalf("outcome %d is for %s, want %s", i, o.File.Path, in[i].Path)
		}
	}
}

func TestWorkersReallyRunInParallel(t *testing.T) {
	const workers = 4
	arrived := make(chan struct{}, workers)
	release := make(chan struct{})
	verify := func(d artifact.Discovered) (artifact.Verified, error) {
		arrived <- struct{}{}
		<-release
		return artifact.Verified{}, nil
	}
	done := make(chan []Outcome)
	go func() { done <- VerifyAll(context.Background(), files(8), Options{Workers: workers, Verify: verify}) }()

	for i := 0; i < workers; i++ {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d workers were running at once, want %d", i, workers)
		}
	}
	close(release)
	if got := <-done; len(got) != 8 {
		t.Fatalf("got %d outcomes", len(got))
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	const workers = 3
	var running, peak atomic.Int32
	verify := func(d artifact.Discovered) (artifact.Verified, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		running.Add(-1)
		return artifact.Verified{}, nil
	}
	VerifyAll(context.Background(), files(60), Options{Workers: workers, Verify: verify})
	if p := peak.Load(); p > workers {
		t.Fatalf("peak concurrency %d exceeds %d workers", p, workers)
	}
}

func TestCancelledContextStillReportsEveryFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	verify := func(d artifact.Discovered) (artifact.Verified, error) {
		calls.Add(1)
		return artifact.Verified{}, nil
	}
	out := VerifyAll(ctx, files(20), Options{Workers: 4, Verify: verify})
	if len(out) != 20 {
		t.Fatalf("got %d outcomes", len(out))
	}
	for i, o := range out {
		if !errors.Is(o.Err, context.Canceled) {
			t.Fatalf("outcome %d: err = %v, want context.Canceled", i, o.Err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("verify was called %d times after cancellation", calls.Load())
	}
}

func TestCancellationMidRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	verify := func(d artifact.Discovered) (artifact.Verified, error) {
		once.Do(cancel) // the first file cancels everything
		return artifact.Verified{}, nil
	}
	out := VerifyAll(ctx, files(50), Options{Workers: 1, Verify: verify})
	var ok, cancelled int
	for _, o := range out {
		switch {
		case o.Err == nil:
			ok++
		case errors.Is(o.Err, context.Canceled):
			cancelled++
		default:
			t.Fatalf("unexpected error %v", o.Err)
		}
	}
	if ok != 1 || cancelled != 49 {
		t.Fatalf("ok=%d cancelled=%d, want 1 and 49", ok, cancelled)
	}
}

func TestOnResultIsCalledOncePerFile(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	opts := Options{Workers: 6, Verify: okVerify, OnResult: func(o Outcome) {
		mu.Lock()
		seen[o.File.Path]++
		mu.Unlock()
	}}
	VerifyAll(context.Background(), files(200), opts)
	if len(seen) != 200 {
		t.Fatalf("callbacks for %d files, want 200", len(seen))
	}
	for p, n := range seen {
		if n != 1 {
			t.Fatalf("%s reported %d times", p, n)
		}
	}
}

func TestVerifiesRealFilesConcurrently(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		writeFile(t, dir, fmt.Sprintf("n%02d.txt", i), fmt.Sprintf("content %d", i))
	}
	found, err := artifact.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := VerifyAll(context.Background(), found, Options{Workers: 5})
	for _, o := range out {
		if o.Err != nil || o.Verified.IsZero() {
			t.Fatalf("%s: %v", o.File.Path, o.Err)
		}
	}
}
