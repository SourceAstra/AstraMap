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

# AstraMap One-Click Build, Package, Commit, and Push Script
# Usage:
#   ./publish.sh [VERSION] [COMMIT_MESSAGE]
#
# Example:
#   ./publish.sh v0.1.0 "release: official open-source release v0.1.0"

VERSION="${1:-v0.1.0}"
COMMIT_MSG="${2:-release: open-source release ${VERSION}}"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "${PROJECT_ROOT}"

echo "================================================="
echo "       AstraMap One-Click Ship Pipeline"
echo "================================================="
echo "Version:        ${VERSION}"
echo "Commit Message: ${COMMIT_MSG}"
echo "Project Root:   ${PROJECT_ROOT}"
echo "================================================="

# Step 1: Code formatting & Vet & Test
echo ""
echo "[Step 1/4] 🔍 Running code quality checks & tests..."
gofmt -w .
make vet
make test

# Step 2: Build & Package Release
echo ""
echo "[Step 2/4] 📦 Building static release artifacts..."
./release.sh "${VERSION}"

# Step 3: Git Stage & Commit
echo ""
echo "[Step 3/4] 📝 Staging files and creating Git commit..."
git add .
if git diff-index --quiet HEAD --; then
  echo "    No changes to commit."
else
  git commit -m "${COMMIT_MSG}"
  echo "    Git commit created successfully."
fi

# Step 4: Push to Remotes
echo ""
echo "[Step 4/4] 🚀 Pushing to GitHub & Remote repositories..."
CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"

echo "    Pushing branch '${CURRENT_BRANCH}' to origin (GitHub)..."
git push origin "${CURRENT_BRANCH}"

# Optionally push tags if version tag doesn't exist locally/remotely
if [ -n "${VERSION}" ]; then
  if ! git rev-parse "${VERSION}" >/dev/null 2>&1; then
    echo "    Creating local git tag '${VERSION}'..."
    git tag -a "${VERSION}" -m "Release ${VERSION}"
  fi
  echo "    Pushing tag '${VERSION}' to origin (GitHub)..."
  git push origin "${VERSION}" || true
fi

# Also push to gitee if configured
if git remote | grep -q "^gitee$"; then
  echo "    Pushing branch '${CURRENT_BRANCH}' to gitee..."
  git push gitee "${CURRENT_BRANCH}" || true
fi

echo ""
echo "================================================="
echo " 🎉 Successfully built, packaged, committed & pushed!"
echo "================================================="
