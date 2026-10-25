package main

import (
	"fmt"
	"sync"
)

func main() {
	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter++ // unsynchronized read-modify-write from many goroutines
		}()
	}
	wg.Wait()
	fmt.Println("counter:", counter)
}
