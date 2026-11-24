package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nm1996/beacon/client"
)

// runOwnership is scenario F, with authentication on: using another
// service's token, heartbeats and unregistering of options.service's
// instances must be refused with 403 instance_not_owned through every
// node, every time, and the instances must stay registered.
func runOwnership(ctx context.Context, cluster *cluster, options options) error {
	seen, ok := cluster.settle(ctx, options)
	if !ok {
		return nil
	}

	targets := seen[cluster.nodes[0].name][options.service]
	if len(targets) == 0 {
		return fmt.Errorf("no %s instance to attack", options.service)
	}

	var allowed []string

	started := time.Now()

	for _, target := range cluster.nodes {
		attacker, err := client.New(target.url, client.WithHTTPClient(cluster.http), client.WithToken(options.attackerToken))
		if err != nil {
			return err
		}

		for range options.rounds {
			for _, instance := range targets {
				allowed = append(allowed, cluster.attack(ctx, attacker, target.name, instance)...)
			}
		}
	}

	cluster.report.check(fmt.Sprintf("another caller cannot change %s on any node", options.service), started, joined(allowed))

	started = time.Now()
	after, err := cluster.look(ctx, sortedKeys(options.expected))

	if err == nil {
		cluster.record(after)
		err = expectCounts(after, options.expected)
	}

	cluster.report.check("every instance is still registered", started, err)

	return nil
}

// attack sends one heartbeat and one unregister for instance through
// attacker, and lists every answer other than 403 instance_not_owned.
func (cluster *cluster) attack(ctx context.Context, attacker *client.Client, nodeName string, instance client.Instance) []string {
	var allowed []string

	started := time.Now()
	err := attacker.Heartbeat(ctx, instance.ID)
	cluster.report.observe("foreign heartbeat", time.Since(started), notOwnedKind(err))

	if !errors.Is(err, client.ErrNotOwned) {
		allowed = append(allowed, fmt.Sprintf("heartbeat of %s through %s: %v", instance.ID, nodeName, answer(err)))
	}

	started = time.Now()
	err = attacker.Unregister(ctx, instance.ID)
	cluster.report.observe("foreign unregister", time.Since(started), notOwnedKind(err))

	if !errors.Is(err, client.ErrNotOwned) {
		allowed = append(allowed, fmt.Sprintf("unregister of %s through %s: %v", instance.ID, nodeName, answer(err)))
	}

	return allowed
}

// notOwnedKind counts a refusal for not owning the instance as the
// expected outcome, and anything else as an error.
func notOwnedKind(err error) string {
	if errors.Is(err, client.ErrNotOwned) {
		return ""
	}

	if err == nil {
		return "accepted"
	}

	return failureKind(err)
}

// answer describes err for a step's detail, naming success plainly.
func answer(err error) string {
	if err == nil {
		return "accepted"
	}

	return err.Error()
}
