#!/usr/bin/env bash
# run.sh drives the simulation: it starts the Compose project, runs the
# scenarios that need containers stopped, killed or started, and calls the
# runner to observe each one. It exits non-zero if any scenario failed.
#
#   simulation/run.sh                    # every scenario below, in order
#   simulation/run.sh instance-killed    # just one
#
# Scenarios:
#   normal               A: everything running
#   instance-killed      B: orders-2 is killed and must expire
#   instance-stopped     B: orders-2 is stopped and must leave at once
#   instance-back        D: orders-2 is killed, expires, starts again
#   instance-back-same-id D: orders-2 with a fixed ID is killed and started
#                        again before it expires
#   node-stopped         C: beacon-a is stopped; nothing may expire
#   node-killed          C: beacon-a is killed; nothing may expire
#   node-stopped-retry-post, node-killed-retry-post
#                        C again, with the load balancer also passing
#                        failed POSTs on to the other node
#   storage-down         E: PostgreSQL is stopped for 20s
#   ownership            F: with authentication on, payments' token must
#                        not change orders' instances, also right after
#                        beacon-b restarts
#
# The project is left running; stop it with
#   docker compose -f simulation/compose.yaml down --volumes
set -euo pipefail

# Times are passed to the runner and to sleep with a decimal point, which
# awk and printf write as a comma under locales such as sr_RS.
export LC_ALL=C

here="$(cd "$(dirname "$0")" && pwd)"

# Every bound below follows from the 9s TTL in compose.yaml; see
# "Timing bounds" in README.md.
ttl=9s

compose() {
	docker compose -f "$here/compose.yaml" "$@"
}

# runner runs one scenario and prints its summary. It never stops the
# script, so later scenarios still run; the result is collected instead.
runner() {
	RUNNER_UID="$(id -u)" RUNNER_GID="$(id -g)" compose run --rm --no-deps --no-TTY runner "$@"
}

# now prints the current time as Unix seconds with a fraction, for -since.
# The containers share the host's clock.
now() {
	local value
	value="$(date +%s.%N)"
	if [[ "$value" == *N ]]; then
		# BSD date, as on macOS, has no %N.
		value="$(perl -MTime::HiRes=time -e 'printf "%.6f", time')"
	fi
	echo "$value"
}

# lead is how many seconds the runner gets to start and get ready before a
# step. A runner that needed longer fails its scenario and says so.
lead=10

# sleep_until waits until the Unix time $1.
sleep_until() {
	local left
	left="$(awk -v until="$1" -v now="$(now)" 'BEGIN { left = until - now; printf "%.3f", (left > 0 ? left : 0) }')"
	sleep "$left"
}

# watch starts the runner with the arguments in runner_args and -since
# $lead seconds ahead, runs "$@" at that moment, and waits for the runner.
# The runner is ready and watching by then, so its timings start at the
# step itself rather than at its own start. Its output is printed, and
# kept in watched for target_id.
watch() {
	local since output pid status=0
	since="$(awk -v now="$(now)" -v lead="$lead" 'BEGIN { printf "%.6f", now + lead }')"
	output="$(mktemp)"
	runner "${runner_args[@]}" -since "$since" >"$output" 2>&1 &
	pid=$!
	sleep_until "$since"
	"$@"
	wait "$pid" || status=$?
	watched="$(cat "$output")"
	rm -f "$output"
	echo "$watched"
	return "$status"
}

# target_id prints the ID the runner reported for its target, from the
# output in $1.
target_id() {
	sed -n 's/^  target id: //p' <<<"$1" | tail -n 1
}

# up starts the project, building the images from the checkout, with
# orders-2 using the instance ID in $ORDERS_2_ID when it is set.
up() {
	compose up --build --detach --wait --quiet-pull
}

# expect_normal fails the script if scenario A does not pass, since every
# other scenario starts from it.
expect_normal() {
	if ! runner -scenario normal; then
		echo "run.sh: scenario normal failed; the other scenarios need it" >&2
		exit 1
	fi
}

normal() {
	runner -scenario normal
}

instance_killed() {
	expect_normal
	local status=0
	runner_args=(-scenario gone -service orders -address orders-2 -within "$ttl")
	watch compose kill orders-2 || status=$?
	compose start orders-2
	return "$status"
}

instance_stopped() {
	expect_normal
	local status=0
	# Unregistering is immediate; one second covers the stop and the polls.
	runner_args=(-scenario gone -service orders -address orders-2 -within 1s)
	watch compose stop orders-2 || status=$?
	compose start orders-2
	return "$status"
}

instance_back() {
	expect_normal
	runner_args=(-scenario gone -service orders -address orders-2 -within "$ttl")
	if ! watch compose kill orders-2; then
		compose start orders-2
		return 1
	fi
	runner_args=(-scenario back -service orders -address orders-2 -within 10s -old-id "$(target_id "$watched")")
	watch compose start orders-2
}

