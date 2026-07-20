#!/bin/bash
set -euo pipefail

VERSION="${1:-v0.1}"
BINARY="${BINARY:-amap}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"

echo "=== AstraMap static release build ==="
echo "Version: ${VERSION}"
echo "Target:  ${GOOS}/${GOARCH}"
echo "Output:  ${BINARY}"

CC=musl-gcc \
CGO_ENABLED=1 \
GOOS="${GOOS}" \
GOARCH="${GOARCH}" \
go build \
	-tags "netgo osusergo" \
	-ldflags "-s -w -extldflags '-static' -X main.version=${VERSION}" \
	-o "${BINARY}" \
	./cmd/amap

FILE_OUTPUT="$(file "${BINARY}")"
printf '%s\n' "${FILE_OUTPUT}"
LDD_OUTPUT="$(ldd "${BINARY}" 2>&1 || true)"

if printf '%s\n' "${FILE_OUTPUT}" | grep -q "statically linked" &&
	! printf '%s\n' "${LDD_OUTPUT}" | grep -Eq "=>|ld-linux|libc\\.so"; then
	echo "static link verified: ${BINARY} has no glibc runtime dependency"
else
	echo "static link verification failed: ${BINARY} is dynamically linked" >&2
	printf '%s\n' "${LDD_OUTPUT}" >&2
	exit 1
fi
