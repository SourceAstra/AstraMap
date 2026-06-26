#!/bin/bash
set -euo pipefail

VERSION="${1:-v0.1}"
IMAGE="${2:-astramap}"
BINARY="amap"

echo "=== AstraMap Build Script ==="
echo "Version: ${VERSION}"
echo "Image:   ${IMAGE}"

# Build binary
echo "[1/3] Compiling ${BINARY}..."
go build -ldflags="-s -w -X main.version=${VERSION}" -o "${BINARY}" ./cmd/amap
echo "  -> $(ls -lh ${BINARY} | awk '{print $5}') $(pwd)/${BINARY}"

# Build Docker image
echo "[2/3] Building Docker image ${IMAGE}:${VERSION}..."
docker build -t "${IMAGE}:${VERSION}" -t "${IMAGE}:latest" .
echo "  -> ${IMAGE}:${VERSION}"
echo "  -> ${IMAGE}:latest"

# Show usage
echo "[3/3] Done. Usage:"
echo "  Dashboard:  docker run -p 8585:8585 -v /path/to/project:/project:ro ${IMAGE}"
echo "  Index:      docker run --rm -v /path/to/project:/project ${IMAGE} index --project /project"
echo "  MCP:        docker run --rm -i -v /path/to/project:/project ${IMAGE} serve --project /project"
