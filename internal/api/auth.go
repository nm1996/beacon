package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nm1996/beacon/internal/auth"
)

// Reasons a Guard refuses a request, passed to its failure callback and
// used as the reason label of beacon_auth_failures_total.
const (
	// AuthFailureMissing is a request without an Authorization header.
	AuthFailureMissing = "missing"
	// AuthFailureInvalid is a malformed header or an unknown token.
	AuthFailureInvalid = "invalid"
	// AuthFailureForbidden is a known token without the needed permission.
	AuthFailureForbidden = "forbidden"
)

// Error codes for refused requests.
const (
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
)

// Authenticator finds the identity a bearer token belongs to.
// *auth.Tokens satisfies it.
type Authenticator interface {
	Authenticate(token string) (auth.Identity, bool)
}

// Guard requires a bearer token with the right permission before a request
// reaches the handler it protects. A nil *Guard lets every request through,
// which is how authentication is turned off.
type Guard struct {
	authenticator Authenticator
	logger        *slog.Logger
	failed        func(reason string)
}

// NewGuard returns a Guard that checks tokens with authenticator and logs
// refused requests to logger. failed, if not nil, is called with one of the
// AuthFailure reasons for every refused request.
func NewGuard(authenticator Authenticator, logger *slog.Logger, failed func(reason string)) *Guard {
	if failed == nil {
		failed = func(string) {}
	}

	return &Guard{authenticator: authenticator, logger: logger, failed: failed}
}

// Require wraps next so it only serves requests carrying a token that holds
// permission. A missing or unknown token gets 401 with a WWW-Authenticate
// challenge; a known token without the permission gets 403.
func (guard *Guard) Require(permission auth.Permission, next http.Handler) http.Handler {
	if guard == nil {
		return next
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		if header == "" {
			guard.refuse(writer, request, AuthFailureMissing, http.StatusUnauthorized, `Bearer realm="beacon"`,
				codeUnauthorized, "A bearer token is required")
			return
		}

		token, ok := bearerToken(header)
		if !ok {
			guard.refuse(writer, request, AuthFailureInvalid, http.StatusUnauthorized, `Bearer realm="beacon", error="invalid_request"`,
				codeUnauthorized, `The Authorization header must be "Bearer <token>"`)
			return
		}

		identity, ok := guard.authenticator.Authenticate(token)
		if !ok {
			guard.refuse(writer, request, AuthFailureInvalid, http.StatusUnauthorized, `Bearer realm="beacon", error="invalid_token"`,
				codeUnauthorized, "The bearer token is not valid")
			return
		}

		if !identity.Allows(permission) {
			guard.refuse(writer, request, AuthFailureForbidden, http.StatusForbidden,
				fmt.Sprintf(`Bearer realm="beacon", error="insufficient_scope", scope=%q`, permission),
				codeForbidden, fmt.Sprintf("Token %q does not have the %s permission", identity.Name, permission),
				slog.String("token", identity.Name))
			return
		}

		next.ServeHTTP(writer, request)
	})
}

// refuse reports and answers a refused request. Requests without any token
// are logged at debug level only, since probes and misconfigured clients
// can send many; the token itself is never logged.
func (guard *Guard) refuse(writer http.ResponseWriter, request *http.Request, reason string, status int,
	challenge, code, message string, attributes ...slog.Attr,
) {
	guard.failed(reason)

	level := slog.LevelWarn
	if reason == AuthFailureMissing {
		level = slog.LevelDebug
	}

	attributes = append(attributes,
		slog.String("reason", reason),
		slog.String("method", request.Method),
		slog.String("path", request.URL.Path),
		slog.String("remote_addr", request.RemoteAddr),
	)
	guard.logger.LogAttrs(request.Context(), level, "request refused", attributes...)

	writer.Header().Set("WWW-Authenticate", challenge)
	writeJSON(guard.logger, writer, status, ErrorResponse{Error: ErrorDetail{Code: code, Message: message}})
}

// bearerToken extracts the token from an Authorization header of the form
// "Bearer <token>". The scheme is case-insensitive, as RFC 9110 requires.
func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	token = strings.TrimSpace(token)

	return token, token != ""
}
