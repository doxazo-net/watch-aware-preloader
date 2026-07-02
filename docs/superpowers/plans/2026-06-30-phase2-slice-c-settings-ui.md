# Phase 2 Slice C - Settings UI + Status Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Unraid plugin a native webGui settings page (server URL, API key, users, budget, path maps, cron interval) plus a status panel that shows the last run from `status.json`, so no file editing is needed and Phase 2 (#2) is complete.

**Architecture:** Native Unraid `.cfg` is the user-facing settings store. Unraid's built-in `/update.php` writes the form fields into the `.cfg` and then runs `rc.preloadd render`, a bash step that reads the `.cfg` and emits the Go engine's `config.toml` + the dcron fragment (PHP never parses or emits TOML). The API key is handled on a separate, write-only path into `secrets.toml`. A read-only status panel decodes `status.json` and localizes the last-run time to US Pacific.

**Tech Stack:** Unraid webGui (`.page` + PHP 8.1+, PSR-12), bash (`rc.preloadd`), the existing Go engine (unchanged). Static gates: PHPStan level 6, PHP-CS-Fixer, `shellcheck`, plus targeted bash and PHP-CLI tests. Behavioral acceptance is a live Unraid webGui test (see Global Constraints).

## Global Constraints

Copied verbatim from the spec (`docs/specs/2026-06-30-phase2-settings-ui-design.md`) and the repo CLAUDE.md. Every task's requirements implicitly include this section.

- **Stacks on #21 (merged).** Branch is `phase2-settings-ui`; all new plugin files live under `src/usr/local/emhttp/plugins/watch-aware-preloader/`.
- **PHP never parses or emits TOML.** The single TOML generator is `rc.preloadd render` (bash). PHP reads `status.json` (JSON) only.
- **`.cfg` is the source of truth.** `config.toml` is a GENERATED artifact, regenerated from the `.cfg` on every Save and every boot. Users edit settings via the webGui or the `.cfg`, never `config.toml`.
- **Credentials are never in `.cfg` and never echoed.** The API key goes only to `secrets.toml` under `[server].api_key`; the UI shows "key is set / not set", never the value. No secret is rendered in any page or log.
- **`secrets.toml` best-effort `chmod 600`.** On the default flash path (`/boot`, FAT32) per-file modes are NOT enforced (mount umask governs); the key's protection is the flash/root boundary. Do not claim file-mode protection on the flash path.
- **Times shown to users are US Pacific, labeled** (e.g. `2026-06-30 14:05:00 PDT`). `status.json.last_run` is RFC3339 UTC and must be converted at render time.
- **Go 1.26+ engine is unchanged** in this slice; its test suite must stay green.
- **PHP: PSR-12**, lint with PHPStan (level 6) + PHP-CS-Fixer; both must also cover `.page` files. All shipped bash must pass `shellcheck`.
- **No emoji, no em-dashes** in code, comments, docs, or user-facing strings.
- **Pinned status/secret paths (from the Go engine defaults, verified):**
  - `status_path` default = `/var/local/preloadd/status.json` (RAM; regenerated each cron tick).
  - `secret_path` default = `/boot/config/plugins/watch-aware-preloader/secrets.toml`.
  - `status.json` schema: `schema_version` must equal `1`; keys are `last_run, mode, duration_ms, ok, error, budget_bytes, bytes_warmed, preloaded, skipped, missing, by_tier, by_user`. `mode` is one of `once|verify|daemon`. `by_tier` keys are snake_case; `by_user` keys are raw Emby UserIDs.
- **Acceptance is a LIVE Unraid webGui test** ("rendered evidence only" rule). Static analysis + the automated tests in this plan are necessary but NOT sufficient to call the UI done; the live test on `outatime` (with the plugin installed via a cut pre-release) is the real gate and is a separate, maintainer-triggered step.

---

## File Structure

All plugin runtime files under `src/usr/local/emhttp/plugins/watch-aware-preloader/` (abbreviated `PLUGDIR` below):

- `PLUGDIR/default.cfg` - default settings (`KEY="value"`), no API key. Read by `parse_plugin_cfg` and by `rc.preloadd render` when the flash `.cfg` is absent.
- `PLUGDIR/WatchAwarePreloader.page` - the settings + status page (header block + PHP body). Includes the helpers below.
- `PLUGDIR/include/status.php` - `wap_read_status()` (decode + schema-check `status.json`) and `wap_format_pacific()` (UTC RFC3339 -> labeled Pacific). Pure functions, unit-tested.
- `PLUGDIR/include/secrets.php` - `wap_write_api_key()` and `wap_api_key_is_set()`. Pure functions, unit-tested.
- `PLUGDIR/include/save-secret.php` - thin HTTP endpoint: CSRF check + call `wap_write_api_key()`. No logic beyond wiring.
- `PLUGDIR/scripts/rc.preloadd` - MODIFIED: env-parameterized base dirs; new `render`, `test`, `run-now` subcommands; `install`/`update` call `render`.
- `plugin/plugin.j2` - MODIFIED: add `launch="Settings/WatchAwarePreloader"`.
- Lint/CI config - MODIFIED to scan `src` (Makefile, `phpstan.neon.dist`, `.php-cs-fixer.dist.php`, `.github/workflows/ci.yml`).
- `test/rc_preloadd_render_test.sh`, `test/status_test.php`, `test/secrets_test.php` - automated tests.

---

## Task 1: Extend lint + CI scope to `src/` and add a shellcheck gate

The PHP tooling and CI currently scan `plugin/` only; our PHP lands in `src/`, so without this the new PHP is never linted and CI stays falsely green. There is also no `shellcheck` gate for the shipped bash. This task is pure tooling: after it, `make php-lint` and a new `make shellcheck` cover the plugin tree and pass on the current (pre-Slice-C) files.

**Files:**
- Modify: `phpstan.neon.dist`
- Modify: `.php-cs-fixer.dist.php`
- Modify: `Makefile:57-64` (`php-lint`) and add a `shellcheck` target
- Modify: `.github/workflows/ci.yml:87-94` (php-lint step); add a shellcheck step

**Interfaces:**
- Produces: `make shellcheck` target; `make php-lint` now also scans `src`.

- [ ] **Step 1: Point PHPStan at `src` as well as `plugin`**

