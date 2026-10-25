package main

import (
	"errors"
	"fmt"
)

type State struct {
	Counter int
}

func Decrement(s State) (State, error) {
	if s.Counter == 0 {
		return s, errors.New("negative state prohibited")
	}

	s.Counter--
	return s, nil
}

func main() {
	current := State{Counter: 0}

	next, err := Decrement(current)
	if err != nil {
		fmt.Println("Rejected:", err)
		return
	}

	fmt.Println(next.Counter)
}
