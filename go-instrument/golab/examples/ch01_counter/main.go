package main

import "fmt"

type State struct {
	Counter int
}

func Increment(s State) State {
	s.Counter++
	return s
}

func main() {
	current := State{Counter: 0}
	next := Increment(current)

	fmt.Println(current.Counter)
	fmt.Println(next.Counter)
}
