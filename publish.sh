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

# AstraMap One-Click Build, Package, Commit, Push, and GitHub Release Script
# Usage:
#   ./publish.sh [VERSION] [COMMIT_MESSAGE] [--all]
#
# Examples:
#   ./publish.sh v0.2.0 "release: official release v0.2.0"
#   ./publish.sh v0.2.0 "release: official release v0.2.0" --all

VERSION="v0.2.0"
COMMIT_MSG=""
BUILD_ALL=false

# Parse command line arguments
for arg in "$@"; do
  if [ "$arg" == "--all" ]; then
    BUILD_ALL=true
  elif [[ "$arg" =~ ^v?[0-9]+\.[0-9]+ ]]; then
    VERSION="$arg"
  elif [ -z "${COMMIT_MSG}" ]; then
    COMMIT_MSG="$arg"
  fi
done

if [ -z "${COMMIT_MSG}" ]; then
  COMMIT_MSG="release: official release ${VERSION}"
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${PROJECT_ROOT}/dist"

cd "${PROJECT_ROOT}"

echo "================================================="
echo "       AstraMap One-Click Ship Pipeline"
echo "================================================="
echo "Version:        ${VERSION}"
echo "Commit Message: ${COMMIT_MSG}"
echo "Project Root:   ${PROJECT_ROOT}"
echo "Dist Dir:       ${DIST_DIR}"
echo "Mode:           $( [ "${BUILD_ALL}" = true ] && echo "All Target Platforms" || echo "Current Host Platform Only ($(go env GOOS)/$(go env GOARCH))" )"
echo "================================================="

# Step 1: Code formatting & Vet & Test
echo ""
echo "[Step 1/5] 🔍 Running code quality checks & tests..."
gofmt -w .
make vet
make test

# Step 2: Build & Package Release Artifacts
echo ""
echo "[Step 2/5] 📦 Building release binary and package artifacts..."
rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

DOC_FILES=(
  "LICENSE"
  "THIRD_PARTY_NOTICES.md"
  "README.md"
  "README_EN.md"
  "QUICKSTART.md"
  "QUICKSTART_EN.md"
  "CHANGELOG.md"
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

  # 交叉编译检查：Tree-sitter C 扩展依赖 CGO。跨架构/跨系统编译时需指定对应的 Cross-Compiler
  if [ "${TARGET_OS}" != "${HOST_OS}" ] || [ "${TARGET_ARCH}" != "${HOST_ARCH}" ]; then
    CROSS_CC=""
    if [ "${TARGET_OS}" = "linux" ] && [ "${TARGET_ARCH}" = "arm64" ]; then
      if command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
        CROSS_CC="aarch64-linux-gnu-gcc"
      fi
    elif [ "${TARGET_OS}" = "windows" ] && [ "${TARGET_ARCH}" = "amd64" ]; then
      if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
        CROSS_CC="x86_64-w64-mingw32-gcc"
      fi
    elif [ "${TARGET_OS}" = "darwin" ]; then
      if command -v osxcross-cc >/dev/null 2>&1; then
        CROSS_CC="osxcross-cc"
      fi
    fi

    if [ -n "${CROSS_CC}" ]; then
      ENV_VARS+=("CC=${CROSS_CC}")
    else
      echo "    [Warning] Cross-compiler for ${TARGET_OS}/${TARGET_ARCH} not found on host."
      echo "    Skipping target ${TARGET_OS}/${TARGET_ARCH} (Install cross-toolchain or run on native runner)."
      rm -rf "${STAGE_DIR}"
      continue
    fi
  else
    # Check if musl-gcc is available for linux static build on same host
    if [ "${TARGET_OS}" = "linux" ] && command -v musl-gcc >/dev/null 2>&1; then
      ENV_VARS+=("CC=musl-gcc")
      BUILD_FLAGS+=(-tags "netgo osusergo" -ldflags="-s -w -extldflags '-static' -X main.version=${VERSION}")
    fi
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

# Step 3: Git Stage & Commit
echo ""
echo "[Step 3/5] 📝 Staging files and creating Git commit..."
git add .
if git diff-index --quiet HEAD --; then
  echo "    No changes to commit."
else
  git commit -m "${COMMIT_MSG}"
  echo "    Git commit created successfully."
fi

# Step 4: Push to Remotes & Tags
echo ""
echo "[Step 4/5] 🚀 Pushing to GitHub & Remote repositories..."
CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"

echo "    Pushing branch '${CURRENT_BRANCH}' to origin (GitHub)..."
git push origin "${CURRENT_BRANCH}"

if [ -n "${VERSION}" ]; then
  if ! git rev-parse "${VERSION}" >/dev/null 2>&1; then
    echo "    Creating local git tag '${VERSION}'..."
    git tag -a "${VERSION}" -m "Release ${VERSION}"
  fi
  echo "    Pushing tag '${VERSION}' to origin (GitHub)..."
  git push origin "${VERSION}" || true
fi

# Step 5: Upload release artifacts to GitHub Release
echo ""
if [ -d "${PROJECT_ROOT}/dist" ] && ls "${PROJECT_ROOT}/dist"/amap-* >/dev/null 2>&1; then
  if command -v gh >/dev/null 2>&1; then
    echo "[Step 5/5] 📤 Uploading release artifacts to GitHub Release..."

    if git rev-parse "${VERSION}" >/dev/null 2>&1; then
      if ! gh release view "${VERSION}" >/dev/null 2>&1; then
        echo "    Creating GitHub Release '${VERSION}'..."
        gh release create "${VERSION}" \
          --title "AstraMap ${VERSION}" \
          --notes "Release artifacts for AstraMap ${VERSION}. See SHA256SUMS for checksums." \
          --draft=false
      fi

      echo "    Uploading artifacts from dist/..."
      gh release upload "${VERSION}" "${PROJECT_ROOT}/dist"/amap-* "${PROJECT_ROOT}/dist"/SHA256SUMS \
        --clobber
      echo "    Release artifacts uploaded successfully."
    else
      echo "    [Warning] Tag '${VERSION}' not found, skipping GitHub Release upload."
    fi
  else
    echo "[Step 5/5] ⚠️  'gh' CLI not found — skipping GitHub Release upload."
    echo "    Install gh (https://cli.github.com) and run:"
    echo "      gh release create ${VERSION} ./dist/amap-* ./dist/SHA256SUMS"
  fi
else
  echo "[Step 5/5] ⏭️  No release artifacts found in dist/, skipping GitHub Release upload."
fi

echo ""
echo "================================================="
echo " 🎉 Successfully built, packaged, committed & pushed!"
echo "================================================="
