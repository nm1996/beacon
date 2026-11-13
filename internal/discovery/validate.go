package discovery

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// The range of TCP ports an instance may register with.
const (
	minPort = 1
	maxPort = 65535
)

const (
	// DefaultMaxNameLength is the longest service name accepted when
	// Config.MaxNameLength is zero. It matches a DNS label, so a service
	// name can always be used as one, for example in a Kubernetes Service
	// or a DNS record.
	DefaultMaxNameLength = 63

	// DefaultMaxAddressLength is the longest address accepted when
	// Config.MaxAddressLength is zero: the longest fully qualified DNS name.
	DefaultMaxAddressLength = 253

	// MaxLengthLimit is the highest value Config.MaxNameLength and
	// Config.MaxAddressLength may be set to. Nothing longer is a valid DNS
	// name, so a larger limit would only let in values no client can use.
	MaxLengthLimit = 253

	// maxHostLabelLength is the longest single label of a host name. It is
	// a DNS rule, not a setting.
	maxHostLabelLength = 63
)

// namePattern is what a service name may look like: runs of lowercase
// letters and digits joined by single '.', '_' or '-' separators, so it
// starts and ends with a letter or digit and never has two separators in a
// row, as in "orders..v2" or "orders._v2". Names are compared exactly, so the
// rule keeps "orders", "Orders" and "orders " from becoming three different
// services.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// hostLabelPattern is one label of a host name: letters, digits and '-',
// not starting or ending with '-'.
var hostLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// validate checks the fields of a registration against the rules and the
// length limits in config. The returned error wraps ErrInvalidRegistration
// and names the first field that is wrong.
func (registration Registration) validate(config Config) error {
	if err := validateName(registration.Name, config.maxNameLength()); err != nil {
		return err
	}

	if err := validateAddress(registration.Address, config.maxAddressLength()); err != nil {
		return err
	}

	if registration.Port < minPort || registration.Port > maxPort {
		return fmt.Errorf("%w: port must be between %d and %d", ErrInvalidRegistration, minPort, maxPort)
	}

	return nil
}

// validateName checks a service name against namePattern and maxLength.
func validateName(name string, maxLength int) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRegistration)
	}

	if len(name) > maxLength || !namePattern.MatchString(name) {
		return fmt.Errorf("%w: name must be at most %d characters of lowercase letters, digits, '.', '_' or '-', "+
			"starting and ending with a letter or digit, with no two of '.', '_' and '-' in a row",
			ErrInvalidRegistration, maxLength)
	}

	return nil
}

// validateAddress accepts an IPv4 or IPv6 address, or a host name of at most
// maxLength characters made of valid labels. It rejects anything else a
// client might send by mistake, such as a URL, a host with a port, or
// surrounding whitespace.
func validateAddress(address string, maxLength int) error {
	if address == "" {
		return fmt.Errorf("%w: address is required", ErrInvalidRegistration)
	}

	// The limit is for host names: an IP address is short by nature, and a
	// low limit must not turn IPv6 addresses away.
	if _, err := netip.ParseAddr(address); err == nil {
		return nil
	}

	invalid := fmt.Errorf("%w: address must be an IP address or a host name of at most %d characters, "+
		"without a scheme, port or path", ErrInvalidRegistration, maxLength)

	if len(address) > maxLength {
		return invalid
	}

	// A single trailing dot marks a fully qualified name and is allowed.
	for label := range strings.SplitSeq(strings.TrimSuffix(address, "."), ".") {
		if len(label) > maxHostLabelLength || !hostLabelPattern.MatchString(label) {
			return invalid
		}
	}

	return nil
}
