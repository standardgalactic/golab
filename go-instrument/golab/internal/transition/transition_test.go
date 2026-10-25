package transition

import (
	"errors"
	"math"
	"testing"
)

func TestIncrementLeavesOriginalUnchanged(t *testing.T) {
	current := State{Counter: 0}
	next := Increment(current)
	if current.Counter != 0 || next.Counter != 1 {
		t.Fatalf("got current=%d next=%d", current.Counter, next.Counter)
	}
}

func TestDecrementRejectsNegative(t *testing.T) {
	_, err := Decrement(State{})
	if !errors.Is(err, ErrNegative) {
		t.Fatalf("want ErrNegative, got %v", err)
	}
}

func TestAdd(t *testing.T) {
	cases := []struct {
		name  string
		start int
		delta int
		want  int
		err   error
	}{
		{"plain", 10, 5, 15, nil},
		{"to the maximum", 990, 10, 1000, nil},
		{"past the maximum", 990, 11, 990, ErrOverflow},
		{"below zero", 3, -4, 3, ErrNegative},
		{"to zero", 3, -3, 0, nil},
		{"huge positive", 1, math.MaxInt, 1, ErrOverflow},
		{"huge negative", 1, math.MinInt, 1, ErrNegative},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Add(State{Counter: c.start}, c.delta)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if got.Counter != c.want {
				t.Fatalf("counter = %d, want %d", got.Counter, c.want)
			}
		})
	}
}
