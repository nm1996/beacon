#!/usr/bin/env bash
# Works out release versions for the stage and prod workflows.
#
# The version being released is the first "## [X.Y.Z]" heading in
# CHANGELOG.md, below Unreleased; preparing a release means moving the
# Unreleased entries under such a heading. Tags must be fetched.
#
#   release-version.sh version     X.Y.Z, if vX.Y.Z is not tagged yet
#   release-version.sh next-rc     the next vX.Y.Z-rc.N to tag
#   release-version.sh latest-rc   the newest vX.Y.Z-rc.N already tagged
#
# CHANGELOG overrides the changelog path, for tests.
set -euo pipefail

cd "$(dirname "$0")/.."

changelog="${CHANGELOG:-CHANGELOG.md}"

fail() {
	echo "release-version: $*" >&2
	exit 1
}

# version prints the version of the first release heading, and fails if that
# version has been released already.
version() {
	local heading
	heading="$(grep -m1 -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' "$changelog" || true)"
	[[ -n "$heading" ]] || fail "no '## [X.Y.Z]' heading in ${changelog##*/}"

	local number="${heading#*[}"
	number="${number%%]*}"

	if git rev-parse -q --verify "refs/tags/v$number" >/dev/null; then
		fail "v$number is already released; move the Unreleased entries in ${changelog##*/} under a new version heading"
	fi

	echo "$number"
}

# candidates prints the rc numbers tagged for version, highest last.
candidates() {
	local prefix="v$1-rc."
	local tag

	git tag --list "$prefix*" | while read -r tag; do
		local number="${tag#"$prefix"}"
		if [[ "$number" =~ ^[0-9]+$ ]]; then
			echo "$number"
		fi
	done | sort -n
}

case "${1:-}" in
version)
	version
	;;
next-rc)
	number="$(version)"
	last="$(candidates "$number" | tail -n1)"
	echo "v$number-rc.$((${last:-0} + 1))"
	;;
latest-rc)
	number="$(version)"
	last="$(candidates "$number" | tail -n1)"
	[[ -n "$last" ]] || fail "no release candidate is tagged for v$number; merge main into stage first"
	echo "v$number-rc.$last"
	;;
*)
	fail "usage: release-version.sh version|next-rc|latest-rc"
	;;
esac
