package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nm1996/beacon/client"
)

const (
	// observeGrace is how long past the bound a scenario keeps watching,
	// so a late change is measured instead of only reported as missing.
	observeGrace = 3 * time.Second

	// trafficInterval is the time between calls the traffic makes.
	trafficInterval = 100 * time.Millisecond
)

// targetAt returns the instance at address that a node returned, if any.
func targetAt(instances []client.Instance, address string) (client.Instance, bool) {
	index := slices.IndexFunc(instances, func(instance client.Instance) bool { return at(instance, address) })
	if index < 0 {
		return client.Instance{}, false
	}

	return instances[index], true
}

// at reports whether instance is at address: a host, as Compose service
// names are, or host:port.
func at(instance client.Instance, address string) bool {
	if host, port, err := net.SplitHostPort(address); err == nil {
		return instance.Address == host && strconv.Itoa(instance.Port) == port
	}

	return instance.Address == address
}

// expectOthers fails unless every node returns every expected instance
// apart from the one of service at address, whether or not that one is
// still there.
func expectOthers(seen view, expected map[string]int, service, address string) error {
	var problems []string

	for _, nodeName := range sortedKeys(seen) {
		for _, name := range sortedKeys(expected) {
			want := expected[name]
			if name == service {
				want--
			}

			got := 0

			for _, instance := range seen[nodeName][name] {
				if name != service || !at(instance, address) {
					got++
				}
			}

			if got != want {
				problems = append(problems, fmt.Sprintf("%s returns %d other %s, want %d", nodeName, got, name, want))
			}
		}
	}

	return joined(problems)
}

// change is when each node was first seen to change, counted in polls and
// as time since the scenario's reference moment.
type change struct {
	poll  map[string]int
	after map[string]time.Duration
}

// newChange returns an empty change.
func newChange() change {
	return change{poll: map[string]int{}, after: map[string]time.Duration{}}
}

// note records that nodeName changed at poll, unless it already did.
func (seenChange change) note(nodeName string, poll int, after time.Duration) {
	if _, ok := seenChange.poll[nodeName]; !ok {
		seenChange.poll[nodeName] = poll
		seenChange.after[nodeName] = after
	}
}

// complete reports whether every node in nodeNames has changed.
func (seenChange change) complete(nodeNames []string) bool {
	for _, nodeName := range nodeNames {
		if _, ok := seenChange.poll[nodeName]; !ok {
			return false
		}
	}

	return true
}

// within fails unless every node in nodeNames changed within bound.
func (seenChange change) within(nodeNames []string, bound time.Duration, what string) error {
	var problems []string

	for _, nodeName := range nodeNames {
		after, ok := seenChange.after[nodeName]

		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %s not seen within %s", nodeName, what, bound+observeGrace))
		case after > bound:
			problems = append(problems, fmt.Sprintf("%s: %s after %s, bound %s", nodeName, what, after.Round(time.Millisecond), bound))
		}
	}

	return joined(problems)
}

// agree fails if the nodes changed more than one poll apart.
func (seenChange change) agree() error {
	if len(seenChange.poll) == 0 {
		return errors.New("no node changed")
	}

	polls := make([]int, 0, len(seenChange.poll))
	for _, poll := range seenChange.poll {
		polls = append(polls, poll)
	}

	if spread := slices.Max(polls) - slices.Min(polls); spread > 1 {
		return fmt.Errorf("the nodes changed %d polls apart", spread)
	}

	return nil
}

// record stores each node's change in the report as name_<node>.
func (seenChange change) record(report *Report, name string) {
	for nodeName, after := range seenChange.after {
		report.timing(name+"_"+nodeName, after)
	}
}

// runGone is scenario B: an instance was killed or stopped at
// options.since. It must leave discovery on every node within
// options.within, at about the same poll everywhere, while every other
// instance stays registered and keeps answering calls made through a
// Resolver with outlier detection.
func runGone(ctx context.Context, cluster *cluster, options options) error {
	services := sortedKeys(options.expected)
	nodes := nodeNames(cluster.nodes)

	caller, err := cluster.startTraffic(ctx, cluster.nodes[0], options.service, trafficInterval)
	if err != nil {
		return fmt.Errorf("start calling %s: %w", options.service, err)
	}

	if err := cluster.awaitSince(ctx, options); err != nil {
		caller.close()
		return err
	}

	gone := newChange()
	deadline := options.since.Add(options.within + observeGrace)

	var (
		othersErr error
		lookErrs  int
		seen      view
	)

	for poll := 0; time.Now().Before(deadline) && !gone.complete(nodes); poll++ {
		seen, err = cluster.look(ctx, services)
		if err != nil {
			lookErrs++
		} else {
			for _, nodeName := range nodes {
				if instance, present := targetAt(seen[nodeName][options.service], options.address); present {
					cluster.setTarget(options.service, options.address, instance.ID)
				} else {
					gone.note(nodeName, poll, time.Since(options.since))
				}
			}

			if othersErr == nil {
				othersErr = expectOthers(seen, options.expected, options.service, options.address)
			}
		}

		if sleep(ctx, pollInterval) != nil {
			break
		}
	}

	caller.close()

	if seen != nil {
		cluster.record(seen)
	}

	gone.record(cluster.report, "gone")

	now := time.Now()
	cluster.report.check(fmt.Sprintf("%s leaves discovery on every node within %s", options.address, options.within), now,
		gone.within(nodes, options.within, "left"))
	cluster.report.check("the nodes agree within one poll", now, gone.agree())
	cluster.report.check("every other instance stays registered", now, othersErr)
	cluster.report.check("lookups succeed throughout", now, countErr(lookErrs, "lookups failed"))
	cluster.report.check("every other instance answers throughout", now, othersAnswered(caller, options.address))

	return ctx.Err()
}

