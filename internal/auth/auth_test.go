package auth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	ordersToken  = "orders-token-0123456789"
	scraperToken = "scraper-token-0123456789"
)

// writeTokens writes content to a tokens file in a temporary directory and
// returns its path.
func writeTokens(test *testing.T, content string) string {
	test.Helper()

	path := filepath.Join(test.TempDir(), "tokens.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		test.Fatalf("write tokens file: %v", err)
	}

	return path
}

// mustHash returns the hash of token, failing the test if it is rejected.
func mustHash(test *testing.T, token string) string {
	test.Helper()

	hash, err := HashToken(token)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	return hash
}

func TestAuthenticateFindsTheTokenOwner(test *testing.T) {
	path := writeTokens(test, `
[[tokens]]
name = "orders"
sha256 = "`+mustHash(test, ordersToken)+`"
permissions = ["register", "discover"]

[[tokens]]
name = "prometheus"
sha256 = "`+strings.ToUpper(mustHash(test, scraperToken))+`"
permissions = ["metrics"]
`)

	tokens, err := Load(path)
	if err != nil {
		test.Fatalf("load tokens: %v", err)
	}

	orders, ok := tokens.Authenticate(ordersToken)
	if !ok || orders.Name != "orders" {
		test.Fatalf("expected the orders token to be accepted, got %+v, %t", orders, ok)
	}

	if !orders.Allows(PermissionRegister) || !orders.Allows(PermissionDiscover) || orders.Allows(PermissionMetrics) {
		test.Errorf("expected orders to hold register and discover only, got %+v", orders)
	}

	scraper, ok := tokens.Authenticate(scraperToken)
	if !ok || scraper.Name != "prometheus" {
		test.Fatalf("expected an upper-case hash to match too, got %+v, %t", scraper, ok)
	}

	if !scraper.Allows(PermissionMetrics) || scraper.Allows(PermissionDiscover) {
		test.Errorf("expected prometheus to hold metrics only, got %+v", scraper)
	}

	for _, rejected := range []string{"", "wrong-token-0123456789", ordersToken + " ", mustHash(test, ordersToken)} {
		if identity, ok := tokens.Authenticate(rejected); ok {
			test.Errorf("expected %q to be rejected, got %+v", rejected, identity)
		}
	}
}

func TestTokenWithoutPermissionsMayDoEverything(test *testing.T) {
	path := writeTokens(test, "[[tokens]]\nname = \"admin\"\nsha256 = \""+mustHash(test, ordersToken)+"\"\n")

	tokens, err := Load(path)
	if err != nil {
		test.Fatalf("load tokens: %v", err)
	}

	identity, ok := tokens.Authenticate(ordersToken)
	if !ok {
		test.Fatal("expected the token to be accepted")
	}

	for _, permission := range []Permission{PermissionDiscover, PermissionRegister, PermissionMetrics} {
		if !identity.Allows(permission) {
			test.Errorf("expected a token without a permission list to hold %s", permission)
		}
	}
}

func TestLoadFailures(test *testing.T) {
	hash := mustHash(test, ordersToken)
	other := mustHash(test, scraperToken)

	cases := []struct {
		name    string
		content string
		wantErr []string
	}{
		{name: "invalid TOML", content: "[[tokens]", wantErr: []string{"read tokens file"}},
		{name: "no tokens", content: "", wantErr: []string{"no tokens or certificates listed"}},
		{name: "unknown key", content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + hash + "\"\ntoken = \"secret\"", wantErr: []string{"unknown keys", "tokens.token"}},
		{name: "missing name", content: "[[tokens]]\nsha256 = \"" + hash + "\"", wantErr: []string{"tokens[0]: name is required"}},
		{
			name:    "repeated name",
			content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + hash + "\"\n[[tokens]]\nname = \"a\"\nsha256 = \"" + other + "\"",
			wantErr: []string{`token "a": name is used more than once`},
		},
		{name: "missing hash", content: "[[tokens]]\nname = \"a\"", wantErr: []string{`token "a": sha256 must be 64 hex characters`}},
		{name: "plain token instead of hash", content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + ordersToken + "\"", wantErr: []string{"sha256 must be 64 hex characters"}},
		{name: "short hash", content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + hash[:62] + "\"", wantErr: []string{"sha256 must be 64 hex characters"}},
		{
			name:    "repeated hash",
			content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + hash + "\"\n[[tokens]]\nname = \"b\"\nsha256 = \"" + hash + "\"",
			wantErr: []string{`token "b": sha256 is used by another token`},
		},
		{
			name:    "unknown permission",
			content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + hash + "\"\npermissions = [\"admin\"]",
			wantErr: []string{`unknown permission "admin"; use discover, register, metrics`},
		},
		{
			name:    "empty permissions",
			content: "[[tokens]]\nname = \"a\"\nsha256 = \"" + hash + "\"\npermissions = []",
			wantErr: []string{"permissions is empty"},
		},
		{
			name:    "several problems at once",
			content: "[[tokens]]\nsha256 = \"x\"\n[[tokens]]\nname = \"b\"\nsha256 = \"" + hash + "\"\npermissions = [\"root\"]",
			wantErr: []string{"tokens[0]: name is required", "tokens[0]: sha256", `token "b": unknown permission "root"`},
		},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := Load(writeTokens(test, testCase.content))
			if err == nil {
				test.Fatal("expected an error")
			}

			for _, want := range testCase.wantErr {
				if !strings.Contains(err.Error(), want) {
					test.Errorf("expected error to mention %q, got %v", want, err)
				}
			}
		})
	}
}

func TestLoadMissingFile(test *testing.T) {
	_, err := Load(filepath.Join(test.TempDir(), "missing.toml"))

	if !errors.Is(err, fs.ErrNotExist) {
		test.Errorf("expected a file-not-found error, got %v", err)
	}
}

func TestHashToken(test *testing.T) {
	// printf '%s' orders-token-0123456789 | sha256sum
	const want = "123c584f4a81f33410bea3456f4f11f97bc7dc33517462ae3b1aec444fd508f0"

	got, err := HashToken(ordersToken)
	if err != nil {
		test.Fatalf("hash token: %v", err)
	}

	if got != want {
		test.Errorf("expected %q, got %q", want, got)
	}

	for _, rejected := range []string{"", "short", "has a space in it, long enough", "tab\tseparated-token-0123", "ünïcödé-token-0123456789"} {
		if _, err := HashToken(rejected); err == nil {
			test.Errorf("expected %q to be rejected", rejected)
		}
	}
}