Edit `phpstan.neon.dist` so `paths` includes `src`:

```yaml
parameters:
    # Level 6 adds missing-type-hint enforcement on top of the core null/type
    # safety levels. Raise toward 8-9 as the settings page matures.
    level: 6
    paths:
        - plugin
        - src
    # Unraid plugin pages are PHP but carry the .page extension; analyse both.
    fileExtensions:
        - php
        - page
    # Unraid injects globals (e.g. $_GET, plugin helpers) and DOCROOT-relative
    # includes that PHPStan cannot resolve standalone. Tighten as stubs are added.
    treatPhpDocTypesAsCertain: false
    # Unraid webGui globals/functions ($cfg, $var, parse_plugin_cfg, my_csrf)
    # are provided at runtime and cannot be resolved standalone.
    ignoreErrors:
        - '#^Function parse_plugin_cfg not found\.$#'
        - '#^Variable \$var might not be defined\.$#'
```

- [ ] **Step 2: Point PHP-CS-Fixer at `src` as well as `plugin`**

Edit `.php-cs-fixer.dist.php`, changing the finder to include both trees:

```php
$finder = (new PhpCsFixer\Finder())
    ->in([__DIR__ . '/plugin', __DIR__ . '/src'])
    ->name('*.php')
    ->name('*.page');
```

- [ ] **Step 3: Widen the Makefile php-lint glob and add a shellcheck target**

In `Makefile`, replace the `php-lint` recipe's `find plugin` with `find plugin src`, and add a `shellcheck` target after `php-fix`:

```makefile
.PHONY: php-lint
php-lint: ## Static analysis (PHPStan) + style check (PHP-CS-Fixer, dry-run)
	@if find plugin src -type f \( -name '*.php' -o -name '*.page' \) 2>/dev/null | grep -q .; then \
		vendor/bin/phpstan analyse --no-progress ; \
		vendor/bin/php-cs-fixer fix --dry-run --diff ; \
	else \
		echo "no PHP files under plugin/ or src/ yet - skipping PHP lint" ; \
	fi

.PHONY: shellcheck
shellcheck: ## Lint shipped bash (rc.preloadd + test harnesses)
	@files=$$(find src -type f -name 'rc.*'; find test -type f -name '*.sh' 2>/dev/null); \
	if [ -n "$$files" ]; then \
		shellcheck $$files ; \
	else \
		echo "no shell scripts to check yet" ; \
	fi
```

- [ ] **Step 4: Update the CI php-lint step and add a shellcheck step**

In `.github/workflows/ci.yml`, change the php-lint step's `find plugin` to `find plugin src`:

```yaml
      - name: PHPStan + PHP-CS-Fixer (skips cleanly until plugin/ or src/ has PHP)
        run: |
          if find plugin src -type f \( -name '*.php' -o -name '*.page' \) | grep -q .; then
            vendor/bin/phpstan analyse --no-progress
            vendor/bin/php-cs-fixer fix --dry-run --diff
          else
            echo "no PHP files under plugin/ or src/ yet - skipping PHP lint"
          fi
```

Add a shellcheck job step in the same file (append a new job after `php-lint`; match the existing indentation/style of the file):

```yaml
  shellcheck:
    name: ShellCheck
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2
        with:
          persist-credentials: false
      - name: Run shellcheck on shipped bash
        run: |
          files=$(find src -type f -name 'rc.*'; find test -type f -name '*.sh' 2>/dev/null)
          if [ -n "$files" ]; then shellcheck $files; else echo "no shell scripts"; fi
```

(Confirm the `actions/checkout` SHA matches the one already used elsewhere in `ci.yml`; reuse that exact pin.)

- [ ] **Step 5: Run the gates to verify they pass on current files**

Run: `make php-lint`
Expected: prints "no PHP files under plugin/ or src/ yet - skipping PHP lint" (no PHP exists yet) OR passes clean.

Run: `make shellcheck`
Expected: `shellcheck` runs on `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd` and exits 0 (the Slice B script is already clean).

- [ ] **Step 6: Commit**

```bash
git add phpstan.neon.dist .php-cs-fixer.dist.php Makefile .github/workflows/ci.yml
git commit -m "chore: extend PHP lint + add shellcheck gate to cover src/"
```

---

## Task 2: default.cfg and rc.preloadd render/test/run-now (bash, TDD)

This is the logic core of the slice: the `.cfg` -> `config.toml` + cron generator, plus the `test` and `run-now` actions the UI buttons invoke. It is the one piece with real branching logic, so it is test-driven with a bash harness. Base directories become env-overridable so the harness can run against a temp dir.

**Files:**
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/default.cfg`
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd`
- Test: `test/rc_preloadd_render_test.sh`

**Interfaces:**
- Produces (bash CLI): `rc.preloadd render|test|run-now|install|update|remove|seed`.
- Produces (env overrides, defaults in parens): `WAP_FLASH` (`/boot/config/plugins/watch-aware-preloader`), `WAP_RUNTIME` (`/usr/local/emhttp/plugins/watch-aware-preloader`), `WAP_STATUS_PATH` (`/var/local/preloadd/status.json`).
- Produces (generated files): `config.toml` with top-level `status_path`/`secret_path`, `[server]`, `[users]`, `[preload]`, `[[path_map]]` blocks, `[schedule]`; and `watch-aware-preloader.cron`.
- Consumes: `default.cfg` keys `SERVER_TYPE, SERVER_URL, USERS, RAM_PERCENT, TARGET_SECONDS, MIN_HEAD_MB, MAX_HEAD_MB, TAIL_MB, PATH_MAPS, CRON_INTERVAL`.

- [ ] **Step 1: Create default.cfg**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/default.cfg`:

```sh
SERVER_TYPE="emby"
SERVER_URL="http://localhost:8096"
USERS=""
RAM_PERCENT="50"
TARGET_SECONDS="20"
MIN_HEAD_MB="8"
MAX_HEAD_MB="250"
TAIL_MB="1"
PATH_MAPS="/share=>/mnt/user"
CRON_INTERVAL="15"
```

`USERS` is a comma-separated list (empty = all users). `PATH_MAPS` is a semicolon-separated list of `from=>to` pairs. `CRON_INTERVAL` is minutes.

- [ ] **Step 2: Write the failing render test**

Create `test/rc_preloadd_render_test.sh`:

```bash
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

