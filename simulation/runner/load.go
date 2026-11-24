package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// checkInterval is how often the load scenario checks that every instance
// is still registered.
const checkInterval = time.Second

// runLoad is the load profile: for options.duration, lookups go to the
// nodes in turn at options.lookupRate, calls go to options.service's
// instances through a Resolver at options.callRate, and every second
// every node must still return every expected instance. An instance that
// expires while it is alive under load is a bug.
func runLoad(ctx context.Context, cluster *cluster, options options) error {
	if _, ok := cluster.settle(ctx, options); !ok {
		return nil
	}

	caller, err := cluster.startTraffic(ctx, cluster.nodes[0], options.service, time.Second/time.Duration(options.callRate))
	if err != nil {
		return fmt.Errorf("start calling %s: %w", options.service, err)
	}

	// The run ends after options.duration; ctx itself ends only when the
	// runner is interrupted, which fails the scenario.
	runCtx, stop := context.WithTimeout(ctx, options.duration)
	defer stop()

	var (
		lookups  sync.WaitGroup
		lookErrs int
	)

	lookups.Go(func() { lookErrs = cluster.lookAtRate(runCtx, sortedKeys(options.expected), options.lookupRate) })

	var missing []string

	started := time.Now()
	ticker := time.NewTicker(checkInterval)

	for runCtx.Err() == nil {
		select {
		case <-runCtx.Done():
		case <-ticker.C:
			// A look that has started finishes, so the end of the run does
			// not show up as a failed lookup.
			seen, err := cluster.look(context.WithoutCancel(runCtx), sortedKeys(options.expected))
			if err != nil {
				missing = append(missing, fmt.Sprintf("at %s: %v", time.Since(started).Round(time.Second), err))
				continue
			}

			if countErr := expectCounts(seen, options.expected); countErr != nil {
				missing = append(missing, fmt.Sprintf("at %s: %v", time.Since(started).Round(time.Second), countErr))
			}

			cluster.record(seen)
		}
	}

	ticker.Stop()
	lookups.Wait()
	caller.close()

	cluster.report.timing("duration", time.Since(started))

	now := time.Now()
	cluster.report.check(fmt.Sprintf("every instance stays registered for %s", options.duration), now, firstFew(missing))
	cluster.report.check("every lookup succeeds", now, countErr(lookErrs, "lookups failed"))
	cluster.report.check(fmt.Sprintf("every call to %s succeeds", options.service), now, othersAnswered(caller, ""))

	return ctx.Err()
}

// lookAtRate discovers one of services through one of the nodes, rate
// times a second until ctx ends, and returns how many failed. It goes
// through every node for one service before moving to the next service,
// so every node gets every service.
func (cluster *cluster) lookAtRate(ctx context.Context, services []string, rate int) int {
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()

	failed := 0

	for turn := 0; ; turn++ {
		select {
		case <-ctx.Done():
			return failed
		case <-ticker.C:
		}

		nodeIndex, serviceIndex := lookupPair(turn, len(cluster.nodes), len(services))
		target := cluster.nodes[nodeIndex]
		service := services[serviceIndex]

		lookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
		if _, err := cluster.discover(lookCtx, target, service); err != nil {
			failed++
		}

		cancel()
	}
}

// lookupPair returns which node and which service lookup number turn
// goes to: every node for the first service, then every node for the
// next, so each node gets each service once every nodes×services turns.
func lookupPair(turn, nodes, services int) (nodeIndex, serviceIndex int) {
	return turn % nodes, (turn / nodes) % services
}

// firstFew joins the first five problems, and counts the rest.
func firstFew(problems []string) error {
	const shown = 5

	if len(problems) > shown {
		problems = append(problems[:shown], fmt.Sprintf("and %d more", len(problems)-shown))
	}

	return joined(problems)
}
