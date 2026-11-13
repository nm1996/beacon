// Package client is the Go client for Beacon's HTTP API v1.
//
// Use Client to call the API directly: Register, Heartbeat, Discover,
// Unregister and Health map one to one onto its endpoints. Most services
// only need Announce, which registers an instance and keeps it alive in the
// background until the returned Lease is closed.
//
// The package uses only the standard library and does not depend on any
// other part of Beacon, so importing it adds nothing else to a build.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultTimeout bounds every request when the caller does not supply
	// its own http.Client.
	defaultTimeout = 10 * time.Second

	// maxResponseBytes caps how much of a response body is read, so a
	// misbehaving server cannot make the client buffer without limit.
	maxResponseBytes = 16 << 20

	// userAgent identifies the client in Beacon's logs and in proxies.
	userAgent = "beacon-go-client"

	// servicesPath is the API collection every service call goes to.
	servicesPath = "/api/v1/services"

	// discoverPageSize is how many instances Discover asks for per request.
	discoverPageSize = 500

	// maxDiscoverPages bounds how many pages one Discover call follows, so a
	// faulty server or proxy that keeps handing out new cursors cannot keep
	// it busy forever. At discoverPageSize it allows five million instances.
	maxDiscoverPages = 10_000
)

// Client calls a Beacon server. It is safe for concurrent use.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	logger     *slog.Logger
	token      string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient makes the client send requests through httpClient, for
// example to set a different timeout, a proxy or TLS settings. By default
// requests use an http.Client with a 10 second timeout. A nil httpClient
// keeps that default.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

// WithLogger makes the client report background events, such as a Lease
// registering again after Beacon restarted, to logger. By default nothing
// is logged, and a nil logger keeps it that way.
func WithLogger(logger *slog.Logger) Option {
	return func(client *Client) {
		if logger != nil {
			client.logger = logger
		}
	}
}

// WithToken makes the client send token as a bearer token on every request,
// for a Beacon that has authentication turned on. Use an https:// base URL
// with it, or the token crosses the network in plain text.
func WithToken(token string) Option {
	return func(client *Client) {
		client.token = token
	}
}

// New returns a client for the Beacon server at baseURL, such as
// "http://beacon:8080". baseURL may include a path prefix when Beacon is
// served behind a proxy, as in "https://example.com/beacon".
func New(baseURL string, options ...Option) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("beacon: parse base URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("beacon: base URL %q must start with http:// or https://", baseURL)
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf("beacon: base URL %q has no host", baseURL)
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""

	client := &Client{
		baseURL:    parsed,
		httpClient: &http.Client{Timeout: defaultTimeout},
		logger:     slog.New(slog.DiscardHandler),
	}

	for _, option := range options {
		option(client)
	}

	return client, nil
}

// Register adds a new instance and returns its ID and TTL. Calling it twice
// creates two instances; Register is not retried for that reason.
func (client *Client) Register(ctx context.Context, registration Registration) (Registered, error) {
	var response registerResponse

	err := client.do(ctx, http.MethodPost, servicesPath, nil, registerRequest(registration), http.StatusCreated, &response)
	if err != nil {
		return Registered{}, err
	}

	return Registered{
		ID:  response.ID,
		TTL: time.Duration(response.TTLSeconds) * time.Second,
	}, nil
}

// Heartbeat tells Beacon the instance with id is still alive. It returns an
// error matching ErrNotFound if Beacon does not know the instance, for
// example because it expired or Beacon restarted; register again then.
func (client *Client) Heartbeat(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalid)
	}

	return client.do(ctx, http.MethodPost, servicesPath+"/"+url.PathEscape(id)+"/heartbeat", nil, nil, http.StatusNoContent, nil)
}

