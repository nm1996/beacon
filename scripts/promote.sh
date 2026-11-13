#!/usr/bin/env bash
# Opens the pull request that promotes one long-lived branch into the next:
#
#   promote.sh main stage   a release candidate of the next version
#   promote.sh stage prod   the release itself
#
# It checks what the merge will release before opening anything, and prints
# the existing pull request instead if one is already open.
set -euo pipefail

cd "$(dirname "$0")/.."

fail() {
	echo "promote: $*" >&2
	exit 1
}

from="${1:-}"
to="${2:-}"

case "$from:$to" in
main:stage | stage:prod) ;;
*) fail "usage: promote.sh main stage | promote.sh stage prod" ;;
esac

git fetch --quiet --tags origin "$from" "$to"

if [[ "$(git rev-list --count "origin/$to..origin/$from")" == 0 ]]; then
	fail "$to already has everything on $from"
fi

existing="$(gh pr list --base "$to" --head "$from" --state open --json url --jq '.[0].url // empty')"
if [[ -n "$existing" ]]; then
	echo "Already open: $existing"
	exit 0
fi

# The release workflows read the version from the changelog being merged.
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
changelog="$scratch/CHANGELOG.md"
git show "origin/$from:CHANGELOG.md" >"$changelog"

if [[ "$to" == stage ]]; then
	tag="$(CHANGELOG="$changelog" scripts/release-version.sh next-rc)"
	title="release: $tag"
	summary="Merging this tags \`$tag\` on \`stage\` and publishes it as a pre-release."
else
	version="$(CHANGELOG="$changelog" scripts/release-version.sh version)"
	candidate="$(CHANGELOG="$changelog" scripts/release-version.sh latest-rc)"

	if [[ "$(git rev-parse "$candidate^{tree}")" != "$(git rev-parse "origin/stage^{tree}")" ]]; then
		fail "stage has changed since $candidate; wait for the stage workflow to tag the next candidate"
	fi

	tag="v$version"
	title="release: $tag"
	summary="Merging this tags \`$tag\`, the same files as \`$candidate\`, and publishes the release."
fi

body="$(
	echo "$summary"
	echo
	echo "## Changes"
	echo
	git log --no-merges --format='- %s' "origin/$to..origin/$from"
)"

gh pr create --base "$to" --head "$from" --title "$title" --body "$body"
