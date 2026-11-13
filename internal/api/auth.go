package api

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nm1996/beacon/internal/auth"
)

// Reasons a Guard refuses a request, passed to its failure callback and
// used as the reason label of beacon_auth_failures_total.
const (
	// AuthFailureMissing is a request with neither an Authorization header
	// nor a verified client certificate.
	AuthFailureMissing = "missing"
	// AuthFailureInvalid is a malformed header, an unknown token, or a
	// verified client certificate that no entry names.
	AuthFailureInvalid = "invalid"
	// AuthFailureForbidden is a known caller without the needed permission.
	AuthFailureForbidden = "forbidden"
)

// Error codes for refused requests.
const (
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
)

// Authenticator finds the identity a bearer token or a verified client
// certificate belongs to. *auth.Identities satisfies it.
type Authenticator interface {
	Authenticate(token string) (auth.Identity, bool)
	AuthenticateCertificate(certificate *x509.Certificate) (auth.Identity, bool)
}

// Guard requires a caller with the right permission before a request
// reaches the handler it protects. A nil *Guard lets every request through,
// which is how authentication is turned off.
type Guard struct {
	authenticator Authenticator
	logger        *slog.Logger
	failed        func(reason string)
}

// NewGuard returns a Guard that identifies callers with authenticator and
// logs refused requests to logger. failed, if not nil, is called with one
// of the AuthFailure reasons for every refused request.
func NewGuard(authenticator Authenticator, logger *slog.Logger, failed func(reason string)) *Guard {
	if failed == nil {
		failed = func(string) {}
	}

	return &Guard{authenticator: authenticator, logger: logger, failed: failed}
}

// identityKey is the context key under which Require stores the caller's
// identity for the handlers it protects.
type identityKey struct{}

// identityFrom returns the identity Require stored in ctx, if any.
func identityFrom(ctx context.Context) (auth.Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(auth.Identity)
	return identity, ok
}

// refusal describes why a request was refused and how to answer it.
type refusal struct {
	reason     string
	status     int
	challenge  string
	code       string
	message    string
	attributes []slog.Attr
}

// Require wraps next so it only serves requests from a caller that holds
// permission. A request with an Authorization header is judged by its
// bearer token alone; one without is judged by its client certificate, if
// TLS verified one. An unknown or missing caller gets 401 with a
// WWW-Authenticate challenge; a known one without the permission gets 403.
func (guard *Guard) Require(permission auth.Permission, next http.Handler) http.Handler {
	if guard == nil {
		return next
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		identity, refused := guard.identify(request)
		if refused == nil && !identity.Allows(permission) {
			refused = &refusal{
				reason:     AuthFailureForbidden,
				status:     http.StatusForbidden,
				challenge:  fmt.Sprintf(`Bearer realm="beacon", error="insufficient_scope", scope=%q`, permission),
				code:       codeForbidden,
				message:    fmt.Sprintf("%q does not have the %s permission", identity.Name, permission),
				attributes: []slog.Attr{slog.String("caller", identity.Name)},
			}
		}

		if refused != nil {
			guard.refuse(writer, request, refused)
			return
		}

		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), identityKey{}, identity)))
	})
}

// identify finds the caller of request, or says why it cannot.
func (guard *Guard) identify(request *http.Request) (auth.Identity, *refusal) {
	header := request.Header.Get("Authorization")
	if header != "" {
		token, ok := bearerToken(header)
		if !ok {
			return auth.Identity{}, unauthorized(AuthFailureInvalid, `Bearer realm="beacon", error="invalid_request"`,
				`The Authorization header must be "Bearer <token>"`)
		}

		identity, ok := guard.authenticator.Authenticate(token)
		if !ok {
			return auth.Identity{}, unauthorized(AuthFailureInvalid, `Bearer realm="beacon", error="invalid_token"`,
				"The bearer token is not valid")
		}

		return identity, nil
	}

	certificate := verifiedClientCertificate(request)
	if certificate == nil {
		return auth.Identity{}, unauthorized(AuthFailureMissing, `Bearer realm="beacon"`,
			"A bearer token or client certificate is required")
	}

	identity, ok := guard.authenticator.AuthenticateCertificate(certificate)
	if !ok {
		refused := unauthorized(AuthFailureInvalid, `Bearer realm="beacon"`,
			"The client certificate is trusted but not listed in Beacon's tokens file")
		refused.attributes = []slog.Attr{slog.Any("certificate_names", certificateNames(certificate))}

		return auth.Identity{}, refused
	}

	return identity, nil
}

// unauthorized builds a 401 refusal.
func unauthorized(reason, challenge, message string) *refusal {
	return &refusal{
		reason:    reason,
		status:    http.StatusUnauthorized,
		challenge: challenge,
		code:      codeUnauthorized,
		message:   message,
	}
}

// refuse reports and answers a refused request. Requests without any
// credentials are logged at debug level only, since probes and
// misconfigured clients can send many; tokens are never logged.
func (guard *Guard) refuse(writer http.ResponseWriter, request *http.Request, refused *refusal) {
	guard.failed(refused.reason)

	level := slog.LevelWarn
	if refused.reason == AuthFailureMissing {
		level = slog.LevelDebug
	}

	attributes := make([]slog.Attr, 0, len(refused.attributes)+4)
	attributes = append(attributes, refused.attributes...)
	attributes = append(attributes,
		slog.String("reason", refused.reason),
		slog.String("method", request.Method),
		slog.String("path", request.URL.Path),
		slog.String("remote_addr", request.RemoteAddr),
	)
	guard.logger.LogAttrs(request.Context(), level, "request refused", attributes...)

	writer.Header().Set("WWW-Authenticate", refused.challenge)
	writeJSON(guard.logger, writer, refused.status, ErrorResponse{Error: ErrorDetail{Code: refused.code, Message: refused.message}})
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

// verifiedClientCertificate returns the client's leaf certificate if the TLS
// handshake verified it against the configured client CAs, and nil
// otherwise, including for plain HTTP and for certificates sent but not
// verified.
func verifiedClientCertificate(request *http.Request) *x509.Certificate {
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 {
		return nil
	}

	return request.TLS.VerifiedChains[0][0]
}

// certificateNames lists the DNS and URI names of certificate, which are not
// secret, so an operator can see which entry is missing from the tokens file.
func certificateNames(certificate *x509.Certificate) []string {
	names := make([]string, 0, len(certificate.DNSNames)+len(certificate.URIs))
	names = append(names, certificate.DNSNames...)

	for _, uri := range certificate.URIs {
		names = append(names, uri.String())
	}

	return names
}