// Discover returns the instances registered under name, or every instance
// when name is empty, sorted by ID.
//
// It fetches them discoverPageSize at a time, following the server's next
// page links, so a large registry never has to fit in one response. A
// server without paging returns everything at once, which also works.
//
// Pages are a live view, not a snapshot: an instance registered or removed
// while Discover runs may or may not be included.
//
// Discover fails rather than return a partial list when the paging goes
// wrong: a next link without a cursor, a cursor seen before, or more than
// maxDiscoverPages pages.
func (client *Client) Discover(ctx context.Context, name string) ([]Instance, error) {
	query := url.Values{"limit": {strconv.Itoa(discoverPageSize)}}
	if name != "" {
		query.Set("name", name)
	}

	var instances []Instance

	seen := make(map[string]struct{})

	for pages := 1; ; pages++ {
		var page []instanceResponse

		header, err := client.send(ctx, http.MethodGet, servicesPath, query, nil, http.StatusOK, &page)
		if err != nil {
			return nil, err
		}

		for _, item := range page {
			instances = append(instances, Instance(item))
		}

		cursor, more, err := nextCursor(header)
		if err != nil {
			return nil, err
		}

		if !more {
			break
		}

		if _, repeated := seen[cursor]; repeated {
			return nil, fmt.Errorf("beacon: server returned page cursor %q twice", cursor)
		}

		if pages == maxDiscoverPages {
			return nil, fmt.Errorf("beacon: discovery did not finish within %d pages", maxDiscoverPages)
		}

		seen[cursor] = struct{}{}
		query.Set("cursor", cursor)
	}

	if instances == nil {
		instances = []Instance{}
	}

	return instances, nil
}

// nextCursor returns the cursor of the rel="next" Link in header, and
// whether there is such a link. A next link without a usable cursor is an
// error, so a broken response is never mistaken for the last page. Only the
// cursor is taken from the link: its path is the server's own and would miss
// any prefix in front of it, such as a proxy's.
func nextCursor(header http.Header) (cursor string, more bool, err error) {
	for _, value := range header.Values("Link") {
		for link := range strings.SplitSeq(value, ",") {
			target, params, found := strings.Cut(strings.TrimSpace(link), ";")
			if !found || !strings.Contains(params, `rel="next"`) {
				continue
			}

			parsed, err := url.Parse(strings.Trim(strings.TrimSpace(target), "<>"))
			if err != nil {
				return "", false, fmt.Errorf("beacon: unreadable next page link %q: %w", target, err)
			}

			cursor := parsed.Query().Get("cursor")
			if cursor == "" {
				return "", false, fmt.Errorf("beacon: next page link %q has no cursor", target)
			}

			return cursor, true, nil
		}
	}

	return "", false, nil
}

// Unregister removes the instance with id. It returns an error matching
// ErrNotFound if Beacon does not know the instance.
func (client *Client) Unregister(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalid)
	}

	return client.do(ctx, http.MethodDelete, servicesPath+"/"+url.PathEscape(id), nil, nil, http.StatusNoContent, nil)
}

// Health reports whether Beacon is serving requests, and which version.
func (client *Client) Health(ctx context.Context) (Health, error) {
	var response Health
	if err := client.do(ctx, http.MethodGet, "/health", nil, nil, http.StatusOK, &response); err != nil {
		return Health{}, err
	}

	return response, nil
}

// do sends one request and decodes the response into out when the status
// is want. Any other status becomes an *Error.
func (client *Client) do(ctx context.Context, method, path string, query url.Values, body any, want int, out any) error {
	_, err := client.send(ctx, method, path, query, body, want, out)

	return err
}

// send is do that also returns the response headers.
func (client *Client) send(ctx context.Context, method, path string, query url.Values, body any, want int, out any) (http.Header, error) {
	target := *client.baseURL
	target.Path += path
	target.RawQuery = query.Encode()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("beacon: encode request: %w", err)
		}

		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("beacon: build request: %w", err)
	}

	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/json")

	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("beacon: %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, maxResponseBytes)

	if response.StatusCode != want {
		return response.Header, decodeError(response.StatusCode, limited)
	}

	if out == nil {
		return response.Header, nil
	}

	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return response.Header, fmt.Errorf("beacon: decode %s %s response: %w", method, path, err)
	}

	return response.Header, nil
}

// decodeError turns an unexpected response into an *Error. Bodies that are
// not Beacon's JSON error format, such as an HTML page from a proxy, keep
// the status code and fall back to the status text as the message.
func decodeError(status int, body io.Reader) error {
	apiError := &Error{StatusCode: status, Message: http.StatusText(status)}

	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(body).Decode(&response); err == nil && response.Error.Code != "" {
		apiError.Code = response.Error.Code
		apiError.Message = response.Error.Message
	}

	return apiError
}
