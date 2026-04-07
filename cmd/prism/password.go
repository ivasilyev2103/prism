package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// readPassword reads the master password from env var or terminal prompt.
func readPassword(prompt string) ([]byte, error) {
	// 1. Check environment variable.
	if env := os.Getenv("PRISM_MASTER_PASSWORD"); env != "" {
		return []byte(env), nil
	}

	// 2. Try terminal prompt with hidden input.
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr) // newline after hidden input
		if err != nil {
			return nil, fmt.Errorf("read password: %w", err)
		}
		return pw, nil
	}

	// 3. Fallback: read from stdin (piped input).
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return []byte(strings.TrimRight(scanner.Text(), "\r\n")), nil
	}
	return nil, fmt.Errorf("read password: no input")
}

// readPasswordConfirmed reads and confirms the master password.
func readPasswordConfirmed() ([]byte, error) {
	// If from env, no confirmation needed.
	if env := os.Getenv("PRISM_MASTER_PASSWORD"); env != "" {
		return []byte(env), nil
	}

	pw1, err := readPassword("Enter master password: ")
	if err != nil {
		return nil, err
	}
	if len(pw1) < 8 {
		return nil, fmt.Errorf("master password must be at least 8 characters")
	}

	pw2, err := readPassword("Confirm master password: ")
	if err != nil {
		explicitBzero(pw1)
		return nil, err
	}

	if string(pw1) != string(pw2) {
		explicitBzero(pw1)
		explicitBzero(pw2)
		return nil, fmt.Errorf("passwords do not match")
	}
	explicitBzero(pw2)
	return pw1, nil
}

// explicitBzero zeroes a byte slice.
func explicitBzero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
