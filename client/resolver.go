package client

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// defaultRefreshInterval is how often a Resolver asks Beacon for the
	// instances by default.
	defaultRefreshInterval = 5 * time.Second

	// defaultMaxStale is how long a Resolver keeps serving instances it
	// could not refresh, by default.
	defaultMaxStale = 5 * time.Minute
)

var (
	// ErrNoInstances is returned by Resolver.Pick when Beacon has no live
	// instance of the service.
	ErrNoInstances = errors.New("beacon: no instances registered")

	// ErrStale is returned by Resolver.Pick when the Resolver has not been
	// able to refresh its instances for longer than its maximum staleness.
	ErrStale = errors.New("beacon: instances are out of date")
)

// Picker chooses one of the instances a Resolver holds for each call. It
// must be safe for concurrent use.
type Picker interface {
	// Pick returns one of instances, which is never empty.
	Pick(instances []Instance) Instance
}

// roundRobin cycles through the instances in ID order.
type roundRobin struct {
	next atomic.Uint64
}

// RoundRobin returns a Picker that takes the instances in turn, so calls
// spread evenly. It is the default.
func RoundRobin() Picker {
	return &roundRobin{}
}

// Pick returns the next instance in turn.
func (picker *roundRobin) Pick(instances []Instance) Instance {
	return instances[(picker.next.Add(1)-1)%uint64(len(instances))]
}

// random picks uniformly at random.
type random struct{}

// Random returns a Picker that takes an instance at random for each call,
// which spreads load without any state shared between callers.
func Random() Picker {
	return random{}
}

// Pick returns an instance chosen at random.
func (random) Pick(instances []Instance) Instance {
	return instances[rand.IntN(len(instances))] //nolint:gosec // load spreading needs no cryptographic randomness
}

// resolverSettings are the options of a Resolver.
type resolverSettings struct {
	refreshInterval time.Duration
	maxStale        time.Duration
	picker          Picker
	outliers        OutlierDetection
}

// OutlierDetection configures how a Resolver stops picking instances that
// keep failing, as Envoy's outlier detection does. The zero value turns it
// off, which is the default.
type OutlierDetection struct {
	// ConsecutiveFailures is how many failures in a row, reported with
	// Resolver.Report, eject an instance. Zero turns detection off.
	ConsecutiveFailures int
	// EjectionTime is how long a first ejection lasts; each further one of
	// the same instance lasts that much longer, up to ten times it. Zero
	// means 30 seconds.
	EjectionTime time.Duration
	// MaxEjectedPercent caps the share of a service's instances ejected at
	// once, so a failure on the caller's side cannot eject everything. Zero
	// means 50; at least one instance always stays.
	MaxEjectedPercent int
}

// Defaults of OutlierDetection.
const (
	defaultEjectionTime      = 30 * time.Second
	defaultMaxEjectedPercent = 50
	maxEjectionMultiplier    = 10
)

// ResolverOption configures a Resolver.
type ResolverOption func(*resolverSettings)

// WithRefreshInterval sets how often the Resolver asks Beacon for the
// instances; 5 seconds by default. Values below one second are raised to
// one second.
func WithRefreshInterval(interval time.Duration) ResolverOption {
	return func(settings *resolverSettings) {
		settings.refreshInterval = max(interval, time.Second)
	}
}

// WithMaxStale sets how long the Resolver keeps serving the instances it
// last saw while it cannot reach Beacon; 5 minutes by default. After that,
// Pick returns ErrStale.
func WithMaxStale(maxStale time.Duration) ResolverOption {
	return func(settings *resolverSettings) {
		settings.maxStale = maxStale
	}
}

// WithOutlierDetection makes the Resolver skip instances that callers
// report failing; see OutlierDetection. It is off by default.
func WithOutlierDetection(detection OutlierDetection) ResolverOption {
	return func(settings *resolverSettings) {
		if detection.EjectionTime <= 0 {
			detection.EjectionTime = defaultEjectionTime
		}

		if detection.MaxEjectedPercent <= 0 || detection.MaxEjectedPercent > 100 {
			detection.MaxEjectedPercent = defaultMaxEjectedPercent
		}

		settings.outliers = detection
	}
}

// WithPicker sets how the Resolver chooses an instance; RoundRobin by
// default. A nil picker keeps the default.
func WithPicker(picker Picker) ResolverOption {
	return func(settings *resolverSettings) {
		if picker != nil {
			settings.picker = picker
		}
	}
}

// view is what a Resolver knows at one moment. It is never changed after
// it is published, so readers need no lock.
type view struct {
	instances []Instance
	etag      string
	fetchedAt time.Time
}

