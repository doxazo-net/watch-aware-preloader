#!/usr/bin/env bash
#
# smoke-install-by-url.sh -- CI smoke test for the install-by-URL cron-collation
# flow (issue #26).
#
# Background: Unraid's update_cron only collates a plugin's .cron fragment into
# the live crontab when a /var/log/plugins/<name>.plg marker exists. On
# install-by-URL the plugin manager persists that marker only AFTER the inline
# install script runs, so rc.preloadd's ensure_marker creates it itself
# (unconditionally, via `ln -sf`). This test drives the REAL rc.preloadd through
# its WAP_* env overrides and asserts the marker is created and that a stub
# update_cron collates the cron fragment -- in both the fresh-install
# (marker/.plg absent) and normal (.plg present) cases.
#
# Output style mirrors test/rc_preloadd_render_test.sh: `=== step ===` headers,
# `OK` on success, `FAIL: ...` + non-zero exit on the first failed assertion.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RC="${REPO_ROOT}/src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd"

fail() { echo "FAIL: $1" >&2; exit 1; }

[ -r "$RC" ] || fail "rc.preloadd not found or not readable at $RC"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Sandbox every path rc.preloadd touches so the test never reaches the real
# /boot, /var/log/plugins, or /usr/local/sbin on the host.
export WAP_FLASH="$work/flash"
export WAP_RUNTIME="$work/runtime"
export WAP_STATUS_PATH="$work/status.json"
export WAP_PLUGIN_LOG_DIR="$work/plugins-log"
export WAP_PLG_FILE="$work/flash/watch-aware-preloader.plg"
mkdir -p "$WAP_FLASH" "$WAP_RUNTIME"

# render falls back to default.cfg when the flash .cfg is absent; supply the real
# shipped default so install/render produce a valid cron fragment.
cp "${REPO_ROOT}/src/usr/local/emhttp/plugins/watch-aware-preloader/default.cfg" \
   "$WAP_RUNTIME/default.cfg"

marker="$WAP_PLUGIN_LOG_DIR/watch-aware-preloader.plg"
cron_fragment="$WAP_FLASH/watch-aware-preloader.cron"

# --- Stub update_cron: mimic Unraid's collator. Glob $WAP_PLUGIN_LOG_DIR/*.plg,
# strip each basename to <name>, and cat that plugin's .cron fragment (from the
# sandbox flash dir) into the target crontab file. Only the marker's presence and
# basename matter -- the symlink target is never dereferenced, matching real
# update_cron. WAP_FLASH and WAP_TEST_CRONTAB are inherited from this script. ---
export WAP_TEST_CRONTAB="$work/crontab"
stub="$work/update_cron"
cat > "$stub" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
: > "$WAP_TEST_CRONTAB"
shopt -s nullglob
for plg in "$WAP_PLUGIN_LOG_DIR"/*.plg; do
    name="$(basename "$plg" .plg)"
    frag="$WAP_FLASH/${name}.cron"
    if [ -f "$frag" ]; then cat "$frag" >> "$WAP_TEST_CRONTAB"; fi
done
STUB
chmod +x "$stub"
export WAP_UPDATE_CRON="$stub"

echo "=== case A: fresh install, .plg marker absent ==="
rm -rf "$WAP_PLUGIN_LOG_DIR"
[ -e "$WAP_PLG_FILE" ] && fail "precondition: PLG_FILE should be absent for case A"
: > "$WAP_TEST_CRONTAB"
bash "$RC" install
# Dangling-tolerant: the marker is a symlink whose target (PLG_FILE) does not yet
# exist, so assert [ -L ], NOT [ -e ].
[ -L "$marker" ] || fail "cron marker symlink not created on fresh install"
[ -e "$marker" ] && fail "case A marker should be a DANGLING symlink (PLG_FILE absent)"
[ -f "$cron_fragment" ] || fail "cron fragment not rendered on fresh install"
grep -qF -- '-once -config' "$WAP_TEST_CRONTAB" \
    || fail "stub update_cron did not collate the cron fragment on fresh install"
echo "OK"

echo "=== case B: normal install, .plg present ==="
touch "$WAP_PLG_FILE"
# Independence from case A: wipe the plugin-log dir so the marker case A created
# is gone. This forces case B to prove rc.preloadd's ensure_marker RE-creates the
# marker on the normal (.plg present) path, rather than passing on a leftover
# symlink that merely stopped dangling once PLG_FILE exists.
rm -f "$WAP_PLUGIN_LOG_DIR"/*.plg
: > "$WAP_TEST_CRONTAB"
bash "$RC" install
[ -L "$marker" ] || fail "cron marker symlink missing when PLG_FILE present"
[ -e "$marker" ] || fail "marker should resolve when PLG_FILE present"
grep -qF -- '-once -config' "$WAP_TEST_CRONTAB" \
    || fail "stub update_cron did not collate the cron fragment when PLG_FILE present"
# Exactly one collated line -- a duplicate/multiple-collation regression must fail.
[ "$(grep -cF -- '-once -config' "$WAP_TEST_CRONTAB")" -eq 1 ] \
    || fail "expected exactly one collated cron line when PLG_FILE present"
echo "OK"

echo "PASS: install-by-URL cron collation"
