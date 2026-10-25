package runner

import (
	"strings"
	"testing"
	"time"
)

func TestSucceeded(t *testing.T) {
	res, err := Run(".", 5*time.Second, "sh", "-c", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Succeeded || strings.TrimSpace(res.Stdout) != "hello" {
		t.Fatalf("%+v", res)
	}
}

func TestFailedKeepsExitCode(t *testing.T) {
	res, err := Run(".", 5*time.Second, "sh", "-c", "echo oops >&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Failed || res.ExitCode != 3 || !strings.Contains(res.Stderr, "oops") {
		t.Fatalf("%+v", res)
	}
}

func TestTimedOut(t *testing.T) {
	res, err := Run(".", 100*time.Millisecond, "sh", "-c", "sleep 5")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != TimedOut {
		t.Fatalf("%+v", res)
	}
}

func TestDidNotStart(t *testing.T) {
	res, err := Run(".", 5*time.Second, "definitely-not-a-program-golab")
	if err == nil || res.Outcome != NotStated {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
