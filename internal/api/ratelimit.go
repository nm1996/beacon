package api

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/nm1996/beacon/internal/ratelimit"
)

// Limits that refuse requests, passed to Config.RateLimited and used as the
// limit label of beacon_rate_limited_total.
const (
	// LimitRegistrations is the per-caller limit on new registrations.
	LimitRegistrations = "registrations"
	// LimitAuthFailures is the per-network limit on failed authentication.
	LimitAuthFailures = "auth_failures"
)

// codeRateLimited is the error code of a request refused by a rate limit.
const codeRateLimited = "rate_limited"

// ipv6NetworkBits is how much of an IPv6 address identifies one client.
// A single host commonly has a whole /64, so keying on the full address
// would let it pick a new key for every request.
const ipv6NetworkBits = 64

// clientNetwork returns the key that stands for request's client network:
// its IPv4 address, or the /64 its IPv6 address is in. It uses the address
// of the connection, so behind a proxy every client shares the proxy's.
func clientNetwork(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}

	address, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}

	address = address.Unmap()
	if address.Is4() {
		return address.String()
	}

	prefix, err := address.WithZone("").Prefix(ipv6NetworkBits)
	if err != nil {
		return address.String()
	}

	return prefix.String()
}

// registrationKey is whom a registration counts against: the authenticated
// caller, or the client network when authentication is off.
func (handler *Handler) registrationKey(request *http.Request) string {
	if identity, ok := identityFrom(request.Context()); ok {
		return "caller:" + identity.Name
	}

	return "network:" + clientNetwork(request)
}

// limitRegistrations wraps next so each caller may register at most as
// often as config.RegistrationLimit allows. It runs after the Guard, so an
// authenticated caller is limited by name wherever it connects from.
func (handler *Handler) limitRegistrations(next http.Handler) http.Handler {
	limiter := handler.config.RegistrationLimit
	if limiter == nil {
		return next
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		key := handler.registrationKey(request)

		if ok, wait := limiter.Allow(key); !ok {
			handler.config.rateLimited(LimitRegistrations)
			handler.logger.LogAttrs(request.Context(), slog.LevelWarn, "request refused",
				slog.String("reason", codeRateLimited),
				slog.String("limit", LimitRegistrations),
				slog.String("key", key),
			)
			writeRateLimited(handler.logger, writer, wait,
				"Too many registrations from this caller; retry after the time in the Retry-After header")

			return
		}

		next.ServeHTTP(writer, request)
	})
}

// writeRateLimited answers 429 rate_limited, telling the client in
// Retry-After how many whole seconds to wait.
func writeRateLimited(logger *slog.Logger, writer http.ResponseWriter, wait time.Duration, message string) {
	writer.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(wait), 10))
	writeJSON(logger, writer, http.StatusTooManyRequests, ErrorResponse{Error: ErrorDetail{Code: codeRateLimited, Message: message}})
}

// retryAfterSeconds rounds wait up to whole seconds, at least one, as the
// Retry-After header needs.
func retryAfterSeconds(wait time.Duration) int64 {
	if wait >= time.Duration(math.MaxInt64)-time.Second {
		return math.MaxInt32
	}

	return max(1, int64((wait+time.Second-1)/time.Second))
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithFailureLimit makes the Guard refuse, with 429, every request from a
// client network that has failed authentication more often than limiter
// allows, before looking at its credentials. limited, if not nil, is called
// for each such refusal.
//
// Each request reserves a token before its credentials are checked, so
// concurrent attempts cannot all get past the check before any is counted:
// at most as many are checked at once as the network has tokens left. The
// token is spent if the credentials are invalid and given back otherwise.
//
// Only invalid credentials count as failures. Requests without any are
// probes or misconfiguration, and a known caller lacking a permission has
// already proved who it is.
func WithFailureLimit(limiter *ratelimit.Limiter, limited func()) GuardOption {
	return func(guard *Guard) {
		guard.failureLimit = limiter

		if limited != nil {
			guard.failureLimited = limited
		}
	}
}
