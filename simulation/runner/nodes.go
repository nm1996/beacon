package main

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// runNodeDown is scenario C: the node options.down was stopped or killed
// at options.since. For options.watch afterwards, every expected instance
// must stay registered on the other nodes, since its heartbeats now reach
// them through the load balancer, and calls to every instance must
// succeed. The other nodes must stop listing it within options.within.
func runNodeDown(ctx context.Context, cluster *cluster, options options) error {
	survivors := slices.DeleteFunc(slices.Clone(cluster.nodes), func(target node) bool { return target.name == options.down })
	if len(survivors) == len(cluster.nodes) || len(survivors) == 0 {
		return fmt.Errorf("-down %q must name one of several -nodes", options.down)
	}

	// Lookups and calls go through the survivors only; the stopped node is
	// expected to fail.
	watched := cluster.only(survivors)
	services := sortedKeys(options.expected)

	callers := make([]*traffic, 0, len(services))

	for _, service := range services {
		caller, err := watched.startTraffic(ctx, survivors[0], service, trafficInterval)
		if err != nil {
			for _, started := range callers {
				started.close()
			}

			return fmt.Errorf("start calling %s: %w", service, err)
		}

		callers = append(callers, caller)
	}

	if err := cluster.awaitSince(ctx, options); err != nil {
		for _, caller := range callers {
			caller.close()
		}

		return err
	}

	left := newChange()
	deadline := options.since.Add(options.watch)
	survivorNames := nodeNames(survivors)

	var (
		missing  error
		lookErrs int
		nodeErrs int
		seen     view
		err      error
	)

	for poll := 0; time.Now().Before(deadline); poll++ {
		seen, err = watched.look(ctx, services)
		if err != nil {
			lookErrs++
		} else if countErr := expectCounts(seen, options.expected); countErr != nil && missing == nil {
			missing = fmt.Errorf("%w after %s", countErr, time.Since(options.since).Round(time.Millisecond))
		}

		for _, target := range survivors {
			listed, listErr := watched.listNodes(ctx, target)

			switch {
			case listErr != nil:
				nodeErrs++
			case sameNames(listed, survivorNames):
				left.note(target.name, poll, time.Since(options.since))
			}
		}

		if err := sleep(ctx, pollInterval); err != nil {
			break
		}
	}

	for _, caller := range callers {
		caller.close()
	}

	if seen != nil {
		watched.record(seen)
	}

	left.record(cluster.report, "unlisted")

	now := time.Now()
	cluster.report.check(fmt.Sprintf("every instance stays registered for %s", options.watch), now, missing)
	cluster.report.check("lookups through the other nodes succeed throughout", now, countErr(lookErrs+nodeErrs, "requests failed"))
	cluster.report.check(fmt.Sprintf("the other nodes stop listing %s within %s", options.down, options.within), now,
		left.within(survivorNames, options.within, "unlisted"))

	for index, caller := range callers {
		cluster.report.check(fmt.Sprintf("every %s instance answers throughout", services[index]), now, othersAnswered(caller, ""))
	}

	return ctx.Err()
}

// sameNames reports whether listed and want hold the same names, in any
// order.
func sameNames(listed, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(listed)), slices.Sorted(slices.Values(want)))
}