// Resolver keeps the instances of one service in memory and picks one of
// them for each call, without asking Beacon. A background goroutine
// refreshes them, asking Beacon only for changes; while Beacon cannot be
// reached it keeps serving the instances it last saw, for up to the
// maximum staleness. NewResolver creates one; Close stops it. It is safe
// for concurrent use.
type Resolver struct {
	client   *Client
	name     string
	settings resolverSettings

	current atomic.Pointer[view]

	// ejected maps instance IDs to when their ejection ends. It is replaced,
	// never changed, so Pick reads it without a lock; Report builds the next
	// one under outliersMu.
	ejected    atomic.Pointer[map[string]time.Time]
	outliersMu sync.Mutex
	outliers   map[string]*outlier

	stop      context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// NewResolver fetches the instances of the service called name and returns
// a Resolver that keeps them up to date until Close. It fails, and starts
// nothing, if that first fetch fails, so a mistake such as an unreachable
// Beacon shows at startup. A service without instances is not a failure.
//
// ctx only bounds the first fetch; the refreshes keep ctx's values but not
// its cancellation.
func (client *Client) NewResolver(ctx context.Context, name string, options ...ResolverOption) (*Resolver, error) {
	settings := resolverSettings{
		refreshInterval: defaultRefreshInterval,
		maxStale:        defaultMaxStale,
		picker:          RoundRobin(),
	}

	for _, option := range options {
		option(&settings)
	}

	resolver := &Resolver{client: client, name: name, settings: settings, done: make(chan struct{})}

	first, err := resolver.fetch(ctx, nil)
	if err != nil {
		return nil, err
	}

	resolver.current.Store(first)

	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	resolver.stop = stop

	go resolver.run(loopCtx)

	return resolver, nil
}

// Pick returns an instance to call, chosen by the Resolver's Picker. It
// makes no request. It returns ErrNoInstances when the service has none,
// and ErrStale when the Resolver has not reached Beacon for longer than
// its maximum staleness.
func (resolver *Resolver) Pick() (Instance, error) {
	current := resolver.current.Load()

	if time.Since(current.fetchedAt) > resolver.settings.maxStale {
		return Instance{}, ErrStale
	}

	if len(current.instances) == 0 {
		return Instance{}, ErrNoInstances
	}

	return resolver.settings.picker.Pick(resolver.healthy(current.instances)), nil
}

// outlier is what a Resolver tracks about an instance callers report on.
type outlier struct {
	failures  int
	ejections int
}

// Report tells the Resolver how a call to instance went: err nil for a
// success, which resets its count, or the error. With outlier detection on,
// enough failures in a row eject the instance for a while, and Pick skips
// it. Without it, Report does nothing.
func (resolver *Resolver) Report(instance Instance, err error) {
	detection := resolver.settings.outliers
	if detection.ConsecutiveFailures <= 0 {
		return
	}

	resolver.outliersMu.Lock()
	defer resolver.outliersMu.Unlock()

	if resolver.outliers == nil {
		resolver.outliers = make(map[string]*outlier)
	}

	state := resolver.outliers[instance.ID]
	if state == nil {
		state = &outlier{}
		resolver.outliers[instance.ID] = state
	}

	if err == nil {
		state.failures = 0
		return
	}

	state.failures++
	if state.failures < detection.ConsecutiveFailures {
		return
	}

	now := time.Now()
	ejected := resolver.liveEjections(now)

	if _, already := ejected[instance.ID]; already {
		return
	}

	total := len(resolver.current.Load().instances)
	if allowed := total * detection.MaxEjectedPercent / 100; len(ejected) >= min(allowed, total-1) {
		return
	}

	state.failures = 0
	state.ejections++
	ejected[instance.ID] = now.Add(detection.EjectionTime * time.Duration(min(state.ejections, maxEjectionMultiplier)))
	resolver.ejected.Store(&ejected)

	resolver.client.logger.Warn("beacon resolver ejected an instance",
		slog.String("name", resolver.name), slog.String("id", instance.ID), slog.Int("ejections", state.ejections))
}

// pruneOutliers forgets what outlier detection tracked about instances no
// longer in the service, so a long-lived Resolver of a service whose
// instances come and go does not grow without bound.
func (resolver *Resolver) pruneOutliers(instances []Instance) {
	resolver.outliersMu.Lock()
	defer resolver.outliersMu.Unlock()

	if len(resolver.outliers) == 0 {
		return
	}

	present := make(map[string]bool, len(instances))
	for _, instance := range instances {
		present[instance.ID] = true
	}

	for id := range resolver.outliers {
		if !present[id] {
			delete(resolver.outliers, id)
		}
	}

	ejected := resolver.liveEjections(time.Now())
	for id := range ejected {
		if !present[id] {
			delete(ejected, id)
		}
	}

	resolver.ejected.Store(&ejected)
}

// liveEjections returns a copy of the ejections that have not ended by
// now. The caller holds outliersMu.
func (resolver *Resolver) liveEjections(now time.Time) map[string]time.Time {
	live := make(map[string]time.Time)

	if current := resolver.ejected.Load(); current != nil {
		for id, until := range *current {
			if now.Before(until) {
				live[id] = until
			}
		}
	}

	return live
}

// healthy returns instances without those currently ejected, or instances
// itself when none is.
func (resolver *Resolver) healthy(instances []Instance) []Instance {
	ejected := resolver.ejected.Load()
	if ejected == nil || len(*ejected) == 0 {
		return instances
	}

	now := time.Now()
	result := make([]Instance, 0, len(instances))

	for _, instance := range instances {
		if until, out := (*ejected)[instance.ID]; !out || !now.Before(until) {
			result = append(result, instance)
		}
	}

	// Ejections never cover everything, but the view may have shrunk since.
	if len(result) == 0 {
		return instances
	}

	return result
}

// Instances returns a copy of the instances the Resolver holds, sorted by
// ID, however old they are.
func (resolver *Resolver) Instances() []Instance {
	return slices.Clone(resolver.current.Load().instances)
}

// Close stops the refreshes and waits for the background goroutine to
// exit. Pick keeps answering from the last instances seen. It is safe to
// call more than once.
func (resolver *Resolver) Close() {
	resolver.closeOnce.Do(func() {
		resolver.stop()
		<-resolver.done
	})
}

// run refreshes the instances until ctx is cancelled: every refresh
// interval, or sooner with backoff after a failure.
func (resolver *Resolver) run(ctx context.Context) {
	defer close(resolver.done)

	failures := 0
	timer := time.NewTimer(resolver.nextDelay(failures))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		latest, err := resolver.fetch(ctx, resolver.current.Load())
		switch {
		case err == nil:
			resolver.current.Store(latest)
			resolver.pruneOutliers(latest.instances)
			failures = 0
		case ctx.Err() == nil:
			failures++
			resolver.client.logger.WarnContext(ctx, "beacon resolver refresh failed",
				slog.String("name", resolver.name), slog.Int("failures", failures), slog.Any("error", err))
		}

		timer.Reset(resolver.nextDelay(failures))
	}
}

