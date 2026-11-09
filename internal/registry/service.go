package registry

import (
	"fmt"
	"strings"
	"time"
)

const (
	minPort = 1
	maxPort = 65535
)

type Service struct {
	ID            string
	Name          string
	Address       string
	Port          int
	RegisteredAt  time.Time
	LastHeartbeat time.Time
}

// validate checks the fields a caller supplies when registering a service.
// The returned error wraps ErrInvalidService.
func (s *Service) validate() error {
	if s == nil {
		return fmt.Errorf("%w: service is required", ErrInvalidService)
	}

	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidService)
	}

	if strings.TrimSpace(s.Address) == "" {
		return fmt.Errorf("%w: address is required", ErrInvalidService)
	}

	if s.Port < minPort || s.Port > maxPort {
		return fmt.Errorf("%w: port must be between %d and %d", ErrInvalidService, minPort, maxPort)
	}

	return nil
}
