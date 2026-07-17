#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
LANGUAGE=${1:?usage: build-package.sh <language> [goos] [goarch]}
GOOS_VALUE=${2:-$(go env GOOS)}
GOARCH_VALUE=${3:-$(go env GOARCH)}
PACKS="$ROOT/language-packs"
MANIFEST="$PACKS/manifests/$LANGUAGE.json"
VERSION=$(awk -F'"' '/"version"[[:space:]]*:/ {print $4; exit}' "$MANIFEST")
[[ -n "$VERSION" ]] || { echo "missing language package version: $MANIFEST" >&2; exit 1; }
DIST=${4:-"$ROOT/dist/languages"}
BIN="$DIST/workers/$GOOS_VALUE-$GOARCH_VALUE/$LANGUAGE"

if [[ "$GOOS_VALUE" == "windows" ]]; then
  BIN="$BIN.exe"
fi

mkdir -p "$(dirname "$BIN")" "$DIST"
(
  cd "$PACKS"
  GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" go build -trimpath \
    -ldflags "-X astramap-language-packs/internal/sdk.WorkerVersion=$VERSION" \
    -o "$BIN" "./cmd/$LANGUAGE"
)

PACK_ARGS=(
  -manifest "$MANIFEST"
  -artifact "$GOOS_VALUE/$GOARCH_VALUE=$BIN"
  -output "$DIST/$LANGUAGE-$VERSION.amaplang"
)

while IFS='|' read -r module directory; do
  [[ -n "$module" && -d "$directory" ]] || continue
  while IFS= read -r notice; do
    name=$(basename "$notice")
    PACK_ARGS+=(-file "licenses/$module/$name=$notice")
  done < <(find "$directory" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) -print)
done < <(
  cd "$PACKS"
  go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' "./cmd/$LANGUAGE" | sort -u
)

if [[ -n "${ASTRAMAP_LANGUAGE_SIGNING_KEY_FILE:-}" ]]; then
  PACK_ARGS+=(
    -private-key "$ASTRAMAP_LANGUAGE_SIGNING_KEY_FILE"
    -trusted-keys-output "$DIST/trusted-keys.json"
  )
else
  PACK_ARGS+=(-unsigned)
fi

(
  cd "$PACKS"
  go run ./cmd/pack "${PACK_ARGS[@]}"
)
