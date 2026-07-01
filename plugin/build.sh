#!/bin/bash
# Build the static linux/amd64 preloadd binary into the plugin src/ tree so the
# release action packages it into the .txz. Run by the release workflow.
set -euo pipefail

version="${PLUGIN_VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
out="src/usr/local/emhttp/plugins/watch-aware-preloader/preloadd"
mkdir -p "$(dirname "${out}")"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "${out}" ./cmd/preloadd
echo "built ${out} (version ${version})"
