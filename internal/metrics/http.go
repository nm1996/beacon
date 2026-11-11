package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// unmatchedRoute labels requests no route matched, so arbitrary paths sent
// by clients cannot create new label values.
const unmatchedRoute = "unmatched"

// InstrumentHTTP wraps next so every request is counted and timed. Requests
// are labelled with the ServeMux pattern that served them, such as
// "/api/v1/services/{id}", never with the raw path, which keeps the number of
// series small however many instance IDs exist.
func (metrics *Metrics) InstrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}

		// ServeMux records the pattern it matched on the request it is given.
		// Hand next a shallow copy with no pattern, so a pattern set by an
		// outer mux is not mistaken for the route next matched.
		routed := request.WithContext(request.Context())
		routed.Pattern = ""

		next.ServeHTTP(recorder, routed)

		route := routed.Pattern
		if route == "" {
			route = unmatchedRoute
		}

		metrics.httpRequests.WithLabelValues(request.Method, route, strconv.Itoa(recorder.status)).Inc()
		metrics.httpDuration.WithLabelValues(request.Method, route).Observe(time.Since(started).Seconds())
	})
}

// statusRecorder remembers the status code a handler sends.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records status and sends it.
func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (recorder *statusRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}
