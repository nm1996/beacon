package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nm1996/beacon/client"
)

// pollInterval is how often scenarios look at the cluster while waiting
// for something to happen.
const pollInterval = 250 * time.Millisecond

// view is what every node returned for every service at one moment:
// node name, then service name, then the instances sorted by ID.
type view map[string]map[string][]client.Instance

// look discovers every service in services through every node.
func (cluster *cluster) look(ctx context.Context, services []string) (view, error) {
	seen := view{}

	for _, target := range cluster.nodes {
		seen[target.name] = map[string][]client.Instance{}

		for _, service := range services {
			instances, err := cluster.discover(ctx, target, service)
			if err != nil {
				return nil, fmt.Errorf("discover %s through %s: %w", service, target.name, err)
			}

			sort.Slice(instances, func(left, right int) bool { return instances[left].ID < instances[right].ID })
			seen[target.name][service] = instances
		}
	}

	return seen, nil
}

// record stores seen in the report as the last look.
func (cluster *cluster) record(seen view) {
	recorded := map[string]map[string][]Seen{}

	for nodeName, services := range seen {
		recorded[nodeName] = map[string][]Seen{}

		for service, instances := range services {
			listed := make([]Seen, 0, len(instances))
			for _, instance := range instances {
				listed = append(listed, Seen{ID: instance.ID, Address: instance.Address, Port: instance.Port})
			}

			recorded[nodeName][service] = listed
		}
	}

	cluster.report.mu.Lock()
	cluster.report.Seen = recorded
	cluster.report.mu.Unlock()
}

// expectCounts fails unless every node returns expected[service]
// instances of every service.
func expectCounts(seen view, expected map[string]int) error {
	var problems []string

	for _, nodeName := range sortedKeys(seen) {
		for _, service := range sortedKeys(expected) {
			if got := len(seen[nodeName][service]); got != expected[service] {
				problems = append(problems, fmt.Sprintf("%s returns %d %s, want %d", nodeName, got, service, expected[service]))
			}
		}
	}

	return joined(problems)
}

// expectConsistent fails if the nodes disagree on any service, or if one
// returns an instance twice, an empty ID, or two instances at one address.
func expectConsistent(seen view) error {
	var problems []string

	nodeNames := sortedKeys(seen)

	for _, nodeName := range nodeNames {
		for _, service := range sortedKeys(seen[nodeName]) {
			problems = append(problems, wellFormed(nodeName, service, seen[nodeName][service])...)
		}
	}

	if len(nodeNames) < 2 {
		return joined(problems)
	}

	first := nodeNames[0]

	for _, nodeName := range nodeNames[1:] {
		for _, service := range sortedKeys(seen[first]) {
			want := identities(seen[first][service])
			if got := identities(seen[nodeName][service]); !slices.Equal(got, want) {
				problems = append(problems, fmt.Sprintf("%s: %s returns %v, %s returns %v", service, nodeName, got, first, want))
			}
		}
	}

	return joined(problems)
}

// wellFormed lists what is wrong with the instances one node returned for
// one service.
func wellFormed(nodeName, service string, instances []client.Instance) []string {
	var problems []string

	ids := map[string]bool{}
	addresses := map[string]bool{}

	for _, instance := range instances {
		address := fmt.Sprintf("%s:%d", instance.Address, instance.Port)

		switch {
		case instance.ID == "":
			problems = append(problems, fmt.Sprintf("%s returns a %s instance without an ID", nodeName, service))
		case ids[instance.ID]:
			problems = append(problems, fmt.Sprintf("%s returns %s %s twice", nodeName, service, instance.ID))
		case addresses[address]:
			problems = append(problems, fmt.Sprintf("%s returns two %s instances at %s", nodeName, service, address))
		case instance.Name != service:
			problems = append(problems, fmt.Sprintf("%s returns %s as %s", nodeName, instance.ID, instance.Name))
		}

		ids[instance.ID] = true
		addresses[address] = true
	}

	return problems
}

// identities lists instances as "id@address:port", in order.
func identities(instances []client.Instance) []string {
	listed := make([]string, 0, len(instances))
	for _, instance := range instances {
		listed = append(listed, fmt.Sprintf("%s@%s:%d", instance.ID, instance.Address, instance.Port))
	}

	return listed
}

// expectNodes fails unless every node in targets lists exactly want as the
// live nodes.
func (cluster *cluster) expectNodes(ctx context.Context, targets []node, want []string) error {
	want = slices.Sorted(slices.Values(want))

	var problems []string

	for _, target := range targets {
		listed, err := cluster.listNodes(ctx, target)
		if err != nil {
			return fmt.Errorf("list nodes through %s: %w", target.name, err)
		}

		slices.Sort(listed)

		if !slices.Equal(listed, want) {
			problems = append(problems, fmt.Sprintf("%s lists %v, want %v", target.name, listed, want))
		}
	}

	return joined(problems)
}

// nodeNames lists the names of nodes.
func nodeNames(nodes []node) []string {
	names := make([]string, 0, len(nodes))
	for _, target := range nodes {
		names = append(names, target.name)
	}

	return names
}

// sortedKeys returns the keys of values in order.
func sortedKeys[Value any](values map[string]Value) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for key := range values {
			if !yield(key) {
				return
			}
		}
	})
}

// joined returns problems as one error, or nil when there are none.
func joined(problems []string) error {
	if len(problems) == 0 {
		return nil
	}

	return errors.New(strings.Join(problems, "; "))
}