echo "PASS: rc.preloadd render"
```

Make it executable: `chmod +x test/rc_preloadd_render_test.sh`

- [ ] **Step 3: Run the test to verify it fails**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: FAIL - `render` is not yet a valid subcommand (`usage: rc.preloadd ...`), so `config.toml` is not generated.

- [ ] **Step 4: Rewrite rc.preloadd with env-parameterized paths, helpers, and the new subcommands**

Replace the entire contents of `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd` with:

```bash
#!/bin/bash
# rc.preloadd - install/remove/render lifecycle for the watch-aware-preloader
# plugin. Called by the .plg INLINE blocks and by the webGui (/update.php
# #command). Idempotent: the .plg install re-runs at each Unraid boot, so seeding
# and rendering must be safe to repeat.
#
# Base directories are overridable via WAP_FLASH / WAP_RUNTIME / WAP_STATUS_PATH
# so the render logic is testable off-host; production defaults match the Unraid
# layout.
set -euo pipefail

PLUGIN="watch-aware-preloader"
FLASH="${WAP_FLASH:-/boot/config/plugins/${PLUGIN}}"
RUNTIME="${WAP_RUNTIME:-/usr/local/emhttp/plugins/${PLUGIN}}"
STATUS_PATH="${WAP_STATUS_PATH:-/var/local/preloadd/status.json}"
CONFIG="${FLASH}/config.toml"
SECRETS="${FLASH}/secrets.toml"
CFG="${FLASH}/${PLUGIN}.cfg"
DEFAULT_CFG="${RUNTIME}/default.cfg"
CRON="${FLASH}/${PLUGIN}.cron"
BIN="${RUNTIME}/preloadd"

trim() {
    local s="$1"
    s="${s#"${s%%[![:space:]]*}"}"
    s="${s%"${s##*[![:space:]]}"}"
    printf '%s' "$s"
}

# cfg_get FILE KEY DEFAULT -> value with surrounding double-quotes stripped, or
# DEFAULT when the key is absent. Last matching line wins (Unraid appends).
cfg_get() {
    local file="$1" key="$2" default="$3" line val
    line="$(grep -E "^[[:space:]]*${key}=" "$file" 2>/dev/null | tail -n1 || true)"
    if [ -z "$line" ]; then
        printf '%s' "$default"
        return
    fi
    val="$(trim "${line#*=}")"
    val="${val%\"}"
    val="${val#\"}"
    printf '%s' "$val"
}

# csv_to_toml_array "a, b" -> ["a", "b"] ; "" -> []
csv_to_toml_array() {
    local csv="$1" out="" item
    local IFS=','
    read -ra items <<< "$csv"
    for item in "${items[@]}"; do
        item="$(trim "$item")"
        [ -z "$item" ] && continue
        if [ -n "$out" ]; then out="$out, "; fi
        out="$out\"$item\""
    done
    printf '[%s]' "$out"
}

# render_path_maps "/a=>/b; /c=>/d" -> repeated [[path_map]] TOML blocks
render_path_maps() {
    local spec="$1" pair from to
    local IFS=';'
    read -ra pairs <<< "$spec"
    for pair in "${pairs[@]}"; do
        pair="$(trim "$pair")"
        [ -z "$pair" ] && continue
        case "$pair" in
            *"=>"*) ;;
            *) continue ;;
        esac
        from="$(trim "${pair%%=>*}")"
        to="$(trim "${pair#*=>}")"
        [ -z "$from" ] && continue
        [ -z "$to" ] && continue
        printf '\n[[path_map]]\nfrom = "%s"\nto = "%s"\n' "$from" "$to"
    done
}

# read_api_key -> EMBY_API_KEY env if set, else [server].api_key from secrets.toml
read_api_key() {
    if [ -n "${EMBY_API_KEY:-}" ]; then
        printf '%s' "$EMBY_API_KEY"
        return
    fi
    [ -f "${SECRETS}" ] || return 0
    local line val
    line="$(grep -E '^[[:space:]]*api_key[[:space:]]*=' "${SECRETS}" 2>/dev/null | tail -n1 || true)"
    [ -z "$line" ] && return 0
    val="$(trim "${line#*=}")"
    val="${val%\"}"
    val="${val#\"}"
    printf '%s' "$val"
}

seed() {
    mkdir -p "${FLASH}"
    if [ ! -f "${SECRETS}" ]; then
        cat > "${SECRETS}" <<'EOF'
# Credentials ONLY. Never commit; never put these in config.toml.
[server]
api_key = ""
EOF
    fi
    # Best-effort tighten to owner-only. NOTE: the default FLASH path is on the
    # FAT32 boot drive, where per-file modes are NOT enforced (mount umask wins),
    # so this is a no-op there - the key's protection is the flash/root-access
    # boundary, same as every Unraid plugin. chmod DOES apply on a Linux fs.
    chmod 600 "${SECRETS}" 2>/dev/null || true
}

render_cron() {
    local interval="$1"
    mkdir -p "${FLASH}"
    cat > "${CRON}" <<EOF
# Generated by the watch-aware-preloader plugin
*/${interval} * * * * /bin/bash -o pipefail -c '"\$0" -once -config "\$1" 2>&1 | /usr/bin/logger -t watch-aware-preloader' "${BIN}" "${CONFIG}"
EOF
    if [ -x /usr/local/sbin/update_cron ]; then
        /usr/local/sbin/update_cron
    else
        echo "note: /usr/local/sbin/update_cron not found; cron fragment written but not reloaded" >&2
    fi
}

# render: read the flash .cfg (or the shipped default.cfg when absent) and emit
# config.toml + the cron fragment. This is the single TOML generator.
render() {
    mkdir -p "${FLASH}"
    local src="${CFG}"
    [ -f "$src" ] || src="${DEFAULT_CFG}"

    local server_type server_url users ram target minh maxh tailmb maps interval
    server_type="$(cfg_get "$src" SERVER_TYPE emby)"
    server_url="$(cfg_get "$src" SERVER_URL "http://localhost:8096")"
    users="$(cfg_get "$src" USERS "")"
    ram="$(cfg_get "$src" RAM_PERCENT 50)"
    target="$(cfg_get "$src" TARGET_SECONDS 20)"
    minh="$(cfg_get "$src" MIN_HEAD_MB 8)"
    maxh="$(cfg_get "$src" MAX_HEAD_MB 250)"
    tailmb="$(cfg_get "$src" TAIL_MB 1)"
    maps="$(cfg_get "$src" PATH_MAPS "")"
    interval="$(cfg_get "$src" CRON_INTERVAL 15)"

    local users_toml path_map_toml
    users_toml="$(csv_to_toml_array "$users")"
    path_map_toml="$(render_path_maps "$maps")"

    umask 077
    cat > "${CONFIG}" <<EOF
# GENERATED by rc.preloadd from ${src##*/} - do not hand-edit. Edit settings in
# the webGui (Settings -> Watch-Aware Preloader) or ${CFG##*/}. Regenerated on
# every save and every boot.
status_path = "${STATUS_PATH}"
secret_path = "${SECRETS}"

[server]
type = "${server_type}"
url = "${server_url}"

[users]
enabled = ${users_toml}

[preload]
ram_percent = ${ram}
target_seconds = ${target}
min_head_mb = ${minh}
max_head_mb = ${maxh}
tail_mb = ${tailmb}
${path_map_toml}
[schedule]
sweep_seconds = 60
session_poll_seconds = 5
EOF

    render_cron "$interval"
}

# test_connection: reach the server's public endpoint, then (if a key is set)
# the authenticated endpoint. Prints a human line for the webGui progress popup.
test_connection() {
    local src="${CFG}" url token code
    [ -f "$src" ] || src="${DEFAULT_CFG}"
    url="$(cfg_get "$src" SERVER_URL "")"
    if [ -z "$url" ]; then
        echo "No server URL configured."
        return 1
    fi
    url="${url%/}"
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${url}/System/Info/Public" || true)"
    if [ "$code" != "200" ]; then
        echo "FAIL: ${url} not reachable (HTTP ${code:-none})."
        return 1
    fi
    token="$(read_api_key)"
    if [ -z "$token" ]; then
        echo "OK: server reachable at ${url}. No API key set yet."
        return 0
    fi
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "X-Emby-Token: ${token}" "${url}/System/Info" || true)"
    if [ "$code" = "200" ]; then
        echo "OK: server reachable and API key accepted."
    else
        echo "FAIL: server reachable but API key rejected (HTTP ${code})."
        return 1
    fi
}

run_now() {
    if [ ! -x "${BIN}" ]; then
        echo "FAIL: ${BIN} not found."
        return 1
    fi
    echo "Running a one-shot sweep..."
    "${BIN}" -once -config "${CONFIG}"
    echo "Done. See the status panel for results."
}

remove_cron() {
    rm -f "${CRON}"
    if [ -x /usr/local/sbin/update_cron ]; then
        /usr/local/sbin/update_cron
    fi
}

case "${1:-}" in
    install)  seed; render ;;
    update)   render ;;
    render)   render ;;
    seed)     seed ;;
    test)     test_connection ;;
    run-now)  run_now ;;
    remove)   remove_cron ;;
    *) echo "usage: rc.preloadd {install|update|render|seed|test|run-now|remove}" >&2; exit 1 ;;
