package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/nm1996/beacon/internal/discovery"
)

// maxPageSize is the largest "limit" GET /api/v1/services accepts.
const maxPageSize = 1000

// parseListQuery reads the "name", "limit" and "cursor" parameters of
// GET /api/v1/services. A cursor is only meaningful with a limit, since it
// comes from a Link header that always carries one.
func parseListQuery(params url.Values) (discovery.ListQuery, error) {
	query := discovery.ListQuery{Name: params.Get("name")}

	if params.Has("limit") {
		limit, err := strconv.Atoi(params.Get("limit"))
		if err != nil || limit < 1 || limit > maxPageSize {
			return discovery.ListQuery{}, fmt.Errorf("limit must be a whole number from 1 to %d", maxPageSize)
		}

		query.Limit = limit
	}

	if params.Has("cursor") {
		if query.Limit == 0 {
			return discovery.ListQuery{}, errors.New("cursor needs limit; follow the Link header of the previous page")
		}

		after, err := decodeCursor(params.Get("cursor"))
		if err != nil {
			return discovery.ListQuery{}, err
		}

		query.After = after
	}

	return query, nil
}

// nextPageLink returns a Link header value pointing at the page after the
// one that ended with lastID, keeping the name filter and the limit.
func nextPageLink(query discovery.ListQuery, lastID string) string {
	params := url.Values{}
	if query.Name != "" {
		params.Set("name", query.Name)
	}

	params.Set("limit", strconv.Itoa(query.Limit))
	params.Set("cursor", encodeCursor(lastID))

	next := url.URL{Path: servicesPath, RawQuery: params.Encode()}

	return "<" + next.String() + `>; rel="next"`
}

// encodeCursor turns the last ID of a page into an opaque cursor. Clients
// must not build cursors themselves, so the encoding may change.
func encodeCursor(lastID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(lastID))
}

// decodeCursor reverses encodeCursor.
func decodeCursor(cursor string) (string, error) {
	invalid := errors.New("cursor is not one Beacon issued; follow the Link header of the previous page")

	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
		return "", invalid
	}

	return string(decoded), nil
}
