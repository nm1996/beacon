package client

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	// ErrNotFound matches errors for an instance Beacon does not know,
	// because it never existed, expired or Beacon restarted.
	ErrNotFound = errors.New("beacon: instance not found")

	// ErrInvalid matches errors for a request Beacon rejected as invalid,
	// such as a registration with a port out of range.
	ErrInvalid = errors.New("beacon: invalid request")

	// ErrUnauthorized matches errors for a request Beacon refused because it
	// carried no bearer token or one Beacon does not accept. See WithToken.
	ErrUnauthorized = errors.New("beacon: unauthorized")

	// ErrForbidden matches errors for a request whose token Beacon accepted
	// but which lacks the permission the request needs. Errors matching
	// ErrNotOwned match it too.
	ErrForbidden = errors.New("beacon: forbidden")

	// ErrNotOwned matches errors for a heartbeat or unregister of an instance
	// another caller registered. Only that caller, or one with the manage
	// permission, may change it.
	ErrNotOwned = errors.New("beacon: instance registered by another caller")

	// ErrQuotaExceeded matches errors for a registration Beacon refused
	// because it already holds its maximum number of instances, or the
	// caller already holds its own maximum. Retrying later can succeed once
	// instances unregister or expire; a Lease keeps retrying on its own.
	ErrQuotaExceeded = errors.New("beacon: instance quota exceeded")

	// ErrRateLimited matches errors for a request Beacon refused because the
	// caller sent too many registrations, or its network failed
	// authentication too often. It also matches a 429 without Beacon's
	// error format, such as one from a proxy. Retrying later can succeed; a
	// Lease keeps retrying on its own.
	ErrRateLimited = errors.New("beacon: rate limited")
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

// Is lets errors.Is match an *Error against ErrNotFound, ErrInvalid,
// ErrNotOwned, ErrQuotaExceeded and ErrRateLimited by its error code, and against ErrUnauthorized and
// ErrForbidden by its status code, so a 401 or 403 from a proxy in front of
// Beacon matches too.
func (apiError *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return apiError.Code == "service_not_found"
	case ErrInvalid:
		return apiError.Code == "invalid_service" || apiError.Code == "invalid_request_body"
	case ErrUnauthorized:
		return apiError.StatusCode == http.StatusUnauthorized
	case ErrForbidden:
		return apiError.StatusCode == http.StatusForbidden
	case ErrNotOwned:
		return apiError.Code == "instance_not_owned"
	case ErrQuotaExceeded:
		return apiError.Code == "quota_exceeded"
	case ErrRateLimited:
		return apiError.Code == "rate_limited" || (apiError.StatusCode == http.StatusTooManyRequests && apiError.Code == "")
	default:
		return false
	}
}