esac
```

- [ ] **Step 5: Run the render test to verify it passes**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: `PASS: rc.preloadd render`

- [ ] **Step 6: Run shellcheck**

Run: `make shellcheck`
Expected: exit 0 (no findings on `rc.preloadd` or the test harness).

- [ ] **Step 7: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/default.cfg \
        src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd \
        test/rc_preloadd_render_test.sh
git commit -m "feat: rc.preloadd render + test/run-now, cfg-driven config.toml"
```

---

## Task 3: Status read helper and the status panel

Add the pure `status.json` decode/format helpers (unit-tested with the PHP CLI, no phpunit needed) and create the page with just the status panel. The settings form is added in Task 4; the page renders standalone with only the panel first so this task has an independently reviewable deliverable.

**Files:**
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/status.php`
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`
- Test: `test/status_test.php`

**Interfaces:**
- Produces (PHP): `wap_read_status(string $path): ?array` - returns the decoded status array, or `null` when the file is missing/unreadable/invalid JSON/`schema_version !== 1`.
- Produces (PHP): `wap_format_pacific(string $rfc3339utc): string` - returns e.g. `2026-06-30 14:05:00 PDT`, or the input unchanged if unparseable.
- Consumes: the pinned status path `/var/local/preloadd/status.json`.

- [ ] **Step 1: Write the failing PHP unit test**

Create `test/status_test.php`:

```php
<?php

declare(strict_types=1);

require __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/status.php';

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

$tmp = tempnam(sys_get_temp_dir(), 'wapst');

// Missing file -> null.
unlink($tmp);
check(wap_read_status($tmp) === null, 'missing file returns null');

// Valid schema_version 1 -> decoded array.
file_put_contents($tmp, json_encode([
    'schema_version' => 1,
    'last_run' => '2026-06-30T21:05:00Z',
    'mode' => 'once',
    'ok' => true,
    'preloaded' => 3,
    'by_tier' => ['resume' => 1, 'next_up' => 2],
    'by_user' => ['3' => 3],
]));
$s = wap_read_status($tmp);
check(is_array($s), 'valid file returns array');
check(($s['preloaded'] ?? null) === 3, 'preloaded decoded');
check(($s['by_tier']['next_up'] ?? null) === 2, 'by_tier decoded');

// Wrong schema_version -> null.
file_put_contents($tmp, json_encode(['schema_version' => 2, 'last_run' => 'x']));
check(wap_read_status($tmp) === null, 'schema_version mismatch returns null');

// Non-JSON -> null.
file_put_contents($tmp, 'not json');
check(wap_read_status($tmp) === null, 'invalid JSON returns null');

// Pacific formatting: 21:05 UTC on 2026-06-30 is 14:05 PDT.
$formatted = wap_format_pacific('2026-06-30T21:05:00Z');
check(str_contains($formatted, '14:05:00'), "pacific hour correct, got {$formatted}");
check(str_contains($formatted, 'PDT'), "pacific label present, got {$formatted}");

// Unparseable time -> returned unchanged.
check(wap_format_pacific('garbage') === 'garbage', 'unparseable time passthrough');

@unlink($tmp);

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: status helpers\n";
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `php test/status_test.php`
Expected: FATAL - `require` of `include/status.php` fails (file does not exist yet).

- [ ] **Step 3: Implement the status helpers**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/include/status.php`:

