package client

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// listTransport answers every lookup with the JSON array in body.
type listTransport struct {
	body atomic.Pointer[string]
}

// RoundTrip returns the current body.
func (transport *listTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(*transport.body.Load())),
		Request:    request,
	}, nil
}

// answer sets what transport returns.
func (transport *listTransport) answer(body string) {
	transport.body.Store(&body)
}

func TestOutliersOfInstancesThatLeftAreForgotten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := &listTransport{}
		transport.answer(`[{"id":"a","name":"orders","address":"10.0.0.1","port":1},{"id":"b","name":"orders","address":"10.0.0.2","port":2}]`)

		beacon, err := New("http://beacon.test", WithHTTPClient(&http.Client{Transport: transport}))
		if err != nil {
			t.Fatal(err)
		}

		resolver, err := beacon.NewResolver(t.Context(), "orders", WithOutlierDetection(OutlierDetection{ConsecutiveFailures: 100}))
		if err != nil {
			t.Fatal(err)
		}
		defer resolver.Close()

		resolver.Report(Instance{ID: "a"}, errors.New("refused"))
		resolver.Report(Instance{ID: "b"}, errors.New("refused"))

		// a leaves the service.
		transport.answer(`[{"id":"b","name":"orders","address":"10.0.0.2","port":2}]`)

		time.Sleep(6 * time.Second)
		synctest.Wait()

		resolver.outliersMu.Lock()
		defer resolver.outliersMu.Unlock()

		if _, kept := resolver.outliers["b"]; !kept || len(resolver.outliers) != 1 {
			t.Errorf("expected only the instance still present tracked after a refresh, got %v", resolver.outliers)
		}
	})
}
