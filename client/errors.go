package client

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound matches errors for an instance Beacon does not know,
	// because it never existed, expired or Beacon restarted.
	ErrNotFound = errors.New("beacon: instance not found")

	// ErrInvalid matches errors for a request Beacon rejected as invalid,
	// such as a registration with a port out of range.
	ErrInvalid = errors.New("beacon: invalid request")
)

// Error is a response Beacon answered with an unexpected status. Code holds
// Beacon's stable error code, such as "service_not_found", when the
// response used Beacon's JSON error format.
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

// Error describes the response.
func (apiError *Error) Error() string {
	if apiError.Code == "" {
		return fmt.Sprintf("beacon: %d %s", apiError.StatusCode, apiError.Message)
	}

	return fmt.Sprintf("beacon: %s: %s (%d)", apiError.Code, apiError.Message, apiError.StatusCode)
}

// Is lets errors.Is match an *Error against ErrNotFound and ErrInvalid by
// its error code.
func (apiError *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return apiError.Code == "service_not_found"
	case ErrInvalid:
		return apiError.Code == "invalid_service" || apiError.Code == "invalid_request_body"
	default:
		return false
	}
}
