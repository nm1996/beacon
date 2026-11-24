package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nm1996/beacon/client"
)

const (
	// probeService names the instance storage-down registers for itself, to
	// send heartbeats with. It is not one of the expected services.
	probeService = "simulation-probe"

	// flushAllowance is how long a node may keep accepting heartbeats after
	// PostgreSQL went away: two flush intervals of 1s, as configured in
	// compose.yaml, plus one poll for when the runner looked.
	flushAllowance = 2*time.Second + pollInterval
)

// outage collects what storage-down saw, poll by poll.
type outage struct {
	// downAt is when the first lookup failed: the moment the runner first
	// saw PostgreSQL gone.
	downAt time.Time
	// servedAgain is when each node's lookups first succeeded again.
	servedAgain map[string]time.Time
	// backAt is when every node first returned every expected instance
	// after serving again.
	backAt time.Time

	lookupKinds     map[string]int
	lateHeartbeats  []string
	refused         int
	unhealthy       []string
	servedInOutage  int
	heartbeatErrors map[string]int
}

// runStorageDown is scenario E: PostgreSQL is stopped at options.since and
// started again options.outage later. While it is down, lookups must fail
// with 500, heartbeats must be refused within two flush intervals, and
// /health must stay 200. Once it is back, every node must serve again
// within options.recover, without a restart, and every instance must be
// back within options.within of that.
func runStorageDown(ctx context.Context, cluster *cluster, options options) error {
	if _, ok := cluster.settle(ctx, options); !ok {
		return nil
	}

	probe, err := cluster.nodes[0].client.Register(ctx, client.Registration{Name: probeService, Address: "simulation-probe", Port: 1})
	if err != nil {
		return fmt.Errorf("register the probe: %w", err)
	}

	defer func() { _ = cluster.nodes[0].client.Unregister(context.WithoutCancel(ctx), probe.ID) }()

	cluster.readyBySince(options)

	// Send heartbeats through every node until the outage starts, so each
	// node knows the probe and answers its heartbeats from memory.
	for time.Now().Before(options.since) {
		for _, target := range cluster.nodes {
			_ = target.client.Heartbeat(ctx, probe.ID)
		}

		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
	}

	seen := &outage{
		servedAgain:     map[string]time.Time{},
		lookupKinds:     map[string]int{},
		heartbeatErrors: map[string]int{},
	}
	restartAt := options.since.Add(options.outage)
	deadline := restartAt.Add(options.recover + options.within + observeGrace)

	for time.Now().Before(deadline) && seen.backAt.IsZero() {
		cluster.pollOutage(ctx, options, probe.ID, restartAt, seen)

		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
	}

	cluster.reportOutage(ctx, options, restartAt, seen)

	return nil
}

// pollOutage looks at every node once and adds what it saw to seen.
func (cluster *cluster) pollOutage(ctx context.Context, options options, probeID string, restartAt time.Time, seen *outage) {
	services := sortedKeys(options.expected)

	// Each answer is timed when it arrives, since one slow node must not
	// make a later answer from another look earlier than it was.
	for _, target := range cluster.nodes {
		if err := cluster.health(ctx, target); err != nil {
			seen.unhealthy = append(seen.unhealthy, fmt.Sprintf("%s at %s: %v", target.name, time.Since(options.since).Round(time.Millisecond), err))
		}

		lookupErr := cluster.lookOnce(ctx, target, services)
		now := time.Now()

		switch {
		case lookupErr != nil:
			if seen.downAt.IsZero() {
				seen.downAt = now
			}

			seen.lookupKinds[failureKind(lookupErr)]++
		case !seen.downAt.IsZero() && now.Before(restartAt):
			seen.servedInOutage++
		case !seen.downAt.IsZero():
			if _, ok := seen.servedAgain[target.name]; !ok {
				seen.servedAgain[target.name] = now
			}
		}

		// Before PostgreSQL is back, a heartbeat answered 204 long after it
		// went away was acknowledged without being stored.
		heartbeatErr := target.client.Heartbeat(ctx, probeID)
		now = time.Now()

		if heartbeatErr != nil {
			seen.heartbeatErrors[failureKind(heartbeatErr)]++
			seen.refused++
		} else if !seen.downAt.IsZero() && now.Before(restartAt) && now.Sub(seen.downAt) > flushAllowance {
			seen.lateHeartbeats = append(seen.lateHeartbeats, fmt.Sprintf("%s %s after the first failed lookup", target.name, now.Sub(seen.downAt).Round(time.Millisecond)))
		}
	}

	if len(seen.servedAgain) < len(cluster.nodes) || !seen.backAt.IsZero() {
		return
	}

	if looked, err := cluster.look(ctx, services); err == nil && expectCounts(looked, options.expected) == nil {
		seen.backAt = time.Now()
		cluster.record(looked)
	}
}

