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
	// maxNameLength matches a DNS label, so a service name can always be
	// used as one, for example in a Kubernetes Service or a DNS record.
	maxNameLength = 63

	// maxAddressLength is the longest fully qualified DNS name.
	maxAddressLength = 253

	// maxHostLabelLength is the longest single label of a host name.
	maxHostLabelLength = 63
)

// namePattern is what a service name may look like: lowercase letters,
// digits, '.', '_' and '-', starting and ending with a letter or digit.
// Names are compared exactly, so the rule keeps "orders", "Orders" and
// "orders " from becoming three different services.
var namePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)

// hostLabelPattern is one label of a host name: letters, digits and '-',
// not starting or ending with '-'.
var hostLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// validate checks the fields of a registration. The returned error wraps
// ErrInvalidRegistration and names the first field that is wrong.
func (registration Registration) validate() error {
	if err := validateName(registration.Name); err != nil {
		return err
	}

	if err := validateAddress(registration.Address); err != nil {
		return err
	}

	if registration.Port < minPort || registration.Port > maxPort {
		return fmt.Errorf("%w: port must be between %d and %d", ErrInvalidRegistration, minPort, maxPort)
	}

	return nil
}

// validateName checks a service name against namePattern and its length.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRegistration)
	}

	if len(name) > maxNameLength || !namePattern.MatchString(name) {
		return fmt.Errorf("%w: name must be at most %d characters of lowercase letters, digits, '.', '_' or '-', "+
			"starting and ending with a letter or digit", ErrInvalidRegistration, maxNameLength)
	}

	return nil
}

// validateAddress accepts an IPv4 or IPv6 address, or a host name made of
// valid labels. It rejects anything else a client might send by mistake,
// such as a URL, a host with a port, or surrounding whitespace.
func validateAddress(address string) error {
	if address == "" {
		return fmt.Errorf("%w: address is required", ErrInvalidRegistration)
	}

	if _, err := netip.ParseAddr(address); err == nil {
		return nil
	}

	invalid := fmt.Errorf("%w: address must be an IP address or a host name of at most %d characters, "+
		"without a scheme, port or path", ErrInvalidRegistration, maxAddressLength)

	if len(address) > maxAddressLength {
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
