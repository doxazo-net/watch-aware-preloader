#!/bin/bash
# Off-host test of `rc.preloadd estimate`: it must invoke the engine binary with
# `-estimate -config <config>`, bounded, and the usage string must list estimate.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
rc="${here}/../src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# A stub binary that records its args instead of contacting a server.
bin="${work}/preloadd"
cat > "$bin" <<'EOS'
#!/bin/bash
printf '%s\n' "$*" > "${WAP_ARGLOG}"
exit 0
EOS
chmod +x "$bin"

flash="${work}/flash"; mkdir -p "$flash"
: > "${flash}/watch-aware-preloader.cfg"

WAP_ARGLOG="${work}/args" \
WAP_FLASH="$flash" \
WAP_BIN="$bin" \
  bash "$rc" estimate >/dev/null 2>&1

args="$(cat "${work}/args" 2>/dev/null || true)"
fail=0
case "$args" in
    *"-estimate"*) ;; *) echo "FAIL: estimate did not pass -estimate (got: $args)"; fail=1 ;;
esac
case "$args" in
    *"-config"*"config.toml"*) ;; *) echo "FAIL: estimate did not pass -config <config> (got: $args)"; fail=1 ;;
esac

# Usage string lists the estimate subcommand. Capture the output first (the usage
# path exits 1, which under `pipefail` would fail a `... | grep` even on a match).
usage_out="$(bash "$rc" bogus 2>&1 || true)"
if ! grep -q 'estimate' <<<"$usage_out"; then
    echo "FAIL: usage string does not list 'estimate'"; fail=1
fi

if [ "$fail" -ne 0 ]; then exit 1; fi
echo "PASS: rc.preloadd estimate dispatch"
