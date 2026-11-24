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
#
# The project is left running; stop it with
#   docker compose -f simulation/compose.yaml down --volumes
set -euo pipefail

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
	RUNNER_UID="$(id -u)" RUNNER_GID="$(id -g)" compose run --rm --no-deps runner "$@"
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
	local since
	since="$(now)"
	compose kill orders-2
	local status=0
	runner -scenario gone -service orders -address orders-2 -since "$since" -within "$ttl" || status=$?
	compose start orders-2
	return "$status"
}

instance_stopped() {
	expect_normal
	local since
	since="$(now)"
	compose stop orders-2
	local status=0
	# Unregistering is immediate; one second covers the polls.
	runner -scenario gone -service orders -address orders-2 -since "$since" -within 1s || status=$?
	compose start orders-2
	return "$status"
}

instance_back() {
	expect_normal
	local output since status=0
	since="$(now)"
	compose kill orders-2
	output="$(runner -scenario gone -service orders -address orders-2 -since "$since" -within "$ttl")" || status=$?
	echo "$output"
	if [[ "$status" -ne 0 ]]; then
		compose start orders-2
		return "$status"
	fi
	local old
	old="$(target_id "$output")"
	since="$(now)"
	compose start orders-2
	runner -scenario back -service orders -address orders-2 -since "$since" -within 10s -old-id "$old"
}

instance_back_same_id() {
	ORDERS_2_ID=orders-2 up
	expect_normal
	compose kill orders-2
	local since status=0
	since="$(now)"
	compose start orders-2
	runner -scenario back -service orders -address orders-2 -since "$since" -within 10s -want-id orders-2 || status=$?
	# Put orders-2 back as the other scenarios expect it, without a fixed ID.
	up
	return "$status"
}

main() {
	local scenarios=("$@")
	if [[ ${#scenarios[@]} -eq 0 ]]; then
		scenarios=(normal instance-killed instance-stopped instance-back instance-back-same-id)
	fi

	up

	local failed=()
	for scenario in "${scenarios[@]}"; do
		echo "== $scenario"
		case "$scenario" in
		normal | instance-killed | instance-stopped | instance-back | instance-back-same-id)
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
