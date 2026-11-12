package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHashToken(test *testing.T) {
	// printf '%s' orders-token-0123456789 | sha256sum
	const want = "123c584f4a81f33410bea3456f4f11f97bc7dc33517462ae3b1aec444fd508f0\n"

	for _, input := range []string{"orders-token-0123456789", "orders-token-0123456789\n", "orders-token-0123456789\r\n"} {
		var output bytes.Buffer

		if err := hashToken(strings.NewReader(input), &output); err != nil {
			test.Fatalf("input %q: %v", input, err)
		}

		if output.String() != want {
			test.Errorf("input %q: expected %q, got %q", input, want, output.String())
		}
	}
}

func TestHashTokenRejectsBadInput(test *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "empty", input: "", wantErr: "no token on standard input"},
		{name: "only a newline", input: "\n", wantErr: "no token on standard input"},
		{name: "too short", input: "short\n", wantErr: "at least 16 characters"},
		{name: "two lines", input: "first-token-0123456789\nsecond-token-0123456789\n", wantErr: "printable ASCII without spaces"},
		{name: "too long", input: strings.Repeat("a", maxTokenInput+1), wantErr: "longer than"},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			var output bytes.Buffer

			err := hashToken(strings.NewReader(testCase.input), &output)

			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				test.Errorf("expected an error containing %q, got %v", testCase.wantErr, err)
			}

			if output.Len() != 0 {
				test.Errorf("expected no output on failure, got %q", output.String())
			}
		})
	}
}
