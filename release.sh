#!/usr/bin/env bash
# Copyright 2026 AstraMap Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the original license at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

# AstraMap Release Automation Script
# Usage:
#   ./release.sh [VERSION] [--all]
#   VERSION: Release tag version, e.g. v0.1.0 (default: v0.1.0)
#   --all: Build all target platforms (requires cross-compiler toolchains)

VERSION="v0.1.0"
BUILD_ALL=false

for arg in "$@"; do
  if [ "$arg" == "--all" ]; then
    BUILD_ALL=true
  elif [[ "$arg" =~ ^v?[0-9]+\.[0-9]+ ]]; then
    VERSION="$arg"
  fi
done

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${PROJECT_ROOT}/dist"

echo "================================================="
echo "       AstraMap Release Packaging Automation"
echo "================================================="
echo "Version:      ${VERSION}"
echo "Project Root: ${PROJECT_ROOT}"
echo "Dist Dir:     ${DIST_DIR}"
echo "Mode:         $( [ "${BUILD_ALL}" = true ] && echo "All Platforms" || echo "Current Platform Only ($(go env GOOS)/$(go env GOARCH))" )"
echo "================================================="

# Clean and create dist directory
rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

DOC_FILES=(
  "LICENSE"
  "THIRD_PARTY_NOTICES.md"
  "README.md"
  "README_EN.md"
  "QUICKSTART.md"
  "QUICKSTART_EN.md"
)

# Verify documentation files
for doc in "${DOC_FILES[@]}"; do
  if [ ! -f "${PROJECT_ROOT}/${doc}" ]; then
    echo "Error: Required file ${doc} is missing!" >&2
    exit 1
  fi
done

if [ "${BUILD_ALL}" = true ]; then
  TARGETS=(
    "linux/amd64/tar.gz"
    "linux/arm64/tar.gz"
    "darwin/amd64/tar.gz"
    "darwin/arm64/tar.gz"
    "windows/amd64/zip"
  )
else
  HOST_OS="$(go env GOOS)"
  HOST_ARCH="$(go env GOARCH)"
  FMT="tar.gz"
  if [ "${HOST_OS}" = "windows" ]; then
    FMT="zip"
  fi
  TARGETS=("${HOST_OS}/${HOST_ARCH}/${FMT}")
fi

for target in "${TARGETS[@]}"; do
  IFS="/" read -r TARGET_OS TARGET_ARCH FORMAT <<< "${target}"
  
  BINARY_NAME="amap"
  if [ "${TARGET_OS}" = "windows" ]; then
    BINARY_NAME="amap.exe"
  fi

  ARCHIVE_NAME="amap-${TARGET_OS}-${TARGET_ARCH}"
  STAGE_DIR="${DIST_DIR}/${ARCHIVE_NAME}"
  
  echo ""
  echo "--> Building binary for ${TARGET_OS}/${TARGET_ARCH}..."
  mkdir -p "${STAGE_DIR}"

  BUILD_FLAGS=(-trimpath -ldflags="-s -w -X main.version=${VERSION}")
  ENV_VARS=("CGO_ENABLED=1" "GOOS=${TARGET_OS}" "GOARCH=${TARGET_ARCH}")

  # Check if musl-gcc is available for linux static build
  if [ "${TARGET_OS}" = "linux" ] && command -v musl-gcc >/dev/null 2>&1; then
    ENV_VARS+=("CC=musl-gcc")
    BUILD_FLAGS+=(-tags "netgo osusergo" -ldflags="-s -w -extldflags '-static' -X main.version=${VERSION}")
  fi

  env "${ENV_VARS[@]}" go build \
    "${BUILD_FLAGS[@]}" \
    -o "${STAGE_DIR}/${BINARY_NAME}" \
    ./cmd/amap

  echo "    Copying legal and documentation files..."
  for doc in "${DOC_FILES[@]}"; do
    cp "${PROJECT_ROOT}/${doc}" "${STAGE_DIR}/"
  done

  cd "${DIST_DIR}"
  if [ "${FORMAT}" = "zip" ]; then
    PACKAGED_FILE="${ARCHIVE_NAME}.zip"
    echo "    Creating archive ${PACKAGED_FILE}..."
    if command -v zip >/dev/null 2>&1; then
      (cd "${STAGE_DIR}" && zip -q -r "${DIST_DIR}/${PACKAGED_FILE}" .)
    else
      tar -czf "${PACKAGED_FILE}" -C "${STAGE_DIR}" .
    fi
  else
    PACKAGED_FILE="${ARCHIVE_NAME}.tar.gz"
    echo "    Creating archive ${PACKAGED_FILE}..."
    tar -czf "${PACKAGED_FILE}" -C "${STAGE_DIR}" .
  fi
  cd "${PROJECT_ROOT}"

  rm -rf "${STAGE_DIR}"
  echo "    Done: ${DIST_DIR}/${PACKAGED_FILE}"
done

echo ""
echo "--> Calculating SHA256 checksums..."
cd "${DIST_DIR}"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum amap-* > SHA256SUMS
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 amap-* > SHA256SUMS
fi
cd "${PROJECT_ROOT}"

echo ""
echo "================================================="
echo "           Release Package Artifacts"
echo "================================================="
ls -lh "${DIST_DIR}"
echo "================================================="
echo "All release artifacts ready in: ${DIST_DIR}"
