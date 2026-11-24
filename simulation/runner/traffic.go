package main

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/nm1996/beacon/client"
)

// trafficCallTimeout bounds each call traffic makes. It is shorter than
// requestTimeout so a dead instance fails fast enough to be ejected while
// the scenario watches.
const trafficCallTimeout = time.Second

// traffic calls one service the way an application does: it picks an
// instance through a client.Resolver, calls it, and reports the outcome
// back, so outlier detection skips instances that fail.
type traffic struct {
	cluster  *cluster
	resolver *client.Resolver
	stop     context.CancelFunc
	done     chan struct{}

	mu sync.Mutex
	// failures counts failed calls by host:port, and calls every call.
	failures map[string]int
	calls    map[string]int
}

// startTraffic calls service through target every interval until the
// returned traffic is stopped. Each call is recorded in the report as
// "resolved <host:port>", and a failed pick as "resolved".
func (cluster *cluster) startTraffic(ctx context.Context, target node, service string, interval time.Duration) (*traffic, error) {
	resolver, err := target.client.NewResolver(ctx, service,
		client.WithRefreshInterval(time.Second),
		client.WithOutlierDetection(client.OutlierDetection{ConsecutiveFailures: 2, EjectionTime: 10 * time.Second}),
	)
	if err != nil {
		return nil, err
	}

	ctx, stop := context.WithCancel(ctx)
	caller := &traffic{
		cluster:  cluster,
		resolver: resolver,
		stop:     stop,
		done:     make(chan struct{}),
		failures: map[string]int{},
		calls:    map[string]int{},
	}

	go caller.run(ctx, interval)

	return caller, nil
}

// run makes one call every interval until ctx ends.
func (caller *traffic) run(ctx context.Context, interval time.Duration) {
	defer close(caller.done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		instance, err := caller.resolver.Pick()
		if err != nil {
			caller.cluster.report.observe("resolved", 0, failureKind(err))
			continue
		}

		// A call in flight when traffic stops finishes, so stopping never
		// shows up as a failed call.
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficCallTimeout)
		address := net.JoinHostPort(instance.Address, strconv.Itoa(instance.Port))
		err = caller.cluster.work(callCtx, instance, "resolved "+address)

		cancel()
		caller.resolver.Report(instance, err)

		caller.mu.Lock()
		caller.calls[address]++
		if err != nil {
			caller.failures[address]++
		}
		caller.mu.Unlock()
	}
}

// close stops the calls and the Resolver, and waits for both.
func (caller *traffic) close() {
	caller.stop()
	<-caller.done
	caller.resolver.Close()
}

// counts returns the calls and failures so far, by host:port.
func (caller *traffic) counts() (calls, failures map[string]int) {
	caller.mu.Lock()
	defer caller.mu.Unlock()

	calls = make(map[string]int, len(caller.calls))
	for address, count := range caller.calls {
		calls[address] = count
	}

	failures = make(map[string]int, len(caller.failures))
	for address, count := range caller.failures {
		failures[address] = count
	}

	return calls, failures
}
