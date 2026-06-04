#!/bin/bash
set -euo pipefail

# Resolve repo root regardless of where the script is invoked from.
cd "$(dirname "$0")/.."

VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS="-w -s -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME"

# Targets to cross-compile. Add/remove pairs as needed.
PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "linux/arm"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

echo "Building pgbackup binaries (version=$VERSION, build_time=$BUILD_TIME)..."

mkdir -p ./bin
for platform in "${PLATFORMS[@]}"; do
  GOOS=${platform%/*}
  GOARCH=${platform#*/}

  # Windows binaries need the .exe suffix.
  ext=""
  [ "$GOOS" = "windows" ] && ext=".exe"

  out="./bin/pgbackup-${GOOS}-${GOARCH}${ext}"
  echo "  -> $out"

  # GOARM is honoured only when GOARCH=arm; harmless otherwise.
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" GOARM=7 \
    go build -ldflags="$LDFLAGS" -o "$out" .
done

chmod +x ./bin/pgbackup-*
echo "Build completed!"
