package registry

import "errors"

var (
	// ErrInvalidService reports that a service failed validation.
	ErrInvalidService = errors.New("invalid service")

	// ErrServiceNotFound reports that no service is registered with the given ID.
	ErrServiceNotFound = errors.New("service not found")
)
