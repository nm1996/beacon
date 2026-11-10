package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nm1996/beacon/internal/registry"
)

// Error codes sent in the "code" field of every error response. Clients
// should branch on these, not on the human-readable message.
const (
	codeInvalidRequestBody = "invalid_request_body"
	codeInvalidService     = "invalid_service"
	codeServiceNotFound    = "service_not_found"
	codeNotFound           = "not_found"
	codeMethodNotAllowed   = "method_not_allowed"
	codeInternalError      = "internal_error"
)

// writeError sends an error response in the shared JSON format.
func (handler *Handler) writeError(writer http.ResponseWriter, status int, code, message string) {
	handler.writeJSON(writer, status, ErrorResponse{
		Error: ErrorDetail{Code: code, Message: message},
	})
}

// writeInvalidBody answers a request whose body mapRequest rejected. The
// message states the rules rather than the specific failure, which would
// expose Go type names.
func (handler *Handler) writeInvalidBody(writer http.ResponseWriter) {
	handler.writeError(writer, http.StatusBadRequest, codeInvalidRequestBody, fmt.Sprintf(
		"Request body must be a single JSON object of at most %d bytes with only the documented fields",
		handler.config.MaxRequestBodyBytes,
	))
}

// writeRegistryError maps a registry error to an HTTP error response.
// Unexpected errors are logged and reported without internal details.
func (handler *Handler) writeRegistryError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrInvalidService):
		handler.writeError(writer, http.StatusBadRequest, codeInvalidService, err.Error())
	case errors.Is(err, registry.ErrServiceNotFound):
		handler.writeError(writer, http.StatusNotFound, codeServiceNotFound, "No service is registered with this ID")
	default:
		handler.logger.Error("registry operation failed", slog.Any("error", err))
		handler.writeError(writer, http.StatusInternalServerError, codeInternalError, "Internal server error")
	}
}

// withJSONErrors serves requests through mux but replaces ServeMux's
// plain-text 404 and 405 responses with the JSON error format. Matched
// requests, including ServeMux's own redirects, are passed through untouched.
func (handler *Handler) withJSONErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fallback, pattern := mux.Handler(request)
		if pattern != "" {
			mux.ServeHTTP(writer, request)
			return
		}

		// Let ServeMux decide between 404 and 405, and set the Allow header
		// for a 405, but throw away the plain-text body it writes.
		capture := &statusCapture{header: writer.Header()}
		fallback.ServeHTTP(capture, request)

		if capture.status == http.StatusMethodNotAllowed {
			handler.writeError(writer, http.StatusMethodNotAllowed, codeMethodNotAllowed,
				fmt.Sprintf("Method %s is not allowed on %s", request.Method, request.URL.Path))
			return
		}

		handler.writeError(writer, http.StatusNotFound, codeNotFound,
			fmt.Sprintf("No endpoint at %s", request.URL.Path))
	})
}

// statusCapture is a ResponseWriter that records the status code and shares
// the real writer's headers, but discards the body.
type statusCapture struct {
	header http.Header
	status int
}

// Header returns the real response's headers, so headers such as Allow
// survive.
func (capture *statusCapture) Header() http.Header {
	return capture.header
}

// Write discards the body.
func (capture *statusCapture) Write(body []byte) (int, error) {
	return len(body), nil
}

// WriteHeader records the status code instead of sending it.
func (capture *statusCapture) WriteHeader(status int) {
	capture.status = status
}