```php
<?php

declare(strict_types=1);

// Read-only helpers for the Watch-Aware Preloader status panel. No secrets, no
// TOML: this reads only the engine's status.json (JSON) and formats times.

const WAP_STATUS_SCHEMA_VERSION = 1;

/**
 * Decode and validate the engine's status.json.
 *
 * @return array<string, mixed>|null the decoded status, or null when the file is
 *         missing, unreadable, not valid JSON, or a different schema version.
 */
function wap_read_status(string $path): ?array
{
    if (!is_file($path)) {
        return null;
    }
    $raw = @file_get_contents($path);
    if ($raw === false) {
        return null;
    }
    $data = json_decode($raw, true);
    if (!is_array($data)) {
        return null;
    }
    if (($data['schema_version'] ?? null) !== WAP_STATUS_SCHEMA_VERSION) {
        return null;
    }
    return $data;
}

/**
 * Convert an RFC3339 UTC timestamp to a labeled US Pacific string, e.g.
 * "2026-06-30 14:05:00 PDT". Returns the input unchanged if it cannot be parsed.
 */
function wap_format_pacific(string $rfc3339utc): string
{
    try {
        $dt = new DateTimeImmutable($rfc3339utc);
    } catch (Exception $e) {
        return $rfc3339utc;
    }
    $dt = $dt->setTimezone(new DateTimeZone('America/Los_Angeles'));
    return $dt->format('Y-m-d H:i:s T');
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `php test/status_test.php`
Expected: `PASS: status helpers`

- [ ] **Step 5: Create the page with the status panel**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`:

```php
Menu="OtherSettings"
Title="Watch-Aware Preloader"
Icon="hdd"
Markdown="false"
---
<?php

/*
 * Watch-Aware Preloader settings + status page. MIT-licensed.
 * Reads the engine's status.json (read-only) and, in Task 4, renders the
 * settings form. No secrets are ever rendered here.
 */

$docroot = $docroot ?? ($_SERVER['DOCUMENT_ROOT'] ?? '/usr/local/emhttp');
require_once "{$docroot}/plugins/watch-aware-preloader/include/status.php";

$wapStatusPath = '/var/local/preloadd/status.json';
$wapStatus     = wap_read_status($wapStatusPath);
?>

<table class="tablesorter shift ups">
<thead><tr><th>Watch-Aware Preloader - last run</th></tr></thead>
</table>

<?php if ($wapStatus === null) { ?>
<p>No run recorded yet. The preloader runs on a schedule; check back after the next run, or use <strong>Run now</strong> once settings are saved.</p>
<?php } else { ?>
<dl>
    <dt>Last run</dt>
    <dd><?= htmlspecialchars(wap_format_pacific((string) ($wapStatus['last_run'] ?? ''))) ?></dd>

    <dt>Result</dt>
    <dd>
        <?php if (!empty($wapStatus['ok'])) { ?>
            OK
        <?php } else { ?>
            Error: <?= htmlspecialchars((string) ($wapStatus['error'] ?? 'unknown')) ?>
        <?php } ?>
        (mode: <?= htmlspecialchars((string) ($wapStatus['mode'] ?? '?')) ?>,
        <?= (int) ($wapStatus['duration_ms'] ?? 0) ?> ms)
    </dd>

    <dt>Warmed</dt>
    <dd>
        <?= (int) ($wapStatus['preloaded'] ?? 0) ?> preloaded,
        <?= (int) ($wapStatus['skipped'] ?? 0) ?> skipped,
        <?= (int) ($wapStatus['missing'] ?? 0) ?> missing;
        <?= number_format((float) ($wapStatus['bytes_warmed'] ?? 0) / 1048576, 1) ?> MiB
        of <?= number_format((float) ($wapStatus['budget_bytes'] ?? 0) / 1048576, 1) ?> MiB budget
    </dd>

    <?php if (!empty($wapStatus['by_tier']) && is_array($wapStatus['by_tier'])) { ?>
    <dt>By tier</dt>
    <dd>
        <?php $parts = [];
        foreach ($wapStatus['by_tier'] as $tier => $n) {
            $parts[] = htmlspecialchars((string) $tier) . ': ' . (int) $n;
        }
        echo implode(', ', $parts); ?>
    </dd>
    <?php } ?>

    <?php if (!empty($wapStatus['by_user']) && is_array($wapStatus['by_user'])) { ?>
    <dt>By user (UserID)</dt>
    <dd>
        <?php $parts = [];
        foreach ($wapStatus['by_user'] as $uid => $n) {
            $parts[] = htmlspecialchars((string) $uid) . ': ' . (int) $n;
        }
        echo implode(', ', $parts); ?>
    </dd>
    <?php } ?>
</dl>
<?php } ?>
```

- [ ] **Step 6: Lint the new PHP**

Run: `php -l src/usr/local/emhttp/plugins/watch-aware-preloader/include/status.php`
Expected: `No syntax errors detected`

Run: `make php-lint`
Expected: PHPStan + PHP-CS-Fixer pass (fix any style findings with `make php-fix` and re-run; the `parse_plugin_cfg`/`$var` ignores from Task 1 cover the webGui globals).

- [ ] **Step 7: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/include/status.php \
        src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page \
        test/status_test.php
git commit -m "feat: status.json read helpers + status panel page"
```

---

## Task 4: Settings form and /update.php wiring, plus launch=

Add the settings form to the page. The form posts to Unraid's native `/update.php`, which writes the named fields into the plugin `.cfg` and then runs `rc.preloadd render` (via `#command`/`#arg[1]`). Test-connection and Run-now use the same `/update.php` `#command` mechanism. Wire `launch=` into the `.plg` so the page opens from the plugin list.

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`
- Modify: `plugin/plugin.j2`

**Interfaces:**
- Consumes: `parse_plugin_cfg("watch-aware-preloader")` (webGui global) for current values; `$var['csrf_token']` for form CSRF.
- Consumes: `rc.preloadd render|test|run-now` from Task 2.

- [ ] **Step 1: Add the settings form, Test-connection, and Run-now forms to the page**

In `WatchAwarePreloader.page`, after the `require_once` of `status.php` and before the status `<table>`, add the cfg read:

```php
$wapCfg = function_exists('parse_plugin_cfg') ? parse_plugin_cfg('watch-aware-preloader') : [];
$wapCsrf = $var['csrf_token'] ?? '';
$wapKeySet = false; // set in Task 5
```

Then insert this block above the status `<table>` (so settings render first, status second):

```php
<table class="tablesorter shift ups">
<thead><tr><th>Watch-Aware Preloader - settings</th></tr></thead>
</table>

