package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// linkPattern extracts the URL of a Link header with rel="next".
var linkPattern = regexp.MustCompile(`^<([^>]+)>; rel="next"$`)

// listPage fetches path and returns the instances and the next page's URL,
// or "" when there is none.
func listPage(t *testing.T, router http.Handler, path string) ([]Service, string) {
	t.Helper()

	rec := serve(router, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, rec.Code, rec.Body.String())
	}

	var services []Service
	if err := json.Unmarshal(rec.Body.Bytes(), &services); err != nil {
		t.Fatalf("GET %s: decode: %v", path, err)
	}

	link := rec.Header().Get("Link")
	if link == "" {
		return services, ""
	}

	match := linkPattern.FindStringSubmatch(link)
	if match == nil {
		t.Fatalf("GET %s: malformed Link header %q", path, link)
	}

	return services, match[1]
}

func TestListWithoutLimitReturnsEverythingSorted(t *testing.T) {
	router, service := setupRouter(t)

	for range 5 {
		mustRegister(t, service, "orders", 8081)
	}

	services, next := listPage(t, router, "/api/v1/services")

	if len(services) != 5 || next != "" {
		t.Fatalf("expected all 5 instances and no Link header, got %d and %q", len(services), next)
	}

	if !slices.IsSortedFunc(services, func(left, right Service) int { return strings.Compare(left.ID, right.ID) }) {
		t.Error("expected instances sorted by ID")
	}
}

func TestListPagesFollowTheLinkHeader(t *testing.T) {
	router, service := setupRouter(t)

	want := make([]string, 0, 7)
	for range 7 {
		want = append(want, mustRegister(t, service, "orders", 8081))
	}

	slices.Sort(want)

	var (
		got   []string
		sizes []int
		path  = "/api/v1/services?limit=3"
	)

	for path != "" && len(sizes) < 10 {
		var page []Service
		page, path = listPage(t, router, path)

		sizes = append(sizes, len(page))
		for _, item := range page {
			got = append(got, item.ID)
		}
	}

	if fmt.Sprint(sizes) != "[3 3 1]" {
		t.Errorf("expected pages of 3, 3 and 1, got %v", sizes)
	}

	if !slices.Equal(got, want) {
		t.Errorf("expected every instance exactly once in ID order\nwant %v\ngot  %v", want, got)
	}
}

func TestListLinkKeepsTheNameFilter(t *testing.T) {
	router, service := setupRouter(t)

	for range 3 {
		mustRegister(t, service, "orders", 8081)
		mustRegister(t, service, "payments", 9000)
	}

	first, next := listPage(t, router, "/api/v1/services?name=orders&limit=2")
	if len(first) != 2 || next == "" {
		t.Fatalf("expected a first page of 2 and a next link, got %d and %q", len(first), next)
	}

	parsed, err := url.Parse(next)
	if err != nil || parsed.Query().Get("name") != "orders" || parsed.Query().Get("limit") != "2" {
		t.Fatalf("expected the next link to keep name and limit, got %q", next)
	}

	second, last := listPage(t, router, next)
	if len(second) != 1 || last != "" {
		t.Fatalf("expected a last page of 1 without a link, got %d and %q", len(second), last)
	}

	for _, item := range append(first, second...) {
		if item.Name != "orders" {
			t.Errorf("expected only orders, got %q", item.Name)
		}
	}
}

func TestListFullLastPageHasNoLink(t *testing.T) {
	router, service := setupRouter(t)

	for range 4 {
		mustRegister(t, service, "orders", 8081)
	}

	_, next := listPage(t, router, "/api/v1/services?limit=2")
	page, last := listPage(t, router, next)

	if len(page) != 2 || last != "" {
		t.Errorf("expected the exactly full last page to have no link, got %d and %q", len(page), last)
	}
}

func TestListRejectsInvalidQueries(t *testing.T) {
	for _, path := range []string{
		"/api/v1/services?limit=0",
		"/api/v1/services?limit=-1",
		"/api/v1/services?limit=ten",
		"/api/v1/services?limit=1001",
		"/api/v1/services?limit=",
		"/api/v1/services?cursor=YQ",
		"/api/v1/services?limit=2&cursor=",
		"/api/v1/services?limit=2&cursor=***",
		"/api/v1/services?limit=2&cursor=" + url.QueryEscape(encodeCursor("\xff")),
	} {
		router, _ := setupRouter(t)

		rec := serve(router, http.MethodGet, path, "")

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected status %d, got %d", path, http.StatusBadRequest, rec.Code)
			continue
		}

		if detail := decodeError(t, rec); detail.Code != codeInvalidQuery {
			t.Errorf("%s: expected code %q, got %q", path, codeInvalidQuery, detail.Code)
		}
	}
}

func TestListAcceptsTheLargestPage(t *testing.T) {
	router, _ := setupRouter(t)

	if rec := serve(router, http.MethodGet, fmt.Sprintf("/api/v1/services?limit=%d", maxPageSize), ""); rec.Code != http.StatusOK {
		t.Errorf("expected limit=%d to be accepted, got %d", maxPageSize, rec.Code)
	}
}
