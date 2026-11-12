// Package auth decides who a caller is and what they may do.
//
// Beacon knows callers by bearer tokens or by client certificates, both
// listed in one identities file (auth.tokens_file). The file holds only
// SHA-256 hashes of the tokens, so reading it is not enough to call Beacon,
// and names certificates by a subject alternative name. Each entry has a
// name, used in logs, and a set of permissions. The package knows nothing
// about HTTP or TLS handshakes; internal/api reads the token or verified
// certificate from the request and asks Identities who it belongs to.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Permission is one kind of access an identity can be given.
type Permission string

// The permissions an identity can hold.
const (
	// PermissionDiscover allows listing instances.
	PermissionDiscover Permission = "discover"
	// PermissionRegister allows registering instances, and sending
	// heartbeats for and unregistering the instances the same caller
	// registered.
	PermissionRegister Permission = "register"
	// PermissionMetrics allows reading GET /metrics.
	PermissionMetrics Permission = "metrics"
	// PermissionManage, together with PermissionRegister, extends heartbeats
	// and unregistering to instances other callers registered, for example
	// so an operator can remove a stuck instance.
	PermissionManage Permission = "manage"
)

// allPermissions is every permission, in the order error messages list them.
var allPermissions = []Permission{PermissionDiscover, PermissionRegister, PermissionMetrics, PermissionManage}

// defaultPermissions is what an entry without a permission list holds:
// everything except PermissionManage, which must be granted explicitly so a
// loosely configured service token cannot touch other services' instances.
var defaultPermissions = []Permission{PermissionDiscover, PermissionRegister, PermissionMetrics}

// MinTokenLength is the shortest token HashToken accepts. Tokens are
// compared by hash, so only their length and randomness protect them from
// guessing.
const MinTokenLength = 16

