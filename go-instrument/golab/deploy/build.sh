#!/bin/sh
# Build a static binary and a minimal image from it. Run from the module root.
set -eu
mkdir -p build
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o build/golab ./cmd/golab
cp deploy/Dockerfile build/Dockerfile
docker build -t golab:dev build
