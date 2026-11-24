package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nm1996/beacon/client"
)

// requestTimeout bounds every request the runner sends, so a hung node
// shows up as an error instead of stalling a scenario.
const requestTimeout = 2 * time.Second

// metricPrefixes are the Beacon metrics a report keeps from /metrics.
var metricPrefixes = []string{
	"beacon_registry_operations_total",
	"beacon_heartbeat_flushes_total",
	"beacon_expired_instances_total",
	"beacon_cluster_nodes",
	"beacon_http_requests_total",
}

// node is one Beacon node the runner talks to directly.
type node struct {
	name   string
	url    string
	client *client.Client
}

// cluster is everything a scenario observes: the Beacon nodes, the
// application instances behind them, and the report results go to.
type cluster struct {
	nodes  []node
	http   *http.Client
	token  string
	report *Report
}

// newCluster returns a cluster for endpoints, a list of name=URL pairs.
func newCluster(endpoints []endpoint, token string, report *Report) (*cluster, error) {
	httpClient := &http.Client{Timeout: requestTimeout}
	options := []client.Option{client.WithHTTPClient(httpClient)}

	if token != "" {
		options = append(options, client.WithToken(token))
	}

	nodes := make([]node, 0, len(endpoints))

	for _, endpoint := range endpoints {
		beacon, err := client.New(endpoint.url, options...)
		if err != nil {
			return nil, err
		}

		nodes = append(nodes, node{name: endpoint.name, url: endpoint.url, client: beacon})
	}

	return &cluster{nodes: nodes, http: httpClient, token: token, report: report}, nil
}

// discover lists service through target, recording the request.
func (cluster *cluster) discover(ctx context.Context, target node, service string) ([]client.Instance, error) {
	started := time.Now()
	instances, err := target.client.Discover(ctx, service)
	cluster.report.observe("discover", time.Since(started), failureKind(err))

	return instances, err
}

// listNodes returns the IDs of the live nodes that target reports.
func (cluster *cluster) listNodes(ctx context.Context, target node) ([]string, error) {
	var nodes []struct {
		ID string `json:"id"`
	}

	if err := cluster.getJSON(ctx, target.url+"/api/v1/nodes", "nodes", &nodes); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(nodes))
	for _, listed := range nodes {
		ids = append(ids, listed.ID)
	}

	return ids, nil
}

// health checks target's /health.
func (cluster *cluster) health(ctx context.Context, target node) error {
	_, err := target.client.Health(ctx)
	return err
}

// work is the body of an application's GET /work.
type work struct {
	Service    string    `json:"service"`
	InstanceID string    `json:"instance_id"`
	Time       time.Time `json:"time"`
}

// work calls instance's GET /work, recording the request under kind, and
// checks that the instance that answered is the one Beacon returned. An
// answer from another instance counts as a failed request.
func (cluster *cluster) work(ctx context.Context, instance client.Instance, kind string) error {
	started := time.Now()
	err := cluster.callWork(ctx, instance)
	cluster.report.observe(kind, time.Since(started), failureKind(err))

	return err
}

// callWork does the work of work, without recording it.
func (cluster *cluster) callWork(ctx context.Context, instance client.Instance) error {
	address := "http://" + net.JoinHostPort(instance.Address, strconv.Itoa(instance.Port)) + "/work"

	body, err := cluster.fetch(ctx, address)
	if err != nil {
		return err
	}

	var answered work
	if err := json.Unmarshal(body, &answered); err != nil {
		return fmt.Errorf("decode %s: %w", address, err)
	}

	if answered.InstanceID != instance.ID || answered.Service != instance.Name {
		return &wrongInstanceError{instance: instance, answered: answered}
	}

	return nil
}

// wrongInstanceError is an answer from another instance than the one
// Beacon returned for the address.
type wrongInstanceError struct {
	instance client.Instance
	answered work
}

// Error names both instances.
func (wrongErr *wrongInstanceError) Error() string {
	return fmt.Sprintf("%s:%d answered as %s %s, Beacon returned %s %s",
		wrongErr.instance.Address, wrongErr.instance.Port, wrongErr.answered.Service, wrongErr.answered.InstanceID,
		wrongErr.instance.Name, wrongErr.instance.ID)
}

// metrics returns the lines of target's /metrics that carry Beacon
// metrics listed in metricPrefixes.
func (cluster *cluster) metrics(ctx context.Context, target node) ([]string, error) {
	body, err := cluster.get(ctx, target.url+"/metrics", "metrics")
	if err != nil {
		return nil, err
	}

	var kept []string

	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		for _, prefix := range metricPrefixes {
			if strings.HasPrefix(line, prefix) {
				kept = append(kept, line)
				break
			}
		}
	}

	return kept, scanner.Err()
}

// snapshotMetrics stores every node's metrics in the report, as before or
// after the run. A node that cannot be scraped is skipped; scenarios that
// stop nodes expect that.
func (cluster *cluster) snapshotMetrics(ctx context.Context, after bool) {
	for _, target := range cluster.nodes {
		lines, err := cluster.metrics(ctx, target)
		if err != nil {
			continue
		}

		cluster.report.mu.Lock()
		snapshots := cluster.report.Metrics[target.name]

		if after {
			snapshots.After = lines
		} else {
			snapshots.Before = lines
		}

		cluster.report.Metrics[target.name] = snapshots
		cluster.report.mu.Unlock()
	}
}

// getJSON GETs address and decodes its JSON body into out, recording the
// request under kind.
func (cluster *cluster) getJSON(ctx context.Context, address, kind string, out any) error {
	body, err := cluster.get(ctx, address, kind)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", address, err)
	}

	return nil
}

// get GETs address, with the token when one is set, and returns the body
// of a 200 response. The request is recorded under kind.
func (cluster *cluster) get(ctx context.Context, address, kind string) ([]byte, error) {
	started := time.Now()
	body, err := cluster.fetch(ctx, address)
	cluster.report.observe(kind, time.Since(started), failureKind(err))

	return body, err
}

// fetch does the work of get.
func (cluster *cluster) fetch(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}

	if cluster.token != "" {
		request.Header.Set("Authorization", "Bearer "+cluster.token)
	}

	response, err := cluster.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	if response.StatusCode != http.StatusOK {
		return nil, &statusError{address: address, status: response.StatusCode}
	}

	return body, nil
}

// statusError is a response other than 200 from a plain GET.
type statusError struct {
	address string
	status  int
}

// Error names the address and status.
func (statusErr *statusError) Error() string {
	return fmt.Sprintf("GET %s: status %d", statusErr.address, statusErr.status)
}

// failureKind names the kind of err for the request counts: empty for
// success, the status for an HTTP answer, or the kind of network failure.
func failureKind(err error) string {
	if err == nil {
		return ""
	}

	var apiError *client.Error
	if errors.As(err, &apiError) {
		return "status " + strconv.Itoa(apiError.StatusCode)
	}

	var statusErr *statusError
	if errors.As(err, &statusErr) {
		return "status " + strconv.Itoa(statusErr.status)
	}

	var wrongErr *wrongInstanceError
	if errors.As(err, &wrongErr) {
		return "wrong instance"
	}

	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Timeout() {
		return "timeout"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	var opError *net.OpError
	if errors.As(err, &opError) {
		return "network " + opError.Op
	}

	return "other"
}

// waitUntil calls condition every interval until it returns nil, and
// returns its last error if that has not happened within timeout.
func waitUntil(ctx context.Context, timeout, interval time.Duration, condition func() error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		err := condition()
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("not within %s: %w", timeout, err)
		case <-ticker.C:
		}
	}
}
