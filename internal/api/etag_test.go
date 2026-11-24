package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

// listWith lists the orders service with ifNoneMatch, if not empty, as the
// If-None-Match header.
func listWith(router http.Handler, ifNoneMatch string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, servicesPath+"?name=orders", nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	return recorder
}

func TestListAnswersNotModifiedWhileTheInstancesStayTheSame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := NewRouter(newDiscovery(t, testTTL), discard, testConfig)
		location := registerWith(router, `{"name":"orders","address":"10.0.0.1","port":8081}`, "").Header().Get("Location")

		first := listWith(router, "")
		etag := first.Header().Get("ETag")

		if first.Code != http.StatusOK || len(etag) < 4 || etag[:2] != `W/` {
			t.Fatalf("expected 200 with a weak ETag, got %d, %q", first.Code, etag)
		}

		// A heartbeat changes last_heartbeat but not where to connect.
		time.Sleep(time.Second)
		serve(router, http.MethodPost, location+"/heartbeat", "")

		unchanged := listWith(router, etag)
		if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 || unchanged.Header().Get("ETag") != etag {
			t.Fatalf("expected 304 without a body and with the same ETag, got %d, %q", unchanged.Code, unchanged.Body.String())
		}

		for _, header := range []string{"*", `"other", ` + etag, etag[2:]} {
			if code := listWith(router, header).Code; code != http.StatusNotModified {
				t.Errorf("If-None-Match %q: expected 304, got %d", header, code)
			}
		}

		registerWith(router, `{"name":"orders","address":"10.0.0.2","port":8081}`, "")

		changed := listWith(router, etag)
		if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag {
			t.Errorf("expected a new instance to change the ETag, got %d, %q", changed.Code, changed.Header().Get("ETag"))
		}
	})
}

func TestTheETagCoversTheNextPageLink(t *testing.T) {
	router := NewRouter(newDiscovery(t, testTTL), discard, testConfig)
	registerWith(router, `{"id":"a","name":"orders","address":"10.0.0.1","port":8081}`, "")

	alone := serve(router, http.MethodGet, servicesPath+"?limit=1", "").Header().Get("ETag")

	registerWith(router, `{"id":"b","name":"orders","address":"10.0.0.2","port":8081}`, "")

	// The first page still holds only "a", but now links to a next page.
	if paged := serve(router, http.MethodGet, servicesPath+"?limit=1", "").Header().Get("ETag"); paged == alone {
		t.Error("expected the ETag to change when a next page appears")
	}
}