// nextDelay is the refresh interval with up to 10% jitter, or after
// failures a doubling delay from one second, capped at the interval.
func (resolver *Resolver) nextDelay(failures int) time.Duration {
	interval := resolver.settings.refreshInterval
	if failures > 0 {
		interval = min(interval, firstRetryDelay<<min(failures-1, maxBackoffDoublings))
	}

	jitter := 0.9 + 0.2*rand.Float64() //nolint:gosec // jitter needs no cryptographic randomness

	return time.Duration(float64(interval) * jitter)
}

// fetch asks Beacon for the instances. With a previous view it sends that
// view's ETag, and keeps its instances if Beacon answers 304 Not
// Modified. A service with more than one page of instances is fetched
// whole with Discover instead, which is never conditional.
func (resolver *Resolver) fetch(ctx context.Context, previous *view) (*view, error) {
	query := url.Values{"name": {resolver.name}, "limit": {strconv.Itoa(discoverPageSize)}}

	header := http.Header{}
	if previous != nil && previous.etag != "" {
		header.Set("If-None-Match", previous.etag)
	}

	var page []instanceResponse

	responseHeader, err := resolver.client.send(ctx, http.MethodGet, servicesPath, query, header, nil, &page,
		http.StatusOK, http.StatusNotModified)
	if err != nil {
		return nil, err
	}

	now := time.Now()

	// Only a 304 leaves page nil: a 200 always holds a JSON array.
	if page == nil && previous != nil {
		return &view{instances: previous.instances, etag: previous.etag, fetchedAt: now}, nil
	}

	if _, more, err := nextCursor(responseHeader); err != nil || more {
		all, err := resolver.client.Discover(ctx, resolver.name)
		if err != nil {
			return nil, err
		}

		return &view{instances: all, fetchedAt: now}, nil
	}

	instances := make([]Instance, 0, len(page))
	for _, item := range page {
		instances = append(instances, Instance(item))
	}

	return &view{instances: instances, etag: responseHeader.Get("ETag"), fetchedAt: now}, nil
}
