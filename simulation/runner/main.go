// Command runner drives one simulation scenario against the Beacon nodes
// and application instances in the Compose project, writes a JSON report,
// prints a summary, and exits non-zero if any check failed.
//
//	docker compose -f simulation/compose.yaml run --rm runner -scenario normal
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// endpoint is one name=URL pair from -nodes.
type endpoint struct {
	name string
	url  string
}

// options is what the command line configures for every scenario.
type options struct {
	scenario string
	nodes    []endpoint
	expected map[string]int
	reports  string
	timeout  time.Duration

	// run and label group reports in the summaries; summarize writes the
	// summaries instead of running a scenario.
	run       string
	label     string
	summarize bool
	rounds    int
	token     string

	// For the scenarios about one instance: which one, when it was
	// killed, stopped or started, and how long the change may take.
	service string
	address string
	since   time.Time
	within  time.Duration
	// sinceAhead is set when -since was still to come as the runner
	// started: run.sh then acts at that moment, and the runner must be
	// ready by then.
	sinceAhead bool
	// oldID is the instance's ID before it was killed; back checks that it
	// never returns. wantID is the ID it must come back with, if fixed.
	oldID  string
	wantID string

	// For node-down: the node that was stopped or killed at since, and how
	// long to watch the others afterwards.
	down  string
	watch time.Duration

	// For storage-down: how long after since PostgreSQL is started again,
	// and how long the nodes then have to serve again.
	outage  time.Duration
	recover time.Duration

	// attackerToken is, for ownership, another service's token.
	attackerToken string

	// For load: how long to run, and how many lookups and calls to make
	// each second.
	duration   time.Duration
	lookupRate int
	callRate   int
}

// scenario runs its steps against cluster, recording each in the report.
type scenario func(ctx context.Context, cluster *cluster, options options) error

// scenarios maps each -scenario name to its implementation.
var scenarios = map[string]scenario{
	"normal": runNormal,
	"gone":   runGone,
	"back":   runBack,

	"node-down":    runNodeDown,
	"storage-down": runStorageDown,
	"ownership":    runOwnership,
	"load":         runLoad,
}

// aboutOneInstance lists the scenarios that need -service and -address.
var aboutOneInstance = []string{"gone", "back"}

// readToken returns the token in path, without surrounding white space,
// or nothing when path is empty.
func readToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	content, err := os.ReadFile(path) //nolint:gosec // The path comes from the command line, by design.
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}

	token := strings.TrimSpace(string(content))
	if token == "" {
		return "", fmt.Errorf("read token: %s is empty", path)
	}

	return token, nil
}

// main runs the scenario and exits 1 if it failed, 2 if it could not run.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)

	stop()
	os.Exit(code)
}

// run parses args, runs the scenario and writes its report, returning the
// exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	parsed, err := parseOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	endpoints := make(map[string]string, len(parsed.nodes))
	for _, target := range parsed.nodes {
		endpoints[target.name] = target.url
	}

	if parsed.summarize {
		if err := writeSummaries(parsed.reports); err != nil {
			fmt.Fprintln(stderr, "summarize:", err)
			return 2
		}

		fmt.Fprintln(stdout, "summaries written to", parsed.reports)

		return 0
	}

	report := newReport(parsed.scenario, endpoints)
	report.Expected = parsed.expected
	report.Run = parsed.run
	report.Label = parsed.label

	cluster, err := newCluster(parsed.nodes, parsed.token, report)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	cluster.snapshotMetrics(ctx, false)

	runErr := scenarios[parsed.scenario](ctx, cluster, parsed)
	if runErr != nil {
		report.check("scenario completed", time.Now(), runErr)
	}

	cluster.snapshotMetrics(context.WithoutCancel(ctx), true)
	report.finish()
	report.print(stdout)

	path, err := report.write(parsed.reports)
	if err != nil {
		fmt.Fprintln(stderr, "write report:", err)
		return 2
	}

	fmt.Fprintln(stdout, "  report:", path)

	if !report.Passed {
		return 1
	}

	return 0
}

