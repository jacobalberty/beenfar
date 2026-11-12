#!/usr/bin/env bash
# Prints the next version of the api module (github.com/jacobalberty/beenfar/api)
# as api/vX.Y.Z, computed from the conventional commits that touched api/ since
# the last api/v* tag, or nothing when none of them calls for a release.
#
#   feat                              minor
#   fix, perf                         patch
#   "!" after the type, or a          major; while 0.x, minor instead. v1.0.0
#   BREAKING CHANGE: footer           and new major versions (which need a
#                                     /vN module path) are released by hand.
#   anything else (docs, ci, chore,   no release
#   build, refactor, test, ...)
#
# Commits that do not follow https://www.conventionalcommits.org/ are ignored;
# commits-api.yml rejects them in pull requests.
set -euo pipefail

last=$(git tag -l 'api/v*' --sort=-v:refname | head -n 1)
current=${last#api/}
current=${current:-v0.0.0}
range=HEAD
[ -n "$last" ] && range="$last..HEAD"

bump=none
while IFS= read -r sha; do
  subject=$(git log -1 --format=%s "$sha")
  body=$(git log -1 --format=%b "$sha")
  if [[ "$subject" =~ ^[a-z]+(\([a-z0-9._/-]+\))?!:\  ]] || grep -qE '^BREAKING[ -]CHANGE:' <<<"$body"; then
    bump=major
  elif [[ "$subject" =~ ^feat(\([a-z0-9._/-]+\))?:\  ]]; then
    [ "$bump" = major ] || bump=minor
  elif [[ "$subject" =~ ^(fix|perf)(\([a-z0-9._/-]+\))?:\  ]]; then
    [ "$bump" = none ] && bump=patch
  fi
done < <(git log --no-merges --format=%H "$range" -- api/)

IFS=. read -r major minor patch <<<"${current#v}"
case "$bump" in
  none) exit 0 ;;
  patch) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)) patch=0 ;;
  major)
    if [ "$major" != 0 ]; then
      echo "breaking api changes since $last need v$((major + 1)) and a /v$((major + 1)) module path; release it by hand" >&2
      exit 1
    fi
    minor=$((minor + 1)) patch=0
    ;;
esac
echo "api/v$major.$minor.$patch"
