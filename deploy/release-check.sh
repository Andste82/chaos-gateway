#!/bin/sh
# Check of a release tag before the image workflow publishes anything (M30, docs/release.md).
#
#   deploy/release-check.sh v1.2.3                in a checkout whose HEAD is the commit the tag points to
#   deploy/release-check.sh --is-newest v1.2.3    whether the tag is the highest release version (only that one gets :latest)
#
# A tag that gets an image on ghcr.io and a GitHub release must be
#   - a version: vMAJOR.MINOR.PATCH, with an optional pre-release part (v1.0.0-rc.1), no build metadata,
#   - on the history of the branch main (`origin/main`, or `main` in a checkout that has no remote): a tag on a commit of a side branch
#     or of a fork would publish what was never reviewed or tested on main.
# A release (no pre-release part) should have its notes in docs/release-notes/<tag>.md; without the file the notes are generated from the
# commits, which is said as a warning.
#
# --is-newest is for the tag of a release that may be published: a hotfix on an older line (v1.0.1 after v1.1.0) must not move :latest
# back, and a pre-release is never the newest. The newest is the highest version, compared by its numbers (sort -V), of the release tags
# `vMAJOR.MINOR.PATCH` that are on main, with the tag itself.
#
# Exit status: 0 the tag may be published (--is-newest: it is the newest release), 1 it may not (--is-newest: it is not; the reason is
# on standard error), 2 usage.
set -eu

newest=""
if [ "${1:-}" = "--is-newest" ]; then
	newest=1
	shift
fi
[ $# -eq 1 ] || { echo "usage: release-check.sh [--is-newest] TAG" >&2; exit 2; }
tag=$1

if ! printf '%s\n' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'; then
	echo "release-check: $tag is not a version tag (vMAJOR.MINOR.PATCH, optionally -rc.1)" >&2
	exit 1
fi

head=$(git rev-parse HEAD) || { echo "release-check: this is not a git checkout" >&2; exit 1; }
main=""
for ref in refs/remotes/origin/main refs/heads/main; do
	if git rev-parse --verify --quiet "$ref" >/dev/null; then
		main=$ref
		break
	fi
done
[ -n "$main" ] || { echo "release-check: the checkout has no branch main to compare with (fetch the whole history: fetch-depth 0)" >&2; exit 1; }

if [ -n "$newest" ]; then
	case "$tag" in
	*-*)
		echo "release-check: $tag is a pre-release, never the newest release" >&2
		exit 1
		;;
	esac
	highest=$( (git tag --merged "$main" --list 'v*'; echo "$tag") | grep -E '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' | sort -V | tail -n 1)
	if [ "$highest" != "$tag" ]; then
		echo "release-check: $tag is not the newest release, $highest is" >&2
		exit 1
	fi
	echo "release-check: $tag is the newest release"
	exit 0
fi

if ! git merge-base --is-ancestor "$head" "$main"; then
	echo "release-check: $tag points to $head, which is not on main: merge the change first and tag the merge" >&2
	exit 1
fi

case "$tag" in
*-*) ;;
*)
	if [ ! -f "docs/release-notes/$tag.md" ]; then
		echo "release-check: warning: docs/release-notes/$tag.md does not exist, the notes of the release are generated from the commits" >&2
	fi
	;;
esac
echo "release-check: $tag is a version tag on main"
