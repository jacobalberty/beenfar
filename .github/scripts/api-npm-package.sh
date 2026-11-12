#!/usr/bin/env bash
# Assembles the npm package @jacobalberty/beenfar-api (the generated TypeScript
# client, api/gen/ts) into <out-dir>, ready for `npm pack` or `npm publish`:
#
#   .github/scripts/api-npm-package.sh <out-dir> <version>
#
# <version> is X.Y.Z, vX.Y.Z or api/vX.Y.Z, so the package carries the version
# of the api Go module it was generated from. The package.json comes from
# api/ts/package.json, minus the fields that only matter in the repository.
set -euo pipefail

out=${1:?usage: api-npm-package.sh <out-dir> <version>}
version=${2:?usage: api-npm-package.sh <out-dir> <version>}
version=${version#api/}
version=${version#v}
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "'$2' is not a version (want X.Y.Z, vX.Y.Z or api/vX.Y.Z)" >&2
  exit 1
fi

root=$(git rev-parse --show-toplevel)
if ! [ -d "$root/api/gen/ts/beenfar" ]; then
  echo "$root/api/gen/ts is missing; run 'go generate' in api/ first" >&2
  exit 1
fi

rm -rf "$out"
mkdir -p "$out"
cp -R "$root/api/gen/ts/." "$out/"
cp "$root/api/ts/README.md" "$root/LICENSE" "$out/"
jq --arg v "$version" 'del(.private, .devDependencies) | .version = $v' \
  "$root/api/ts/package.json" > "$out/package.json"
