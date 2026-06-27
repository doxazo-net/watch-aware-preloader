#!/usr/bin/env bash
#
# pre-push-gate.sh -- full local gate before push.
#
# Steps (in order, fast-fail):
#   1. gofmt          -- whole module formatted
#   2. go vet         -- static analysis
#   3. golangci-lint  -- full linter suite (SKIP with warning if not installed)
#   4. go test -race  -- tests with race detector
#   5. go build       -- cross-compile linux/amd64 for the daemon
#   6. PHP lint       -- phpstan + php-cs-fixer (only when .php/.page files exist
#                        and vendor/bin/phpstan is present)
#
# Exit 0 = all checks passed; non-zero = first failure.

set -euo pipefail

echo "=== gofmt ==="
UNFORMATTED=$(gofmt -l . 2>/dev/null || true)
if [ -n "$UNFORMATTED" ]; then
    echo "FAIL: the following files need formatting:"
    echo "$UNFORMATTED" | sed 's/^/  /'
    echo ""
    echo "Run: gofmt -w ."
    exit 1
fi
echo "OK"

echo ""
echo "=== go vet ==="
go vet ./...
echo "OK"

echo ""
echo "=== golangci-lint ==="
if ! command -v golangci-lint >/dev/null 2>&1; then
    echo "SKIP: golangci-lint not installed (install the repo's documented golangci-lint v2 toolchain -- see REQUIREMENTS.md)"
else
    golangci-lint run ./...
    echo "OK"
fi

echo ""
echo "=== go test -race ==="
CGO_ENABLED=1 go test -race -count=1 ./...
echo "OK"

echo ""
echo "=== go build (linux/amd64) ==="
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/preloadd
echo "OK"

echo ""
echo "=== PHP lint ==="
if find plugin/ -type f \( -name '*.php' -o -name '*.page' \) 2>/dev/null | grep -q . && [ -x vendor/bin/phpstan ]; then
    vendor/bin/phpstan analyse --no-progress
    vendor/bin/php-cs-fixer fix --dry-run --diff
    echo "OK"
else
    echo "SKIP: no PHP files under plugin/ or vendor/bin/phpstan not present"
fi

echo ""
echo "All hard checks passed."