// parseOptions reads the flags in args.
func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)

	var (
		parsed       options
		nodes        string
		expected     string
		since        string
		tokenFile    string
		attackerFile string
	)

	flags.StringVar(&parsed.scenario, "scenario", "normal", "scenario to run: "+strings.Join(scenarioNames(), ", "))
	flags.StringVar(&nodes, "nodes", "beacon-a=http://beacon-a:8080,beacon-b=http://beacon-b:8080", "Beacon nodes to observe, as name=URL pairs separated by commas")
	flags.StringVar(&expected, "expect", "orders=2,payments=1,inventory=1", "instances each service should have, as name=count pairs separated by commas")
	flags.StringVar(&parsed.reports, "reports", "/reports", "directory to write the JSON report to, in a folder per day")
	flags.StringVar(&parsed.run, "run", "", "names the run.sh invocation this report belongs to, for the summaries")
	flags.StringVar(&parsed.label, "label", "", "names the run.sh scenario this report belongs to, for the summaries")
	flags.BoolVar(&parsed.summarize, "summarize", false, "instead of running a scenario, write README.md summaries of every report in -reports")
	flags.DurationVar(&parsed.timeout, "timeout", time.Minute, "how long to wait for the cluster to reach the expected state")
	flags.IntVar(&parsed.rounds, "rounds", 20, "how many times to repeat each lookup and call, for latency")
	flags.StringVar(&tokenFile, "token-file", os.Getenv("RUNNER_TOKEN_FILE"), "file holding the runner's bearer token for Beacon; defaults to $RUNNER_TOKEN_FILE, and empty sends none")
	flags.StringVar(&attackerFile, "attacker-token-file", "", "ownership: file holding another service's token, which must not be able to change -service's instances")
	flags.DurationVar(&parsed.outage, "outage", 20*time.Second, "storage-down: how long after -since PostgreSQL is started again")
	flags.DurationVar(&parsed.recover, "recover", 15*time.Second, "storage-down: how long after PostgreSQL starts every node must serve again")
	flags.DurationVar(&parsed.duration, "duration", 5*time.Minute, "load: how long to run")
	flags.IntVar(&parsed.lookupRate, "lookup-rate", 5, "load: lookups per second, spread over the nodes and services")
	flags.IntVar(&parsed.callRate, "call-rate", 20, "load: calls per second to -service's instances")
	flags.StringVar(&parsed.service, "service", "", "gone, back: the service of the instance to watch; ownership: the service whose instances to attack; load: the service to call")
	flags.StringVar(&parsed.address, "address", "", "gone, back: the address of the instance to watch, such as orders-2, or host:port")
	flags.StringVar(&since, "since", "", "gone, back, node-down: when the instance or node is or was killed, stopped or started, as Unix seconds or RFC 3339; empty is now. A time to come is waited for")
	flags.DurationVar(&parsed.within, "within", 9*time.Second, "gone, back: how long after -since the change may take")
	flags.StringVar(&parsed.oldID, "old-id", "", "back: the ID the instance had before; it must not return")
	flags.StringVar(&parsed.wantID, "want-id", "", "back: the ID the instance must come back with")
	flags.StringVar(&parsed.down, "down", "", "node-down: the node that was stopped or killed at -since; -within bounds how long the others may list it")
	flags.DurationVar(&parsed.watch, "watch", 30*time.Second, "node-down: how long after -since every instance must stay registered")

	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	if _, ok := scenarios[parsed.scenario]; !ok {
		return options{}, fmt.Errorf("unknown -scenario %q; want one of %s", parsed.scenario, strings.Join(scenarioNames(), ", "))
	}

	var err error

	if parsed.nodes, err = parseEndpoints(nodes); err != nil {
		return options{}, err
	}

	if parsed.expected, err = parseExpected(expected); err != nil {
		return options{}, err
	}

	if parsed.rounds < 1 || parsed.timeout <= 0 || parsed.within <= 0 {
		return options{}, errors.New("-rounds, -timeout and -within must be positive")
	}

	if parsed.since, err = parseSince(since); err != nil {
		return options{}, err
	}

	parsed.sinceAhead = parsed.since.After(time.Now())

	if parsed.token, err = readToken(tokenFile); err != nil {
		return options{}, err
	}

	if parsed.attackerToken, err = readToken(attackerFile); err != nil {
		return options{}, err
	}

	if parsed.scenario == "ownership" {
		if parsed.attackerToken == "" {
			return options{}, errors.New("-scenario ownership needs -attacker-token-file")
		}

		if _, ok := parsed.expected[parsed.service]; !ok {
			return options{}, errors.New("-scenario ownership needs a -service listed in -expect")
		}
	}

	if parsed.scenario == "storage-down" && (parsed.outage <= 0 || parsed.recover <= 0) {
		return options{}, errors.New("-outage and -recover must be positive")
	}

	if parsed.scenario == "load" {
		if parsed.duration <= 0 || parsed.lookupRate < 1 || parsed.callRate < 1 {
			return options{}, errors.New("-duration, -lookup-rate and -call-rate must be positive")
		}

		if _, ok := parsed.expected[parsed.service]; !ok {
			return options{}, errors.New("-scenario load needs a -service listed in -expect")
		}
	}

	if parsed.scenario == "node-down" && (parsed.down == "" || parsed.watch <= 0) {
		return options{}, errors.New("-scenario node-down needs -down and a positive -watch")
	}

	if slices.Contains(aboutOneInstance, parsed.scenario) {
		if parsed.service == "" || parsed.address == "" {
			return options{}, fmt.Errorf("-scenario %s needs -service and -address", parsed.scenario)
		}

		if _, ok := parsed.expected[parsed.service]; !ok {
			return options{}, fmt.Errorf("-service %s is not in -expect", parsed.service)
		}
	}

	return parsed, nil
}

// parseSince parses -since: Unix seconds with an optional fraction, as
// date +%s.%N prints them, or RFC 3339. Empty means now.
func parseSince(value string) (time.Time, error) {
	if value == "" {
		return time.Now(), nil
	}

	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		whole := int64(seconds)
		return time.Unix(whole, int64((seconds-float64(whole))*float64(time.Second))), nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("-since %q is neither Unix seconds nor RFC 3339", value)
	}

	return parsed, nil
}

// scenarioNames lists the scenarios, sorted.
func scenarioNames() []string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// parseEndpoints parses name=URL pairs separated by commas.
func parseEndpoints(value string) ([]endpoint, error) {
	var endpoints []endpoint

	for pair := range strings.SplitSeq(value, ",") {
		name, address, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || name == "" || address == "" {
			return nil, fmt.Errorf("-nodes: %q is not name=URL", pair)
		}

		if slices.ContainsFunc(endpoints, func(existing endpoint) bool { return existing.name == name }) {
			return nil, fmt.Errorf("-nodes: %q appears twice", name)
		}

		endpoints = append(endpoints, endpoint{name: name, url: address})
	}

	return endpoints, nil
}

// parseExpected parses name=count pairs separated by commas.
func parseExpected(value string) (map[string]int, error) {
	expected := map[string]int{}

	for pair := range strings.SplitSeq(value, ",") {
		name, count, ok := strings.Cut(strings.TrimSpace(pair), "=")
		number, err := strconv.Atoi(count)

		if !ok || name == "" || err != nil || number < 0 {
			return nil, fmt.Errorf("-expect: %q is not name=count", pair)
		}

		expected[name] = number
	}

	return expected, nil
}