// Identity is the caller a token or certificate belongs to.
type Identity struct {
	// Name names the entry in logs and error messages. It is not secret.
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

// Identities is the set of accepted tokens and certificates loaded from an
// identities file. It is not changed after loading, so it is safe for
// concurrent use.
type Identities struct {
	tokens       []tokenIdentity
	certificates []certificateIdentity
}

// tokenIdentity is one accepted token.
type tokenIdentity struct {
	identity Identity
	hash     [sha256.Size]byte
}

// certificateIdentity is one accepted client certificate, matched by a DNS
// or URI subject alternative name. Exactly one of dns and uri is set.
type certificateIdentity struct {
	identity Identity
	dns      string
	uri      string
}

// identitiesFile is the layout of an identities file.
type identitiesFile struct {
	Tokens       []tokenEntry       `toml:"tokens"`
	Certificates []certificateEntry `toml:"certificates"`
}

// tokenEntry is one [[tokens]] table. Permissions is a pointer so that a
// missing list, meaning every permission, can be told apart from an empty
// one, which is rejected.
type tokenEntry struct {
	Name        string    `toml:"name"`
	SHA256      string    `toml:"sha256"`
	Permissions *[]string `toml:"permissions"`
}

// certificateEntry is one [[certificates]] table.
type certificateEntry struct {
	Name        string    `toml:"name"`
	DNS         string    `toml:"dns"`
	URI         string    `toml:"uri"`
	Permissions *[]string `toml:"permissions"`
}

// Load reads the identities file at path. It fails if the file is missing,
// is not valid TOML, has an unknown key, or lists neither tokens nor
// certificates, and reports every invalid entry at once: a missing name or
// one used twice, a token hash that is not 64 hex characters or appears
// twice, a certificate without exactly one of dns and uri or matching the
// same name as another, or an unknown or empty permission list.
func Load(path string) (*Identities, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // The path is the operator's own auth.tokens_file setting.
	if err != nil {
		return nil, fmt.Errorf("read tokens file %s: %w", path, err)
	}

	return Parse(contents, path)
}

// Parse reads an identities file from contents, as Load does from a file.
// path only names the file in error messages.
func Parse(contents []byte, path string) (*Identities, error) {
	var file identitiesFile

	metadata, err := toml.Decode(string(contents), &file)
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

	identities, err := newIdentities(file)
	if err != nil {
		return nil, fmt.Errorf("invalid tokens file %s: %w", path, err)
	}

	return identities, nil
}

// newIdentities validates the entries of an identities file and builds
// Identities from them, reporting every problem at once.
func newIdentities(file identitiesFile) (*Identities, error) {
	if len(file.Tokens) == 0 && len(file.Certificates) == 0 {
		return nil, errors.New("no tokens or certificates listed; add at least one [[tokens]] or [[certificates]] entry")
	}

	var problems []error

	report := func(label string, err error) {
		problems = append(problems, fmt.Errorf("%s: %w", label, err))
	}

	names := make(map[string]bool)
	checkName := func(label, name string) {
		switch {
		case name == "":
			report(label, errors.New("name is required"))
		case names[name]:
			report(label, errors.New("name is used more than once"))
		}

		names[name] = true
	}

	identities := &Identities{}

	hashes := make(map[[sha256.Size]byte]bool)
	for index, item := range file.Tokens {
		label := entryLabel("tokens", index, item.Name)
		checkName(label, item.Name)

		hash, err := parseHash(item.SHA256)
		switch {
		case err != nil:
			report(label, err)
		case hashes[hash]:
			report(label, errors.New("sha256 is used by another token"))
		}

		hashes[hash] = true

		permissions, err := parsePermissions(item.Permissions)
		if err != nil {
			report(label, err)
		}

		identities.tokens = append(identities.tokens, tokenIdentity{
			identity: Identity{Name: item.Name, permissions: permissions},
			hash:     hash,
		})
	}

	subjects := make(map[string]bool)
	for index, item := range file.Certificates {
		label := entryLabel("certificates", index, item.Name)
		checkName(label, item.Name)

		subject, err := parseSubject(item)
		switch {
		case err != nil:
			report(label, err)
		case subjects[subject.key()]:
			report(label, errors.New("matches the same certificate name as another entry"))
		}

		subjects[subject.key()] = true

		permissions, err := parsePermissions(item.Permissions)
		if err != nil {
			report(label, err)
		}

		subject.identity = Identity{Name: item.Name, permissions: permissions}
		identities.certificates = append(identities.certificates, subject)
	}

	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	return identities, nil
}

// entryLabel names an entry in error messages: by its name when it has one,
// otherwise by its table and position.
func entryLabel(table string, index int, name string) string {
	if name == "" {
		return fmt.Sprintf("%s[%d]", table, index)
	}

	return fmt.Sprintf("%s %q", strings.TrimSuffix(table, "s"), name)
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

// parseSubject reads which certificate name an entry matches. DNS names are
// kept in lower case, since they compare case-insensitively; URIs, such as
// SPIFFE IDs, must be absolute and compare exactly.
func parseSubject(item certificateEntry) (certificateIdentity, error) {
	switch {
	case (item.DNS == "") == (item.URI == ""):
		return certificateIdentity{}, errors.New("set exactly one of dns and uri")
	case item.DNS != "":
		if strings.ContainsAny(item.DNS, " *") {
			return certificateIdentity{}, fmt.Errorf("dns %q must be a plain host name; wildcards are not matched", item.DNS)
		}

		return certificateIdentity{dns: strings.ToLower(item.DNS)}, nil
	default:
		parsed, err := url.Parse(item.URI)
		if err != nil || !parsed.IsAbs() {
			return certificateIdentity{}, fmt.Errorf("uri %q must be an absolute URI such as spiffe://example.org/orders", item.URI)
		}

		return certificateIdentity{uri: parsed.String()}, nil
	}
}

// key identifies the certificate name an entry matches, to find duplicates.
func (certificate certificateIdentity) key() string {
	if certificate.dns != "" {
		return "dns:" + certificate.dns
	}

	return "uri:" + certificate.uri
}

// parsePermissions turns an entry's permission list into Permissions. A
// missing list grants defaultPermissions. An empty list is rejected, since
// an entry that may do nothing is almost certainly a mistake, and so is
// manage without register, which would have no effect.
func parsePermissions(values *[]string) ([]Permission, error) {
	if values == nil {
		return defaultPermissions, nil
	}

	if len(*values) == 0 {
		return nil, errors.New("permissions is empty; list some, or leave it out to allow discover, register and metrics")
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

	if slices.Contains(permissions, PermissionManage) && !slices.Contains(permissions, PermissionRegister) {
		return nil, errors.New("manage only works together with register; add register to permissions")
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

// HasCertificates reports whether any [[certificates]] entries were loaded.
func (identities *Identities) HasCertificates() bool {
	return len(identities.certificates) > 0
}

// Authenticate returns the identity token belongs to, if it is accepted.
// It compares the token's hash with every entry in constant time, so the
// time taken does not reveal which entry, if any, came close.
func (identities *Identities) Authenticate(token string) (Identity, bool) {
	hash := sha256.Sum256([]byte(token))

	var (
		found    Identity
		accepted bool
	)

	for _, candidate := range identities.tokens {
		if subtle.ConstantTimeCompare(hash[:], candidate.hash[:]) == 1 {
			found = candidate.identity
			accepted = true
		}
	}

	return found, accepted
}

// AuthenticateCertificate returns the identity of the first entry, in file
// order, that names one of certificate's DNS or URI subject alternative
// names. The caller must have verified certificate against the client CAs;
// this only maps an already trusted certificate to its permissions. The
// subject's common name is ignored, as it is for server certificates.
func (identities *Identities) AuthenticateCertificate(certificate *x509.Certificate) (Identity, bool) {
	for _, candidate := range identities.certificates {
		if candidate.dns != "" && slices.ContainsFunc(certificate.DNSNames, func(name string) bool {
			return strings.EqualFold(name, candidate.dns)
		}) {
			return candidate.identity, true
		}

		if candidate.uri != "" && slices.ContainsFunc(certificate.URIs, func(uri *url.URL) bool {
			return uri.String() == candidate.uri
		}) {
			return candidate.identity, true
		}
	}

	return Identity{}, false
}

// HashToken returns the hex SHA-256 hash of token for an identities file. It
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
