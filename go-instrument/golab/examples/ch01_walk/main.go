package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

func main() {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			fmt.Println(path)
		}
		return nil
	})
	if err != nil {
		fmt.Println("Traversal failed:", err)
	}
}