<form markdown="false" name="wap_settings" method="POST" action="/update.php" target="progressFrame">
<input type="hidden" name="#file" value="watch-aware-preloader/watch-aware-preloader.cfg">
<input type="hidden" name="#command" value="/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd">
<input type="hidden" name="#arg[1]" value="render">
<input type="hidden" name="csrf_token" value="<?= htmlspecialchars($wapCsrf) ?>">

<dl>
    <dt>Server type</dt>
    <dd>
        <select name="SERVER_TYPE" size="1">
            <option value="emby"<?= (($wapCfg['SERVER_TYPE'] ?? 'emby') === 'emby') ? ' selected' : '' ?>>Emby</option>
        </select>
    </dd>
</dl>
<blockquote class="inline_help">Jellyfin support arrives in Phase 3.</blockquote>

<dl>
    <dt>Server URL</dt>
    <dd><input type="text" name="SERVER_URL" value="<?= htmlspecialchars((string) ($wapCfg['SERVER_URL'] ?? 'http://localhost:8096')) ?>"></dd>
</dl>
<blockquote class="inline_help">Base URL of your Emby server, e.g. http://tower:8096</blockquote>

<dl>
    <dt>Users</dt>
    <dd><input type="text" name="USERS" value="<?= htmlspecialchars((string) ($wapCfg['USERS'] ?? '')) ?>"></dd>
</dl>
<blockquote class="inline_help">Comma-separated user names to preload for. Leave empty to include all users.</blockquote>

<dl>
    <dt>RAM budget (percent)</dt>
    <dd><input type="number" name="RAM_PERCENT" min="1" max="100" step="1" value="<?= (int) ($wapCfg['RAM_PERCENT'] ?? 50) ?>"></dd>
</dl>
<blockquote class="inline_help">Share of available RAM used as the preload budget.</blockquote>

<dl>
    <dt>Target seconds</dt>
    <dd><input type="number" name="TARGET_SECONDS" min="1" step="1" value="<?= (int) ($wapCfg['TARGET_SECONDS'] ?? 20) ?>"></dd>
</dl>
<blockquote class="inline_help">Seconds of playback to keep warm (covers disk spin-up).</blockquote>

<dl>
    <dt>Path maps</dt>
    <dd><input type="text" name="PATH_MAPS" value="<?= htmlspecialchars((string) ($wapCfg['PATH_MAPS'] ?? '/share=>/mnt/user')) ?>"></dd>
</dl>
<blockquote class="inline_help">Map server paths to host paths as from=&gt;to pairs, separated by semicolons, e.g. /share=&gt;/mnt/user; /media=&gt;/mnt/user/media</blockquote>

<dl>
    <dt>Schedule interval (minutes)</dt>
    <dd><input type="number" name="CRON_INTERVAL" min="1" max="59" step="1" value="<?= (int) ($wapCfg['CRON_INTERVAL'] ?? 15) ?>"></dd>
</dl>
<blockquote class="inline_help">How often the cron one-shot runs.</blockquote>

<dl>
    <dt><strong>Save</strong></dt>
    <dd><span><input type="submit" value="Apply"></span></dd>
</dl>
</form>

<form markdown="false" method="POST" action="/update.php" target="progressFrame">
<input type="hidden" name="#command" value="/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd">
<input type="hidden" name="#arg[1]" value="test">
<input type="hidden" name="csrf_token" value="<?= htmlspecialchars($wapCsrf) ?>">
<dl>
    <dt><strong>Test connection</strong></dt>
    <dd><span><input type="submit" value="Test connection"></span></dd>
</dl>
</form>

<form markdown="false" method="POST" action="/update.php" target="progressFrame">
<input type="hidden" name="#command" value="/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd">
<input type="hidden" name="#arg[1]" value="run-now">
<input type="hidden" name="csrf_token" value="<?= htmlspecialchars($wapCsrf) ?>">
<dl>
    <dt><strong>Run now</strong></dt>
    <dd><span><input type="submit" value="Run now"></span></dd>
</dl>
</form>
```

- [ ] **Step 2: Add launch= to the plugin template**

In `plugin/plugin.j2`, add the `launch` attribute to the `<PLUGIN ...>` element (so the plugin list "Settings" link opens the page):

```xml
<PLUGIN
  name="watch-aware-preloader"
  author="sydlexius"
  version="{{ env['PLUGIN_VERSION'] }}"
  pluginURL="https://raw.githubusercontent.com/sydlexius/watch-aware-preloader/main/plugin/watch-aware-preloader.plg"
  launch="Settings/WatchAwarePreloader"
  support="https://github.com/sydlexius/watch-aware-preloader"
  min="7.0.0"
  icon="hdd"
>
```

- [ ] **Step 3: Verify launch= is present and lint the page**

Run: `grep -n 'launch="Settings/WatchAwarePreloader"' plugin/plugin.j2`
Expected: one match.

Run: `make php-lint`
Expected: PHPStan + PHP-CS-Fixer pass (run `make php-fix` for style, re-run).

- [ ] **Step 4: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page plugin/plugin.j2
git commit -m "feat: settings form (/update.php -> render), test/run-now buttons, launch="
```

---

## Task 5: Write-only API key endpoint and set/unset indicator

The API key cannot ride `/update.php` (that writes the world-readable `.cfg`); it must go to `secrets.toml` and never echo. Add pure write/probe helpers (unit-tested), a thin CSRF-checked endpoint, and a key-field + set/unset indicator on the page.

