#!/bin/bash
# Test rc.preloadd render: a fixture .cfg must produce a correct config.toml
# and cron fragment. Runs against a temp dir via the WAP_* env overrides.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RC="${REPO_ROOT}/src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd"

fail() { echo "FAIL: $1" >&2; exit 1; }
assert_contains() { grep -qF -- "$2" "$1" || fail "expected '$2' in $1:\n$(cat "$1")"; }
assert_not_contains() { grep -qF -- "$2" "$1" && fail "did not expect '$2' in $1" || true; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export WAP_FLASH="$work/flash"
export WAP_RUNTIME="$work/runtime"
export WAP_STATUS_PATH="$work/status.json"
# Sandbox the cron marker paths so the test never touches the real
# /var/log/plugins or the real .plg file on the host.
export WAP_PLUGIN_LOG_DIR="$work/plugins-log"
export WAP_PLG_FILE="$work/flash/watch-aware-preloader.plg"
mkdir -p "$WAP_FLASH" "$WAP_RUNTIME"

# default.cfg lives in the runtime tree; copy the real one in.
cp "${REPO_ROOT}/src/usr/local/emhttp/plugins/watch-aware-preloader/default.cfg" \
   "$WAP_RUNTIME/default.cfg"

# Fixture flash .cfg (what /update.php would have written).
cat > "$WAP_FLASH/watch-aware-preloader.cfg" <<'CFG'
SERVER_TYPE="emby"
SERVER_URL="http://media.example:8096"
USERS="alice, bob"
RAM_PERCENT="40"
TARGET_SECONDS="25"
MIN_HEAD_MB="8"
MAX_HEAD_MB="250"
TAIL_MB="1"
PATH_MAPS="/share=>/mnt/user; /media=>/mnt/user/media"
CRON_INTERVAL="10"
CFG

bash "$RC" render

cfg="$WAP_FLASH/config.toml"
cron="$WAP_FLASH/watch-aware-preloader.cron"
[ -f "$cfg" ] || fail "config.toml not generated"
[ -f "$cron" ] || fail "cron fragment not generated"

assert_contains "$cfg" 'type = "emby"'
assert_contains "$cfg" 'url = "http://media.example:8096"'
assert_contains "$cfg" 'enabled = ["alice", "bob"]'
assert_contains "$cfg" 'ram_percent = 40'
assert_contains "$cfg" 'target_seconds = 25'
assert_contains "$cfg" 'from = "/share"'
assert_contains "$cfg" 'to = "/mnt/user"'
assert_contains "$cfg" 'from = "/media"'
assert_contains "$cfg" 'to = "/mnt/user/media"'
assert_contains "$cfg" "status_path = \"$WAP_STATUS_PATH\""
assert_contains "$cfg" "secret_path = \"$WAP_FLASH/secrets.toml\""
assert_not_contains "$cfg" "api_key"
assert_contains "$cron" '*/10 * * * *'

# Absent flash .cfg -> falls back to default.cfg.
rm -f "$WAP_FLASH/watch-aware-preloader.cfg" "$cfg"
bash "$RC" render
assert_contains "$cfg" 'url = "http://localhost:8096"'
assert_contains "$cfg" 'enabled = []'

# Cron marker (PRs #24/#25 fix): a fresh `install` with PLG_FILE ABSENT must
# still create the /var/log/plugins/<name>.plg marker as a symlink ENTRY. It is
# a dangling symlink (target absent) - assert [ -L ], NOT [ -e ] - so
# update_cron can collate the cron fragment on install-by-URL.
rm -rf "$WAP_PLUGIN_LOG_DIR"
[ -e "$WAP_PLG_FILE" ] && fail "precondition: PLG_FILE should be absent for this check"
bash "$RC" install
marker="$WAP_PLUGIN_LOG_DIR/watch-aware-preloader.plg"
[ -L "$marker" ] || fail "cron marker symlink not created on fresh install"
[ -e "$marker" ] && fail "marker should be a DANGLING symlink (PLG_FILE absent)"

# --- TOML-injection hardening (hostile-review findings 1-3): values containing
# " \ or control chars must be escaped so config.toml stays valid and round-trips
# the literal, and a non-numeric numeric field must fall back to its default
# rather than inject an unquoted token. ---
cat > "$WAP_FLASH/watch-aware-preloader.cfg" <<'CFG'
SERVER_TYPE="emby"
SERVER_URL="http://x:8096/p\q"r"
USERS="al"ice, bob"
RAM_PERCENT="not-a-number"
TARGET_SECONDS="25"
MIN_HEAD_MB="8"
MAX_HEAD_MB="250"
TAIL_MB="1"
PATH_MAPS="/sh\are=>/mnt"x"
CRON_INTERVAL="10"
CFG
rm -f "$cfg"
bash "$RC" render
[ -f "$cfg" ] || fail "config.toml not generated (injection fixture)"
assert_not_contains "$cfg" "api_key"
# Non-numeric RAM_PERCENT falls back to the default (50); never injected unquoted.
assert_contains "$cfg" 'ram_percent = 50'

if python3 -c 'import tomllib' 2>/dev/null; then
    # Strongest check: the whole file parses AND the injecting values round-trip.
    python3 - "$cfg" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as fh:
    d = tomllib.load(fh)
assert d["server"]["url"] == 'http://x:8096/p\\q"r', d["server"]["url"]
assert d["users"]["enabled"] == ['al"ice', 'bob'], d["users"]["enabled"]
pm = d["path_map"]  # [[path_map]] is a top-level array-of-tables
assert pm[0]["from"] == '/sh\\are', pm[0]
assert pm[0]["to"] == '/mnt"x', pm[0]
assert "api_key" not in d.get("server", {})
print("  TOML round-trip OK (tomllib)")
PY
else
    # Fallback when tomllib is unavailable: assert the escaped bytes are present.
    assert_contains "$cfg" 'url = "http://x:8096/p\\q\"r"'
    assert_contains "$cfg" 'enabled = ["al\"ice", "bob"]'
    assert_contains "$cfg" 'from = "/sh\\are"'
    assert_contains "$cfg" 'to = "/mnt\"x"'
fi

echo "PASS: rc.preloadd render"