// runBack is scenario D: an instance was started again at options.since.
// It must be back on every node within options.within, at about the same
// poll everywhere, under a new ID, or under options.wantID when that is
// set, and must answer as itself. The ID it had before, options.oldID,
// must never be returned again.
func runBack(ctx context.Context, cluster *cluster, options options) error {
	if err := cluster.awaitSince(ctx, options); err != nil {
		return err
	}

	services := sortedKeys(options.expected)
	nodes := nodeNames(cluster.nodes)
	back := newChange()
	deadline := options.since.Add(options.within + observeGrace)

	var (
		oldSeen  bool
		returned = map[string]client.Instance{}
		lookErrs int
		seen     view
		err      error
	)

	// Keep watching a few polls after the instance is back, so an old ID
	// that reappears late is still caught.
	extra := 4

	for poll := 0; time.Now().Before(deadline) && extra > 0; poll++ {
		seen, err = cluster.look(ctx, services)
		if err != nil {
			lookErrs++
		} else {
			for _, nodeName := range nodes {
				instance, present := targetAt(seen[nodeName][options.service], options.address)
				if !present {
					continue
				}

				if options.wantID == "" && options.oldID != "" && instance.ID == options.oldID {
					oldSeen = true
				}

				returned[nodeName] = instance
				back.note(nodeName, poll, time.Since(options.since))
			}
		}

		if back.complete(nodes) {
			extra--
		}

		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
	}

	if seen != nil {
		cluster.record(seen)
	}

	back.record(cluster.report, "back")

	now := time.Now()
	cluster.report.check(fmt.Sprintf("%s is back on every node within %s", options.address, options.within), now,
		back.within(nodes, options.within, "back"))
	cluster.report.check("the nodes agree within one poll", now, back.agree())
	cluster.report.check("lookups succeed throughout", now, countErr(lookErrs, "lookups failed"))

	instance, ok := returned[nodes[0]]
	if !ok {
		return nil
	}

	cluster.setTarget(options.service, options.address, instance.ID)

	switch {
	case options.wantID != "":
		cluster.report.check(fmt.Sprintf("%s comes back as %s", options.address, options.wantID), now,
			sameID(instance.ID, options.wantID))
	case options.oldID != "":
		cluster.report.check(fmt.Sprintf("%s comes back under a new ID", options.address), now,
			newID(instance.ID, options.oldID))
		cluster.report.check("the old ID never returns", now, neverSeen(oldSeen, options.oldID))
	}

	started := time.Now()
	cluster.report.check(fmt.Sprintf("%s answers as itself", options.address), started,
		waitUntil(ctx, options.within, pollInterval, func() error { return cluster.callOnce(ctx, instance) }))

	return nil
}

// othersAnswered fails if any call to an instance other than the one at
// address failed, if the Resolver could not pick an instance, or if no
// call was made.
func othersAnswered(caller *traffic, address string) error {
	calls, failures, pickFailures := caller.counts()

	var problems []string

	if pickFailures > 0 {
		problems = append(problems, fmt.Sprintf("the Resolver could not pick an instance %d times", pickFailures))
	}

	made := 0

	for _, other := range sortedKeys(calls) {
		if other == address || strings.HasPrefix(other, address+":") {
			continue
		}

		made += calls[other]

		if failures[other] > 0 {
			problems = append(problems, fmt.Sprintf("%d of %d calls to %s failed, first with %v",
				failures[other], calls[other], other, caller.failureOf(other)))
		}
	}

	if made == 0 {
		problems = append(problems, "no call reached another instance")
	}

	return joined(problems)
}

// sameID fails unless got is want.
func sameID(got, want string) error {
	if got != want {
		return fmt.Errorf("came back as %s, want %s", got, want)
	}

	return nil
}

// newID fails if got is the old ID.
func newID(got, old string) error {
	if got == old {
		return fmt.Errorf("came back as the old ID %s", old)
	}

	return nil
}

// neverSeen fails if the old ID was seen.
func neverSeen(seen bool, old string) error {
	if seen {
		return fmt.Errorf("%s was returned again", old)
	}

	return nil
}

// countErr fails if count is not zero.
func countErr(count int, what string) error {
	if count > 0 {
		return fmt.Errorf("%d %s", count, what)
	}

	return nil
}

// awaitSince waits until options.since when it was still to come as the
// runner started, recording whether the runner got ready in time: run.sh
// acts at that moment, so whatever the runner set up after it would miss
// the start of what it measures.
func (cluster *cluster) awaitSince(ctx context.Context, options options) error {
	cluster.readyBySince(options)

	return sleep(ctx, time.Until(options.since))
}

// readyBySince records whether the runner got ready before options.since,
// when that was still to come as the runner started.
func (cluster *cluster) readyBySince(options options) {
	if !options.sinceAhead {
		return
	}

	now := time.Now()

	var late error
	if left := options.since.Sub(now); left < 0 {
		late = fmt.Errorf("ready %s after -since; give run.sh more lead", (-left).Round(time.Millisecond))
	}

	cluster.report.check("the runner is ready before -since", now, late)
}

// sleep waits for delay or until ctx ends.
func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
