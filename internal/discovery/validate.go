package discovery

import (
	"fmt"
	"strings"
)

// The range of TCP ports an instance may register with.
const (
	minPort = 1
	maxPort = 65535
)

// validate checks the fields of a registration. The returned error wraps
// ErrInvalidRegistration and names the first field that is wrong.
func (registration Registration) validate() error {
	if strings.TrimSpace(registration.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRegistration)
	}

	if strings.TrimSpace(registration.Address) == "" {
		return fmt.Errorf("%w: address is required", ErrInvalidRegistration)
	}

	if registration.Port < minPort || registration.Port > maxPort {
		return fmt.Errorf("%w: port must be between %d and %d", ErrInvalidRegistration, minPort, maxPort)
	}

	return nil
}
