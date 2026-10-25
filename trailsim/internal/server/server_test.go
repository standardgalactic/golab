package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trailsim/internal/runner"
	"trailsim/internal/world"
)

func setup(t *testing.T) (*httptest.Server, *runner.Runner) {
	t.Helper()
	c := world.Default()
	c.W, c.H, c.Ants = 48, 32, 30
	w, err := world.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := runner.New(w, 2*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	for r.Frame() == nil {
		time.Sleep(time.Millisecond)
	}
	ts := httptest.NewServer(New(r, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(func() { ts.Close(); cancel() })
	return ts, r
}

func post(t *testing.T, ts *httptest.Server, path, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestIndexPage(t *testing.T) {
	ts, _ := setup(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "<canvas") {
		t.Fatalf("status %d, body does not look like the page", resp.StatusCode)
	}
}

func TestFrameEndpoint(t *testing.T) {
	ts, _ := setup(t)
	resp, err := http.Get(ts.URL + "/api/frame")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var f world.Frame
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.W != 48 || f.H != 32 || len(f.Walls) != 48*32 || len(f.Ants) != 90 {
		t.Fatalf("unexpected frame: w=%d h=%d walls=%d ants=%d", f.W, f.H, len(f.Walls), len(f.Ants))
	}
}

func TestStreamDeliversChangingFrames(t *testing.T) {
	ts, _ := setup(t)
	resp, err := http.Get(ts.URL + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	var versions []uint64
	for sc.Scan() && len(versions) < 3 {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var f world.Frame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, f.Version)
	}
	if len(versions) < 3 || !(versions[0] < versions[1] && versions[1] < versions[2]) {
		t.Fatalf("versions did not increase: %v", versions)
	}
}

func TestCommands(t *testing.T) {
	ts, r := setup(t)
	if code, _ := post(t, ts, "/api/pause", `{"paused":true}`); code != 200 {
		t.Fatalf("pause: %d", code)
	}
	if !r.Frame().Paused {
		t.Fatal("not paused")
	}
	if code, _ := post(t, ts, "/api/speed", `{"speed":7}`); code != 200 || r.Frame().Speed != 7 {
		t.Fatal("speed not applied")
	}
	if code, _ := post(t, ts, "/api/paint", `{"kind":"wall","x":10,"y":10,"radius":2}`); code != 200 {
		t.Fatal("paint failed")
	}
	if f := r.Frame(); f.Walls[10*f.W+10] != world.Wall {
		t.Fatal("wall missing")
	}
	if code, _ := post(t, ts, "/api/reset", `{"ants":12,"seed":9,"trails":false}`); code != 200 {
		t.Fatal("reset failed")
	}
	if r.Frame().Stats.Ants != 12 || r.Config().Seed != 9 || r.Config().Trails {
		t.Fatal("reset did not apply the fields")
	}
	if r.Config().W != 48 {
		t.Fatal("reset lost the unspecified fields")
	}
}

func TestBadRequests(t *testing.T) {
	ts, _ := setup(t)
	cases := []struct {
		path, body string
		want       int
	}{
		{"/api/pause", `not json`, 400},
		{"/api/pause", `{"paused":true,"extra":1}`, 400},
		{"/api/paint", `{"kind":"lava","x":1,"y":1,"radius":1}`, 400},
		{"/api/paint", `{"kind":"wall","x":999,"y":1,"radius":1}`, 400},
		{"/api/reset", `{"ants":-3}`, 400},
		{"/api/pause", `{"paused":true,"pad":"` + strings.Repeat("x", 10000) + `"}`, 413},
	}
	for _, c := range cases {
		if code, body := post(t, ts, c.path, c.body); code != c.want {
			t.Errorf("%s %.30q: status %d, want %d (%s)", c.path, c.body, code, c.want, body)
		}
	}
}

func TestWrongMethod(t *testing.T) {
	ts, _ := setup(t)
	resp, err := http.Get(ts.URL + "/api/pause")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", resp.StatusCode)
	}
}

func TestServeShutsDown(t *testing.T) {
	ts, r := setup(t)
	_ = ts
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, "127.0.0.1:0", New(r, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
}