// lookOnce discovers every service through target and returns the first
// error.
func (cluster *cluster) lookOnce(ctx context.Context, target node, services []string) error {
	for _, service := range services {
		if _, err := cluster.discover(ctx, target, service); err != nil {
			return err
		}
	}

	return nil
}

// reportOutage turns what storage-down saw into steps and timings.
func (cluster *cluster) reportOutage(ctx context.Context, options options, restartAt time.Time, seen *outage) {
	now := time.Now()
	report := cluster.report

	if !seen.downAt.IsZero() {
		report.timing("lookups_failing_after", seen.downAt.Sub(options.since))
	}

	var lookupErr error

	switch {
	case seen.downAt.IsZero():
		lookupErr = fmt.Errorf("no lookup failed")
	case len(seen.lookupKinds) != 1 || seen.lookupKinds["status 500"] == 0:
		lookupErr = fmt.Errorf("lookups failed with %v, want only status 500", seen.lookupKinds)
	case seen.servedInOutage > 0:
		lookupErr = fmt.Errorf("%d lookups succeeded during the outage", seen.servedInOutage)
	}

	report.check("lookups fail with 500 while PostgreSQL is down", now, lookupErr)

	var heartbeatErr error

	switch {
	case len(seen.lateHeartbeats) > 0:
		heartbeatErr = fmt.Errorf("answered 204: %s", strings.Join(seen.lateHeartbeats, "; "))
	case seen.heartbeatErrors["status 500"] == 0:
		heartbeatErr = fmt.Errorf("no heartbeat was refused with 500; errors %v", seen.heartbeatErrors)
	}

	report.check(fmt.Sprintf("heartbeats are refused within %s", flushAllowance), now, heartbeatErr)
	report.check("/health stays 200 on every node", now, joined(seen.unhealthy))

	var servedErr []string

	for _, target := range cluster.nodes {
		served, ok := seen.servedAgain[target.name]

		switch {
		case !ok:
			servedErr = append(servedErr, target.name+" did not serve again")
		case served.Sub(restartAt) > options.recover:
			servedErr = append(servedErr, fmt.Sprintf("%s served again %s after the restart", target.name, served.Sub(restartAt).Round(time.Millisecond)))
		}

		if ok {
			report.timing("served_again_"+target.name, served.Sub(restartAt))
		}
	}

	report.check(fmt.Sprintf("every node serves again within %s of PostgreSQL starting", options.recover), now, joined(servedErr))

	var backErr error

	if seen.backAt.IsZero() {
		backErr = fmt.Errorf("not every instance came back")
	} else {
		last := latest(seen.servedAgain)
		report.timing("instances_back_after_serving", seen.backAt.Sub(last))

		if seen.backAt.Sub(last) > options.within {
			backErr = fmt.Errorf("back %s after the nodes served again", seen.backAt.Sub(last).Round(time.Millisecond))
		}
	}

	report.check(fmt.Sprintf("every instance is back within %s", options.within), now, backErr)

	cluster.snapshotMetrics(ctx, true)
	report.check("every node counts failed heartbeat writes", now, cluster.flushErrorsRose())
}

// latest returns the latest of times.
func latest(times map[string]time.Time) time.Time {
	var last time.Time

	for _, moment := range times {
		if moment.After(last) {
			last = moment
		}
	}

	return last
}

// flushErrorsRose fails unless beacon_heartbeat_flushes_total with result
// error is higher on every node after the run than before it.
func (cluster *cluster) flushErrorsRose() error {
	cluster.report.mu.Lock()
	defer cluster.report.mu.Unlock()

	var problems []string

	for _, target := range cluster.nodes {
		snapshots := cluster.report.Metrics[target.name]
		before, after := flushErrors(snapshots.Before), flushErrors(snapshots.After)

		if after <= before {
			problems = append(problems, fmt.Sprintf("%s: %v before, %v after", target.name, before, after))
		}
	}

	return joined(problems)
}

// flushErrors returns the value of beacon_heartbeat_flushes_total with
// result error in lines, or 0 if it is not there.
func flushErrors(lines []string) float64 {
	for _, line := range lines {
		if !strings.HasPrefix(line, `beacon_heartbeat_flushes_total{result="error"}`) {
			continue
		}

		fields := strings.Fields(line)

		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			return value
		}
	}

	return 0
}
