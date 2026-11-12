package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nm1996/beacon/internal/auth"
)

// maxTokenInput caps how much of standard input --hash-token reads. Real
// tokens are far shorter; anything longer is almost certainly the wrong
// input, such as a file piped in by mistake.
const maxTokenInput = 4 << 10

// hashToken reads one token from input and writes its SHA-256 hash, as a
// tokens file expects it, to output. A single trailing newline is dropped,
// so both "echo" and a file ending in a newline work. Reading from input
// rather than an argument keeps the token out of shell history and process
// listings.
func hashToken(input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, maxTokenInput+1))
	if err != nil {
		return fmt.Errorf("hash token: read standard input: %w", err)
	}

	if len(data) > maxTokenInput {
		return fmt.Errorf("hash token: input is longer than %d bytes", maxTokenInput)
	}

	token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if token == "" {
		return errors.New("hash token: no token on standard input; try: printf '%s' \"$TOKEN\" | beacon --hash-token")
	}

	hash, err := auth.HashToken(token)
	if err != nil {
		return fmt.Errorf("hash token: %w", err)
	}

	if _, err := fmt.Fprintln(output, hash); err != nil {
		return fmt.Errorf("hash token: %w", err)
	}

	return nil
}
