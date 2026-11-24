package main

import (
	"context"
	"time"

	"github.com/nm1996/beacon/client"
)

// runNormal is scenario A: with everything running, every node lists both
// nodes and returns the same instances, and every instance answers as the
// instance Beacon returned for it.
func runNormal(ctx context.Context, cluster *cluster, options options) error {
	seen, ok := cluster.settle(ctx, options)
	if !ok {
		return nil
	}

	services := sortedKeys(options.expected)

	started := time.Now()
	cluster.report.check("every node returns the same instances", started, expectConsistent(seen))

	started = time.Now()

	for range options.rounds {
		if _, err := cluster.look(ctx, services); err != nil {
			cluster.report.check("repeated lookups succeed", started, err)
			return nil
		}
	}

	cluster.report.check("repeated lookups succeed", started, nil)

	started = time.Now()
	cluster.report.check("every instance answers as itself", started, cluster.callAll(ctx, seen, options.rounds))

	return nil
}

// settle waits until every node is healthy, lists every node, and returns
// the expected instances, recording a step for each. It returns the last
// look, and false if any of them failed to happen within options.timeout.
func (cluster *cluster) settle(ctx context.Context, options options) (view, bool) {
	started := time.Now()

	err := waitUntil(ctx, options.timeout, pollInterval, func() error {
		for _, target := range cluster.nodes {
			if err := cluster.health(ctx, target); err != nil {
				return err
			}
		}

		return nil
	})
	if !cluster.report.check("every node is healthy", started, err) {
		return nil, false
	}

	started = time.Now()
	err = waitUntil(ctx, options.timeout, pollInterval, func() error {
		return cluster.expectNodes(ctx, cluster.nodes, nodeNames(cluster.nodes))
	})

	if !cluster.report.check("every node lists every node", started, err) {
		return nil, false
	}

	var seen view

	started = time.Now()
	err = waitUntil(ctx, options.timeout, pollInterval, func() error {
		var lookErr error
		if seen, lookErr = cluster.look(ctx, sortedKeys(options.expected)); lookErr != nil {
			return lookErr
		}

		return expectCounts(seen, options.expected)
	})

	cluster.report.timing("settle", time.Since(started))

	if seen != nil {
		cluster.record(seen)
	}

	if !cluster.report.check("every node returns the expected instances", started, err) {
		return nil, false
	}

	return seen, true
}

// callAll calls every instance the first node returned, rounds times each,
// and returns the first failure.
func (cluster *cluster) callAll(ctx context.Context, seen view, rounds int) error {
	first := cluster.nodes[0].name

	for range rounds {
		for _, instances := range seen[first] {
			for _, instance := range instances {
				if err := cluster.callOnce(ctx, instance); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// callOnce calls instance's /work within requestTimeout.
func (cluster *cluster) callOnce(ctx context.Context, instance client.Instance) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	return cluster.work(ctx, instance, "work")
}