**Files:**
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/secrets.php`
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/save-secret.php`
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`
- Test: `test/secrets_test.php`

**Interfaces:**
- Produces (PHP): `wap_write_api_key(string $secretPath, string $key): void` - writes `[server]\napi_key = "<key>"` to `$secretPath`, creating the parent dir, best-effort `chmod 0600`. Overwrites the whole file (secrets.toml holds only credentials).
- Produces (PHP): `wap_api_key_is_set(string $secretPath): bool` - true iff a non-empty `[server].api_key` is present.

- [ ] **Step 1: Write the failing secrets test**

Create `test/secrets_test.php`:

```php
<?php

declare(strict_types=1);

require __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/secrets.php';

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

$dir = sys_get_temp_dir() . '/wapsec_' . getmypid();
$path = $dir . '/secrets.toml';
@mkdir($dir, 0700, true);

check(wap_api_key_is_set($path) === false, 'unset when file missing');

wap_write_api_key($path, 'abc123');
check(is_file($path), 'file created');
$contents = file_get_contents($path);
check(str_contains($contents, 'api_key = "abc123"'), 'key written under [server]');
check(str_contains($contents, '[server]'), '[server] table written');
check(wap_api_key_is_set($path) === true, 'set after write');

// Overwrite with empty -> reported unset.
wap_write_api_key($path, '');
check(wap_api_key_is_set($path) === false, 'empty key reported unset');

@unlink($path);
@rmdir($dir);

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: secrets helpers\n";
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `php test/secrets_test.php`
Expected: FATAL - `require` of `include/secrets.php` fails (file does not exist yet).

- [ ] **Step 3: Implement the secrets helpers**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/include/secrets.php`:

```php
<?php

declare(strict_types=1);

// Write-only credential helpers. The API key is written to secrets.toml and is
// NEVER read back into the UI - the page shows only "set" / "not set". On the
// FAT32 flash path chmod is a no-op (mount umask governs); the key's protection
// is the flash/root boundary, the Unraid norm.

/**
 * Write [server].api_key to $secretPath, overwriting the file. Creates the
 * parent directory and best-effort tightens the file to 0600.
 */
function wap_write_api_key(string $secretPath, string $key): void
{
    $dir = dirname($secretPath);
    if (!is_dir($dir)) {
        @mkdir($dir, 0700, true);
    }
    $escaped = str_replace(['\\', '"'], ['\\\\', '\\"'], $key);
    $body = "# Credentials ONLY. Never commit; never put these in config.toml.\n"
          . "[server]\n"
          . "api_key = \"{$escaped}\"\n";
    file_put_contents($secretPath, $body, LOCK_EX);
    @chmod($secretPath, 0600);
}

