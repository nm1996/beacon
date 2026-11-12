// Package auth decides who a caller is and what they may do.
//
// Beacon knows callers by bearer tokens listed in a tokens file. The file
// holds only SHA-256 hashes of the tokens, so reading it is not enough to
// call Beacon. Each token has a name, used in logs, and a set of
// permissions. The package knows nothing about HTTP; internal/api reads the
// token from the request and asks Tokens who it belongs to.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Permission is one kind of access a token can be given.
type Permission string

// The permissions a token can hold.
const (
	// PermissionDiscover allows listing instances.
	PermissionDiscover Permission = "discover"
	// PermissionRegister allows registering instances, sending their
	// heartbeats and unregistering them.
	PermissionRegister Permission = "register"
	// PermissionMetrics allows reading GET /metrics.
	PermissionMetrics Permission = "metrics"
)

// allPermissions is every permission, in the order error messages list them.
// A token that does not list its permissions holds all of them.
var allPermissions = []Permission{PermissionDiscover, PermissionRegister, PermissionMetrics}

// MinTokenLength is the shortest token HashToken accepts. Tokens are
// compared by hash, so only their length and randomness protect them from
// guessing.
const MinTokenLength = 16

// Identity is the caller a token belongs to.
type Identity struct {
	// Name names the token in logs and error messages. It is not secret.
	Name        string
	permissions []Permission
}

// NewIdentity returns an identity called name that holds permissions.
func NewIdentity(name string, permissions ...Permission) Identity {
	return Identity{Name: name, permissions: slices.Clone(permissions)}
}

// Allows reports whether the identity holds permission.
func (identity Identity) Allows(permission Permission) bool {
	return slices.Contains(identity.permissions, permission)
}

// Tokens is the set of accepted tokens loaded from a tokens file. It is not
// changed after loading, so it is safe for concurrent use.
type Tokens struct {
	entries []entry
}

// entry is one accepted token.
type entry struct {
	identity Identity
	hash     [sha256.Size]byte
}

// tokensFile is the layout of a tokens file.
type tokensFile struct {
	Tokens []tokenEntry `toml:"tokens"`
}

// tokenEntry is one [[tokens]] table. Permissions is a pointer so that a
// missing list, meaning every permission, can be told apart from an empty
// one, which is rejected.
type tokenEntry struct {
	Name        string    `toml:"name"`
	SHA256      string    `toml:"sha256"`
	Permissions *[]string `toml:"permissions"`
}

// LoadTokens reads the tokens file at path. It fails if the file is missing,
// is not valid TOML, has an unknown key, or lists no tokens, and reports
// every invalid entry: a missing or repeated name, a hash that is not 64 hex
// characters or appears twice, or an unknown or empty permission list.
func LoadTokens(path string) (*Tokens, error) {
	var file tokensFile

	metadata, err := toml.DecodeFile(path, &file)
	if err != nil {
		return nil, fmt.Errorf("read tokens file %s: %w", path, err)
	}

	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}

		return nil, fmt.Errorf("read tokens file %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}

	tokens, err := newTokens(file.Tokens)
	if err != nil {
		return nil, fmt.Errorf("invalid tokens file %s: %w", path, err)
	}

	return tokens, nil
}

// newTokens validates the entries of a tokens file and builds Tokens from
// them, reporting every problem at once.
func newTokens(entries []tokenEntry) (*Tokens, error) {
	if len(entries) == 0 {
		return nil, errors.New("no tokens listed; add at least one [[tokens]] entry")
	}

	var problems []error

	names := make(map[string]bool, len(entries))
	hashes := make(map[[sha256.Size]byte]bool, len(entries))
	tokens := &Tokens{entries: make([]entry, 0, len(entries))}

	for index, item := range entries {
		label := fmt.Sprintf("tokens[%d]", index)
		if item.Name != "" {
			label = fmt.Sprintf("token %q", item.Name)
		}

		switch {
		case item.Name == "":
			problems = append(problems, fmt.Errorf("%s: name is required", label))
		case names[item.Name]:
			problems = append(problems, fmt.Errorf("%s: name is used more than once", label))
		}

		names[item.Name] = true

		hash, err := parseHash(item.SHA256)
		switch {
		case err != nil:
			problems = append(problems, fmt.Errorf("%s: %w", label, err))
		case hashes[hash]:
			problems = append(problems, fmt.Errorf("%s: sha256 is used by another token", label))
		}

		hashes[hash] = true

		permissions, err := parsePermissions(item.Permissions)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", label, err))
		}

		tokens.entries = append(tokens.entries, entry{
			identity: Identity{Name: item.Name, permissions: permissions},
			hash:     hash,
		})
	}

	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	return tokens, nil
}

// parseHash decodes a hex SHA-256 hash in either case.
func parseHash(value string) ([sha256.Size]byte, error) {
	var hash [sha256.Size]byte

	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return hash, fmt.Errorf("sha256 must be %d hex characters, as printed by beacon --hash-token", 2*sha256.Size)
	}

	copy(hash[:], decoded)

	return hash, nil
}

// parsePermissions turns a token's permission list into Permissions. A
// missing list grants every permission; an empty one is rejected, since a
// token that may do nothing is almost certainly a mistake.
func parsePermissions(values *[]string) ([]Permission, error) {
	if values == nil {
		return allPermissions, nil
	}

	if len(*values) == 0 {
		return nil, errors.New("permissions is empty; list some, or leave it out to allow everything")
	}

	permissions := make([]Permission, 0, len(*values))

	for _, value := range *values {
		permission := Permission(value)
		if !slices.Contains(allPermissions, permission) {
			return nil, fmt.Errorf("unknown permission %q; use %s", value, permissionList())
		}

		if !slices.Contains(permissions, permission) {
			permissions = append(permissions, permission)
		}
	}

	return permissions, nil
}

// permissionList names every permission for error messages.
func permissionList() string {
	names := make([]string, 0, len(allPermissions))
	for _, permission := range allPermissions {
		names = append(names, string(permission))
	}

	return strings.Join(names, ", ")
}

// Authenticate returns the identity token belongs to, if it is accepted.
// It compares the token's hash with every entry in constant time, so the
// time taken does not reveal which entry, if any, came close.
func (tokens *Tokens) Authenticate(token string) (Identity, bool) {
	hash := sha256.Sum256([]byte(token))

	var (
		found    Identity
		accepted bool
	)

	for _, candidate := range tokens.entries {
		if subtle.ConstantTimeCompare(hash[:], candidate.hash[:]) == 1 {
			found = candidate.identity
			accepted = true
		}
	}

	return found, accepted
}

// HashToken returns the hex SHA-256 hash of token for a tokens file. It
// rejects tokens shorter than MinTokenLength and tokens with anything but
// printable ASCII other than space, which cannot be sent reliably in an
// Authorization header.
func HashToken(token string) (string, error) {
	if len(token) < MinTokenLength {
		return "", fmt.Errorf("token must be at least %d characters, got %d", MinTokenLength, len(token))
	}

	for _, character := range token {
		if character < '!' || character > '~' {
			return "", errors.New("token must be printable ASCII without spaces")
		}
	}

	hash := sha256.Sum256([]byte(token))

	return hex.EncodeToString(hash[:]), nil
}
