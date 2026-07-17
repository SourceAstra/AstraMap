#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
AMAP=${AMAP:-"$ROOT/amap"}
LANGUAGES=(ruby dart swift lua scala zig visualbasic)

installed=()
packages=()
rollback() {
  for ((index=${#installed[@]}-1; index>=0; index--)); do
    IFS='|' read -r language version previous <<<"${installed[index]}"
    "$AMAP" language remove "$language" "$version" || true
    if [[ -n "$previous" ]]; then
      "$AMAP" language enable "$language" "$previous" || true
    fi
  done
}
trap rollback ERR

for language in "${LANGUAGES[@]}"; do
  previous=$("$AMAP" language list | awk -v id="$language" '$1 == id && $3 == "active" {print $2; exit}')
  "$ROOT/scripts/languages/build-package.sh" "$language"
  manifest="$ROOT/language-packs/manifests/$language.json"
  version=$(awk -F'"' '/"version"[[:space:]]*:/ {print $4; exit}' "$manifest")
  args=(language install)
  if [[ -z "${ASTRAMAP_LANGUAGE_SIGNING_KEY_FILE:-}" ]]; then
    args+=(--allow-unsigned)
  else
    args+=(--trust-key "$ROOT/dist/languages/trusted-keys.json")
  fi
  args+=("$ROOT/dist/languages/$language-$version.amaplang")
  "$AMAP" "${args[@]}"
  installed+=("$language|$version|$previous")
  packages+=(--package "$ROOT/dist/languages/$language-$version.amaplang")
done

if [[ -n "${ASTRAMAP_LANGUAGE_SIGNING_KEY_FILE:-}" && -n "${ASTRAMAP_LANGUAGE_CATALOG_BASE_URL:-}" ]]; then
  (
    cd "$ROOT/language-packs"
    go run ./cmd/pack \
      --catalog-output "$ROOT/dist/languages/catalog.json" \
      --catalog-base-url "$ASTRAMAP_LANGUAGE_CATALOG_BASE_URL" \
      --catalog-key-id astramap-release-1 \
      --private-key "$ASTRAMAP_LANGUAGE_SIGNING_KEY_FILE" \
      --trusted-keys-output "$ROOT/dist/languages/trusted-keys.json" \
      "${packages[@]}"
  )
fi

trap - ERR
