#!/usr/bin/env bash
# Verify the git hooks are correctly wired up.
# Exits 0 when configuration is correct, non-zero otherwise.

set -euo pipefail

hooks_path=$(git config --get core.hooksPath 2>/dev/null || true)

if [ "$hooks_path" != ".githooks" ]; then
    echo "FAIL: core.hooksPath is '${hooks_path:-<unset>}', expected '.githooks'" >&2
    echo "Fix: run 'make hooks' from this repo root" >&2
    exit 1
fi

for h in pre-commit pre-push; do
    if [ ! -x ".githooks/$h" ]; then
        echo "FAIL: .githooks/$h missing or not executable" >&2
        echo "Fix: run 'make hooks' from this repo root" >&2
        exit 1
    fi
done

if [ ! -x "scripts/pre-push-gate.sh" ]; then
    echo "FAIL: scripts/pre-push-gate.sh missing or not executable" >&2
    echo "Fix: run 'make hooks' from this repo root" >&2
    exit 1
fi

echo "OK: hooks configured -- core.hooksPath=.githooks, pre-commit + pre-push + pre-push-gate.sh executable"