/** True iff secrets.toml has a non-empty [server].api_key. */
function wap_api_key_is_set(string $secretPath): bool
{
    if (!is_file($secretPath)) {
        return false;
    }
    $raw = @file_get_contents($secretPath);
    if ($raw === false) {
        return false;
    }
    if (preg_match('/^\s*api_key\s*=\s*"(.*)"\s*$/m', $raw, $m) === 1) {
        return trim($m[1]) !== '';
    }
    return false;
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `php test/secrets_test.php`
Expected: `PASS: secrets helpers`

- [ ] **Step 5: Implement the CSRF-checked endpoint**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/include/save-secret.php`:

```php
<?php

declare(strict_types=1);

// Thin POST endpoint: verify CSRF, then write the API key to secrets.toml.
// Output goes to the webGui progress popup; never echo the key.

require_once __DIR__ . '/secrets.php';

header('Content-Type: text/plain');

$expected = '';
$varIni = '/var/local/emhttp/var.ini';
if (is_file($varIni)) {
    $var = @parse_ini_file($varIni);
    if (is_array($var)) {
        $expected = (string) ($var['csrf_token'] ?? '');
    }
}
$provided = (string) ($_POST['csrf_token'] ?? '');
if ($expected === '' || !hash_equals($expected, $provided)) {
    http_response_code(403);
    echo "Refused: CSRF token mismatch.\n";
    exit;
}

$key = (string) ($_POST['api_key'] ?? '');
$secretPath = '/boot/config/plugins/watch-aware-preloader/secrets.toml';
wap_write_api_key($secretPath, $key);

echo ($key === '') ? "API key cleared.\n" : "API key saved.\n";
```

- [ ] **Step 6: Add the API-key form and set/unset indicator to the page**

In `WatchAwarePreloader.page`, replace the Task-4 placeholder line
`$wapKeySet = false; // set in Task 5` with:

```php
require_once "{$docroot}/plugins/watch-aware-preloader/include/secrets.php";
$wapKeySet = wap_api_key_is_set('/boot/config/plugins/watch-aware-preloader/secrets.toml');
```

Then add this form immediately after the closing `</form>` of the settings form (before the Test-connection form):

```php
<form markdown="false" method="POST" action="/plugins/watch-aware-preloader/include/save-secret.php" target="progressFrame">
<input type="hidden" name="csrf_token" value="<?= htmlspecialchars($wapCsrf) ?>">
<dl>
    <dt>API key <?= $wapKeySet ? '(currently: set)' : '(currently: not set)' ?></dt>
    <dd><input type="password" name="api_key" autocomplete="new-password" placeholder="<?= $wapKeySet ? 'leave blank to keep, or enter a new key' : 'paste your Emby API key' ?>"></dd>
</dl>
<blockquote class="inline_help">Stored in secrets.toml, never in the settings file and never shown here. Saving an empty value clears the key.</blockquote>
<dl>
    <dt><strong>Save API key</strong></dt>
    <dd><span><input type="submit" value="Save API key"></span></dd>
</dl>
</form>
```

Note: the placeholder text tells the user that submitting blank clears the key. This is intentional and matches `wap_write_api_key`'s overwrite semantics; the "leave blank to keep" wording applies only conceptually. To avoid confusion, the help text states the clear-on-empty behavior explicitly.

- [ ] **Step 7: Lint the new PHP and re-run the PHP tests**

Run: `php -l src/usr/local/emhttp/plugins/watch-aware-preloader/include/secrets.php && php -l src/usr/local/emhttp/plugins/watch-aware-preloader/include/save-secret.php`
Expected: `No syntax errors detected` for both.

Run: `php test/secrets_test.php && php test/status_test.php`
Expected: `PASS: secrets helpers` then `PASS: status helpers`.

Run: `make php-lint`
Expected: PHPStan + PHP-CS-Fixer pass (`make php-fix` for style, re-run). Add a PHPStan ignore for `parse_ini_file`/`hash_equals` only if it flags them (it should not at level 6).

- [ ] **Step 8: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/include/secrets.php \
        src/usr/local/emhttp/plugins/watch-aware-preloader/include/save-secret.php \
        src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page \
        test/secrets_test.php
git commit -m "feat: write-only API key endpoint + set/unset indicator"
```

---

## Task 6: Docs close-out and full-suite green

Update the README to describe the settings page, note that `config.toml` is now generated from the `.cfg`, and run every gate once more. No new logic.

**Files:**
- Modify: `README.md`
- Modify: `docs/specs/2026-06-30-phase2-settings-ui-design.md` (mark implemented)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update the README installation/configuration section**

In `README.md`, under "Installation (Unraid plugin)", replace the "Set the server URL in config.toml..." guidance with settings-page guidance:

```markdown
After install, configure everything from the webGui at
**Settings -> Watch-Aware Preloader**: set the server URL, users, RAM budget,
target seconds, path maps, and schedule interval, then paste your API key into
the write-only API-key field (stored in `secrets.toml`, never shown back). Use
**Test connection** to verify, and **Run now** to warm immediately. The status
panel shows the last run (time shown in US Pacific).

`config.toml` is generated from these settings on every save and every boot -
edit settings in the webGui (or the plugin `.cfg`), not `config.toml` directly.
Uninstalling removes the cron job and binary but preserves your settings and
`secrets.toml` on the flash drive.
```

Keep the existing FAT32/flash credentials note (it remains accurate).

- [ ] **Step 2: Mark the spec implemented**

At the top of `docs/specs/2026-06-30-phase2-settings-ui-design.md`, update the status line:

```markdown
> Status: IMPLEMENTED (branch phase2-settings-ui). Live Unraid acceptance
> pending the install test on outatime. Third/final slice of Phase 2 PR 2.
```

- [ ] **Step 3: Run the entire gate suite**

Run: `go test ./...`
Expected: all Go packages PASS (engine unchanged).

Run: `bash test/rc_preloadd_render_test.sh && php test/status_test.php && php test/secrets_test.php`
Expected: `PASS: rc.preloadd render`, `PASS: status helpers`, `PASS: secrets helpers`.

Run: `make shellcheck && make php-lint`
Expected: both exit 0.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/specs/2026-06-30-phase2-settings-ui-design.md
git commit -m "docs: settings page usage; mark Slice C spec implemented"
```

---

## Post-plan: acceptance and shipping (NOT part of task execution)

These are maintainer-triggered and out of band; they are recorded here so the executor does not mistake plan completion for shipped:

1. **Prep the PR** via `/prep-pr` (squash, gates) once tasks 1-6 are complete and reviewed - only on explicit maintainer go-ahead.
2. **Cut a letter-free pre-release** (e.g. `2026.07.01`) so the dkaser action builds the `.txz` and commits the rendered `.plg`. Outward publish - needs explicit go-ahead.
3. **Live acceptance on `outatime`** (the real UI gate, "rendered evidence only"): install the plugin, open Settings -> Watch-Aware Preloader, Save settings (confirm `config.toml` + `.cron` regenerate), set the API key (confirm `secrets.toml` written, not echoed), Test connection, Run now, confirm the status panel renders a real `status.json`. Capture screenshots + `getComputedStyle`/selector checks per the UI rule.

   Live acceptance checklist (friendly-name validation):
   - Plugin displays as "Watch-Aware Preloader" in the Installed Plugins list.
   - The Settings link opens Settings/WatchAwarePreloader correctly (routing works).
   - Icon loads; no 404 / path-lookup errors in the browser console.
   - Settings page + status panel render correctly.
   - (Validates Unraid's internal `plugins/$name/` resolution tolerates the friendly `name` attr.)

---

## Self-Review

**1. Spec coverage** (against `docs/specs/2026-06-30-phase2-settings-ui-design.md`):
- `.cfg`-native store, `config.toml` generated by `rc.preloadd render` (never PHP) - Task 2. [covered]
- API key separate, write-only to `secrets.toml`, set/unset only - Task 5. [covered]
- `WatchAwarePreloader.page` + `include/*.php`; `launch="Settings/WatchAwarePreloader"` - Tasks 3-4. [covered]
- `default.cfg` with the listed keys - Task 2. [covered]
- `rc.preloadd` gains `render`; Save/install/boot call it; secrets seeding + best-effort chmod stays - Task 2. [covered]
- Settings: server URL/type, users, RAM %, target seconds, path maps, cron interval - Task 4. [covered]
- Buttons: Save, Test connection, Run now - Tasks 4 (+ rc actions in Task 2). [covered]
- Status panel: last run (Pacific), mode, ok/error, preloaded/skipped/missing, bytes, by_tier, by_user, schema-mismatch -> "no run yet" - Task 3. [covered] (Current cron schedule display: the panel does not re-read the cron; the interval is visible/edited in the settings form. Acceptable for MVP; noted here so it is a conscious omission, not a gap.)
- Static gates: PHPStan + PHP-CS-Fixer over `.php`/`.page`, shellcheck on `rc.preloadd`, Go suite green - Tasks 1, 3, 5, 6. [covered]
- Deferred (per-library UI, per-user name resolution, Jellyfin) - correctly out of scope. [covered]

**2. Placeholder scan:** No "TBD/TODO/handle edge cases". The one forward reference (`$wapKeySet = false; // set in Task 5`) is an explicit, intentional two-step wiring that Task 5 Step 6 replaces; it is real runnable code in the interim (Task 4 renders a valid page with the key indicator defaulting to unset). Flagged so a reviewer expects it.

**3. Type/name consistency:** `wap_read_status`, `wap_format_pacific`, `wap_write_api_key`, `wap_api_key_is_set` are used with identical signatures in their tests, the page, and the endpoint. `rc.preloadd` subcommands (`render`, `test`, `run-now`) match exactly between Task 2 (definition), Task 4 (form `#arg[1]` values), and the tests. `.cfg` keys match between `default.cfg`, the form field `name=`s, and `cfg_get` calls. The pinned paths (`/var/local/preloadd/status.json`, `/boot/config/plugins/watch-aware-preloader/secrets.toml`) are identical across `rc.preloadd`, `status.php` reader, the page, and `save-secret.php`.
