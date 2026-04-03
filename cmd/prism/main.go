package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "prism: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Phase 1: skeleton only.
	// Phase 9 will wire up all modules with dependency injection.
	fmt.Println("Prism LocalAI Gateway")
	fmt.Println("Status: skeleton (Phase 1)")
	return nil
}
