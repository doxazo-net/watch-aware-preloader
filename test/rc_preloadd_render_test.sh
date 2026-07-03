#!/bin/bash
# Test rc.preloadd render: a fixture .cfg must produce a correct config.toml
# and cron fragment. Runs against a temp dir via the WAP_* env overrides.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RC="${REPO_ROOT}/src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd"

fail() { echo "FAIL: $1" >&2; exit 1; }
assert_contains() { grep -qF -- "$2" "$1" || fail "expected '$2' in $1:\n$(cat "$1")"; }
assert_not_contains() { if grep -qF -- "$2" "$1"; then fail "did not expect '$2' in $1"; fi; }

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
LIBRARIES="lib-1, lib-2"
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
assert_contains "$cfg" '[users]'
assert_contains "$cfg" 'enabled = ["alice", "bob"]'
assert_contains "$cfg" '[libraries]'
assert_contains "$cfg" 'enabled = ["lib-1", "lib-2"]'
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

# --- Control-char stripping (CR review finding 2): a stray control char (0x01)
# in a string field must be STRIPPED so config.toml stays valid TOML. ---
ctrl=$'\x01'
{
    printf 'SERVER_TYPE="emby"\n'
    printf 'SERVER_URL="http://localhost:8096"\n'
    printf 'USERS="al%sice, bob"\n' "$ctrl"
    printf 'RAM_PERCENT="50"\n'
    printf 'TARGET_SECONDS="20"\n'
    printf 'MIN_HEAD_MB="8"\n'
    printf 'MAX_HEAD_MB="250"\n'
    printf 'TAIL_MB="1"\n'
    printf 'PATH_MAPS="/sh%sare=>/mnt/user"\n' "$ctrl"
    printf 'CRON_INTERVAL="15"\n'
} > "$WAP_FLASH/watch-aware-preloader.cfg"
rm -f "$cfg"
bash "$RC" render
[ -f "$cfg" ] || fail "config.toml not generated (control-char fixture)"
if python3 -c 'import tomllib' 2>/dev/null; then
    python3 - "$cfg" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as fh:
    d = tomllib.load(fh)
# The 0x01 byte must be gone; the rest of each value survives.
assert d["users"]["enabled"] == ["alice", "bob"], d["users"]["enabled"]
assert d["path_map"][0]["from"] == "/share", d["path_map"][0]
print("  control char stripped, TOML valid (tomllib)")
PY
else
    assert_contains "$cfg" 'enabled = ["alice", "bob"]'
    assert_contains "$cfg" 'from = "/share"'
fi
# The raw control byte must not appear anywhere in the rendered file.
if LC_ALL=C grep -q "$ctrl" "$cfg"; then fail "control char leaked into config.toml"; fi

# --- CRON_INTERVAL=0 (CR review finding 4): 0 is invalid (*/0 never fires) and
# must fall back to the default 15, and the rendered step is always >= 1. ---
cat > "$WAP_FLASH/watch-aware-preloader.cfg" <<'CFG'
SERVER_TYPE="emby"
SERVER_URL="http://localhost:8096"
USERS=""
RAM_PERCENT="50"
TARGET_SECONDS="20"
MIN_HEAD_MB="8"
MAX_HEAD_MB="250"
TAIL_MB="1"
PATH_MAPS=""
CRON_INTERVAL="0"
CFG
rm -f "$cfg" "$cron"
bash "$RC" render
assert_contains "$cron" '*/15 * * * *'
assert_not_contains "$cron" '*/0 '

# --- write-pickers: assembles pickers.json from the three read-only
# subcommands, atomically and world-readable. ---
STUB_BIN="$work/preloadd"
cat > "$STUB_BIN" <<'STUB'
#!/bin/bash
case "$1" in
  list-users)
    python3 -m json.tool <<'JSON'
[{"id":"id-a","name":"Alice"}]
JSON
    ;;
  list-libraries)
    python3 -m json.tool <<'JSON'
[{"id":"lib-1","name":"Movies","type":"movies"}]
JSON
    ;;
  detect-pathmaps)
    python3 -m json.tool <<'JSON'
{"rules":[{"from":"/share/Movies","to":"/mnt/user/Movies","source":"docker"}],"unraid_unc_fallback":true}
JSON
    ;;
  *) exit 2 ;;
esac
STUB
chmod +x "$STUB_BIN"
printf 'SERVER_URL="http://tower:8096"\n' >> "$WAP_FLASH/watch-aware-preloader.cfg"
WAP_PICKERS_PATH="$work/pickers.json" WAP_BIN="$STUB_BIN" "$RC" write-pickers
grep -q '"server_url": *"http://tower:8096"' "$work/pickers.json" || fail "server_url not in cache"
grep -q '"id": "id-a"' "$work/pickers.json" || fail "users not merged"
grep -q '"id": "lib-1"' "$work/pickers.json" || fail "libraries not merged"
grep -q '"source": "docker"' "$work/pickers.json" || fail "pathmaps not merged"
# world-readable
perms="$(stat -c '%a' "$work/pickers.json" 2>/dev/null || stat -f '%Lp' "$work/pickers.json")"
[ "$perms" = "644" ] || fail "pickers.json not 0644 (got $perms)"

# --- write-pickers JSON-escaping: a SERVER_URL containing a double-quote
# (hand-edited .cfg, not run through the presave sanitizer) must be escaped in
# pickers.json rather than corrupting the JSON structure. ---
printf 'SERVER_URL="http://tow\"er:8096"\n' >> "$WAP_FLASH/watch-aware-preloader.cfg"
WAP_PICKERS_PATH="$work/pickers.json" WAP_BIN="$STUB_BIN" "$RC" write-pickers
grep -q 'tow\\"er' "$work/pickers.json" || fail "server_url quote not escaped in pickers.json"
if python3 -c 'import json' 2>/dev/null; then
    python3 - "$work/pickers.json" <<'PY'
import sys, json
with open(sys.argv[1]) as fh:
    d = json.load(fh)
assert d["server_url"] == 'http://tow"er:8096', d["server_url"]
print("  pickers.json valid JSON, server_url round-trips (json)")
PY
fi

echo "PASS: rc.preloadd render"