# kill_and_start kills orders-2 and starts it again at once.
kill_and_start() {
	compose kill orders-2
	compose start orders-2
}

instance_back_same_id() {
	ORDERS_2_ID=orders-2 up
	expect_normal
	local status=0
	runner_args=(-scenario back -service orders -address orders-2 -within 10s -want-id orders-2)
	watch kill_and_start || status=$?
	# Put orders-2 back as the other scenarios expect it, without a fixed ID.
	up
	return "$status"
}

# lb_mode recreates the load balancer with $1 as its proxy_next_upstream.
lb_mode() {
	LB_NEXT_UPSTREAM="$1" compose up --detach --wait --no-deps lb
}

# node_down stops beacon-a with docker compose $1 (stop or kill), watches
# the instances for more than three TTLs, then starts it again and checks
# that it serves the same instances. $2 bounds how long beacon-b may keep
# listing it: at once after a stop, 15s after a kill.
node_down() {
	expect_normal
	local status=0
	runner_args=(-scenario node-down -down beacon-a -within "$2" -watch 30s)
	watch compose "$1" beacon-a || status=$?
	compose up --detach --wait --no-deps beacon-a
	runner -scenario normal || status=$?
	return "$status"
}

node_stopped() {
	node_down stop 1s
}

node_killed() {
	node_down kill 15s
}

node_stopped_retry_post() {
	local status=0
	lb_mode "error timeout non_idempotent"
	node_down stop 1s || status=$?
	lb_mode "error timeout"
	return "$status"
}

node_killed_retry_post() {
	local status=0
	lb_mode "error timeout non_idempotent"
	node_down kill 15s || status=$?
	lb_mode "error timeout"
	return "$status"
}

# outage stops PostgreSQL for 20s, more than two TTLs.
outage() {
	compose stop postgres
	sleep 20
	compose start postgres
}

# storage_down runs the outage while the runner watches. The runner gets
# ready first, and sends heartbeats through both nodes until PostgreSQL
# stops.
storage_down() {
	expect_normal
	runner_args=(-scenario storage-down -outage 20s -recover 15s -within 4s)
	watch outage
}

# auth_files writes a random token for each caller to auth/, and the tokens
# file with their hashes. The applications may register and discover; the
# runner may also read metrics and list nodes. The files are readable by
# the containers' user, which is fine on a laptop only.
auth_files() {
	local dir="$here/auth"
	local tokens="$dir/tokens.toml.new"
	: >"$tokens"
	for caller in orders payments inventory runner; do
		local token permissions='["register", "discover"]'
		token="$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
		printf '%s\n' "$token" >"$dir/$caller.token"
		if [[ "$caller" == runner ]]; then
			permissions='["register", "discover", "metrics", "manage"]'
		fi
		printf '[[tokens]]\nid = "%s"\nname = "%s"\nsha256 = "%s"\npermissions = %s\n\n' \
			"$caller" "$caller" "$(printf '%s' "$token" | sha256)" "$permissions" >>"$tokens"
	done
	mv "$tokens" "$dir/tokens.toml"
	chmod 0644 "$dir"/*.token "$dir/tokens.toml"
}

# sha256 prints the hex SHA-256 of standard input.
sha256() {
	if command -v sha256sum >/dev/null; then
		sha256sum | cut -d ' ' -f 1
	else
		shasum -a 256 | cut -d ' ' -f 1
	fi
}

# ownership turns authentication on, attacks orders' instances with
# payments' token, restarts beacon-b and attacks again at once, then turns
# authentication off.
ownership() {
	local status=0
	auth_files
	export BEACON_SIM_AUTH=true
	up
	expect_normal
	runner -scenario ownership -service orders -attacker-token-file /auth/payments.token || status=$?
	compose restart beacon-b
	compose up --detach --wait --no-deps beacon-b
	runner -scenario ownership -service orders -attacker-token-file /auth/payments.token || status=$?
	unset BEACON_SIM_AUTH
	up
	return "$status"
}

main() {
	local scenarios=("$@")
	if [[ ${#scenarios[@]} -eq 0 ]]; then
		scenarios=(normal instance-killed instance-stopped instance-back instance-back-same-id
			node-stopped node-killed node-stopped-retry-post node-killed-retry-post
			storage-down ownership)
	fi

	up

	local failed=()
	for scenario in "${scenarios[@]}"; do
		echo "== $scenario"
		case "$scenario" in
		normal | instance-killed | instance-stopped | instance-back | instance-back-same-id | \
			node-stopped | node-killed | node-stopped-retry-post | node-killed-retry-post | \
			storage-down | ownership)
			"${scenario//-/_}" || failed+=("$scenario")
			;;
		*)
			echo "run.sh: unknown scenario $scenario" >&2
			exit 2
			;;
		esac
	done

	if [[ ${#failed[@]} -gt 0 ]]; then
		echo "== failed: ${failed[*]}" >&2
		exit 1
	fi

	echo "== every scenario passed"
}

main "$@"
