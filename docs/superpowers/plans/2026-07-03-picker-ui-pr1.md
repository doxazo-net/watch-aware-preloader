# Picker UI PR1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the settings page's free-text `USERS` field with server-queried user + library checkbox pickers and a read-only auto-path-map table, populated from a root-written `pickers.json` cache.

**Architecture:** The `.page` renders unprivileged and cannot read the 0600-root API key, so it never queries the server directly. Instead `rc.preloadd` (root, via `/update.php`) writes a world-readable `pickers.json` on a successful connection test; the page reads that cache at render time (the same root-writes / page-reads boundary the `status.json` panel already uses). The Go engine gains id-or-name user matching so the new id-based picker and legacy name configs both resolve.

**Tech Stack:** Go 1.26 (stdlib), Bash (`rc.preloadd`), PHP (Unraid `.page` + includes), plain-PHP `test/*_test.php` harness, Bash render-contract harness.

## Global Constraints

- Go 1.26+, `net/http` stdlib, `log/slog`; single static binary, no CGO, no new deps.
- PHP PSR-12; lint clean under PHPStan + PHP-CS-Fixer (`make php-lint`).
- API key is a secret: never rendered in the page, never written to the `.cfg`, never echoed by a subcommand. `pickers.json` carries only user/library/path-map metadata, never the key.
- The `.page` must fail loud: never render an empty picker as if it meant "no users"; distinguish "not connected" from "server returned none."
- `pickers.json` is world-readable (0644) so the `nobody`-served page can read it; written atomically (temp + `mv`).
- Acceptance for the page surface is a live Unraid webGui test on `outatime` (rendered-evidence rule), not static markup review.
- Each PR <= ~1000 hand-written LOC.

---

## File structure

**Go**
- Modify `internal/app/pipeline.go` — `ResolveUserIDs` matches id or name.
- Modify `internal/app/pipeline_test.go` — id/name match table.

**Bash (`rc.preloadd`)**
- Modify `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd` — render `[libraries]`; add `write_pickers` + `write-pickers` dispatch; call it from `test_connection`.
- Modify `test/rc_preloadd_render_test.sh` — assert `[libraries]` rendered; assert `write-pickers` assembles the cache from stub subcommand output.

**PHP**
- Modify `src/.../include/paths.php` — `wap_default_pickers_path()`.
- Create `src/.../include/pickers.php` — read cache + freshness + accessors.
- Modify `src/.../include/settings.php` — `USERS[]`/`LIBRARIES[]` array→CSV normalization; add `LIBRARIES`.
- Modify `src/.../WatchAwarePreloader.page` — connect-gate, user/library checkbox pickers, read-only auto-map table, demote `PATH_MAPS`.
- Create `test/pickers_test.php` — cache read/freshness/accessor unit tests.
- Modify `test/settings_test.php` — array-normalization cases.

---

## Task 1: `ResolveUserIDs` matches id or name (Go engine)

**Files:**
- Modify: `internal/app/pipeline.go:37-54`
- Test: `internal/app/pipeline_test.go`

**Interfaces:**
- Consumes: `emby.User{ID, Name string}`.
- Produces: `ResolveUserIDs(users []emby.User, enabled []string) []string` — unchanged signature; an entry now matches a user by `u.ID` OR `u.Name`; empty `enabled` still returns all user IDs.

- [ ] **Step 1: Write the failing test**

Add to `internal/app/pipeline_test.go`:

```go
func TestResolveUserIDsMatchesIdOrName(t *testing.T) {
	users := []emby.User{
		{ID: "id-alice", Name: "Alice"},
		{ID: "id-bob", Name: "Bob"},
	}
	cases := []struct {
		name    string
		enabled []string
		want    []string
	}{
		{"by id", []string{"id-alice"}, []string{"id-alice"}},
		{"by name (legacy)", []string{"Bob"}, []string{"id-bob"}},
		{"mixed id and name", []string{"id-alice", "Bob"}, []string{"id-alice", "id-bob"}},
		{"empty means all", nil, []string{"id-alice", "id-bob"}},
		{"no match yields none", []string{"nobody"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveUserIDs(users, tc.enabled)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ResolveUserIDs(%v) = %v, want %v", tc.enabled, got, tc.want)
			}
		})
	}
}
```

Ensure `internal/app/pipeline_test.go` imports `"slices"` (add to the import block if absent).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestResolveUserIDsMatchesIdOrName -v`
Expected: FAIL on the "by id" case (`want [id-alice]`, got `[]`) — current code matches only `u.Name`.

- [ ] **Step 3: Write minimal implementation**

In `internal/app/pipeline.go`, change the match line inside `ResolveUserIDs`:

```go
	var ids []string
	for _, u := range users {
		if want[u.ID] || want[u.Name] {
			ids = append(ids, u.ID)
		}
	}
	return ids
```

Update the `ResolveUserIDs` doc comment to note it matches by id or name (id preferred for stability; names accepted for legacy configs).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestResolveUserIDsMatchesIdOrName -v`
Expected: PASS (all sub-cases).

- [ ] **Step 5: Full app tests + commit**

Run: `go test ./internal/app/...`
Expected: PASS.

```bash
git add internal/app/pipeline.go internal/app/pipeline_test.go
git commit -m "feat: ResolveUserIDs matches user id or name

The picker writes stable Emby ids; legacy configs hold names. Match an
enabled entry against both u.ID and u.Name so both resolve with no
migration. Ids are GUIDs and names are human strings, so no collision."
```

---

## Task 2: `rc render` emits `[libraries] enabled`

The library-scope engine (#43) reads `config.Libraries.Enabled`, but `render` never writes `[libraries]`, so the scope is always empty (all libraries). Add it from a new `LIBRARIES` cfg key, reusing `csv_to_toml_array`.

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd` (`render`)
- Test: `test/rc_preloadd_render_test.sh`

**Interfaces:**
- Consumes: `cfg_get`, `csv_to_toml_array` (existing).
- Produces: `config.toml` now contains a `[libraries]\nenabled = [...]` block sourced from the `LIBRARIES` cfg key (empty `[]` when unset).

- [ ] **Step 1: Write the failing test**

Add to `test/rc_preloadd_render_test.sh` (follow the file's existing render-and-grep pattern; write a `LIBRARIES` value into the test `.cfg`, run `rc.preloadd render`, assert the block). Example assertion block, matching the harness's existing style:

```bash
# LIBRARIES cfg -> [libraries] enabled TOML array
printf 'LIBRARIES="lib-1,lib-2"\n' >> "$CFG"
"$RC" render
grep -q '^\[libraries\]$' "$CONFIG" || fail "missing [libraries] section"
grep -q '^enabled = \["lib-1", "lib-2"\]$' "$CONFIG" || fail "libraries.enabled not rendered"
```

(Use the harness's actual `$RC`, `$CFG`, `$CONFIG`, and `fail` names — read the top of `rc_preloadd_render_test.sh` and reuse them verbatim.)

- [ ] **Step 2: Run test to verify it fails**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: FAIL at "missing [libraries] section" — `render` does not emit it yet.

- [ ] **Step 3: Write minimal implementation**

In `rc.preloadd` `render()`, read the new key alongside the others:

```bash
    users="$(cfg_get "$src" USERS "")"
    libraries="$(cfg_get "$src" LIBRARIES "")"
```

Add its TOML array next to `users_toml`:

```bash
    local users_toml libraries_toml path_map_toml
    users_toml="$(csv_to_toml_array "$users")"
    libraries_toml="$(csv_to_toml_array "$libraries")"
    path_map_toml="$(render_path_maps "$maps")"
```

And emit the block in the heredoc, immediately after the `[users]` block:

```bash
[users]
enabled = ${users_toml}

[libraries]
enabled = ${libraries_toml}

[preload]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: PASS.

- [ ] **Step 5: Confirm the config loader tolerates the block, then commit**

Run: `go test ./internal/config/...`
Expected: PASS (empty/absent `[libraries]` already defaults to all; a populated block round-trips).

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd test/rc_preloadd_render_test.sh
git commit -m "feat: render [libraries] enabled from LIBRARIES cfg

The #43 library-scope engine reads config.Libraries.Enabled but nothing
wrote it. Render it from a new LIBRARIES cfg key via csv_to_toml_array."
```

---

## Task 3: `rc.preloadd write-pickers` assembles the cache; `test` calls it

Add a `write_pickers` function that runs the three read-only subcommands and writes `pickers.json` atomically, world-readable. Expose it as a `write-pickers` dispatch (root-invocable, harness-testable) and call it best-effort from `test_connection` on a fully-successful connect.

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd`
- Test: `test/rc_preloadd_render_test.sh` (or a sibling harness; keep it in the same file for one `make` target)

**Interfaces:**
- Consumes: `$BIN` (the `preloadd` binary path), `$CONFIG`, `$SERVER_URL` via `cfg_get`.
- Produces: `pickers.json` at `${WAP_PICKERS_PATH:-/var/local/preloadd/pickers.json}`, shape:
  `{"generated_at","server_url","users":[...],"libraries":[...],"pathmaps":{...}}`. Written only when all three subcommands succeed; 0644.

- [ ] **Step 1: Write the failing test**

Add to `test/rc_preloadd_render_test.sh`. Stub `$BIN` with a script that emits fixture JSON per subcommand, point `WAP_PICKERS_PATH` at a temp file, run `rc.preloadd write-pickers`, and assert the assembled cache:

```bash
# write-pickers assembles pickers.json from the subcommand JSON
STUB_BIN="$TMP/preloadd"
cat > "$STUB_BIN" <<'STUB'
#!/bin/bash
case "$1" in
  list-users)      echo '[{"id":"id-a","name":"Alice"}]' ;;
  list-libraries)  echo '[{"id":"lib-1","name":"Movies","type":"movies"}]' ;;
  detect-pathmaps) echo '{"rules":[{"from":"/share/Movies","to":"/mnt/user/Movies","source":"docker"}],"unraid_unc_fallback":true}' ;;
  *) exit 2 ;;
esac
STUB
chmod +x "$STUB_BIN"
printf 'SERVER_URL="http://tower:8096"\n' >> "$CFG"
WAP_PICKERS_PATH="$TMP/pickers.json" WAP_BIN="$STUB_BIN" "$RC" write-pickers
grep -q '"server_url": *"http://tower:8096"' "$TMP/pickers.json" || fail "server_url not in cache"
grep -q '"id":"id-a"' "$TMP/pickers.json" || fail "users not merged"
grep -q '"id":"lib-1"' "$TMP/pickers.json" || fail "libraries not merged"
grep -q '"source":"docker"' "$TMP/pickers.json" || fail "pathmaps not merged"
# world-readable
perms="$(stat -c '%a' "$TMP/pickers.json" 2>/dev/null || stat -f '%Lp' "$TMP/pickers.json")"
[ "$perms" = "644" ] || fail "pickers.json not 0644 (got $perms)"
```

Reuse the harness's actual `$TMP`, `$RC`, `$CFG`, `fail` names.

- [ ] **Step 2: Run test to verify it fails**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: FAIL — `write-pickers` is not a recognized subcommand yet.

- [ ] **Step 3: Write minimal implementation**

In `rc.preloadd`, allow overriding the binary path for the harness and add the pickers path near the other path vars (top of file):

```bash
BIN="${WAP_BIN:-${RUNTIME}/preloadd}"
PICKERS="${WAP_PICKERS_PATH:-/var/local/preloadd/pickers.json}"
```

(Replace the existing `BIN="${RUNTIME}/preloadd"` line.)

Add the function (place it near `test_connection`):

```bash
# write_pickers: run the read-only subcommands and assemble pickers.json for the
# settings page. Root-only reads (secrets.toml via the binary). Writes atomically
# and world-readable; only overwrites the cache when ALL THREE subcommands
# succeed, so a transient failure keeps the last good cache (fail loud).
write_pickers() {
    local src="${CFG}" url users libs maps gen tmp
    [ -f "$src" ] || src="${DEFAULT_CFG}"
    url="$(cfg_get "$src" SERVER_URL "")"
    users="$("${BIN}" list-users -config "${CONFIG}" 2>/dev/null)" || { echo "write_pickers: list-users failed" >&2; return 1; }
    libs="$("${BIN}" list-libraries -config "${CONFIG}" 2>/dev/null)" || { echo "write_pickers: list-libraries failed" >&2; return 1; }
    maps="$("${BIN}" detect-pathmaps -config "${CONFIG}" 2>/dev/null)" || { echo "write_pickers: detect-pathmaps failed" >&2; return 1; }
    gen="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    mkdir -p "$(dirname "${PICKERS}")"
    tmp="$(mktemp "${PICKERS}.XXXXXX")"
    printf '{"generated_at":"%s","server_url":"%s","users":%s,"libraries":%s,"pathmaps":%s}\n' \
        "$gen" "$url" "$users" "$libs" "$maps" > "$tmp"
    chmod 0644 "$tmp"
    mv -f "$tmp" "${PICKERS}"
}
```

Note: `server_url` is a `toml_escape`-clean cfg value (already sanitized by presave), so it needs no further JSON escaping for the URLs this field holds; the three JSON payloads are emitted by the binary and embedded verbatim.

Add the dispatch case (alongside `test)` / `run-now)`):

```bash
    write-pickers) write_pickers ;;
```

And update the usage string to include `write-pickers`.

Finally, call it best-effort at the end of `test_connection`'s success path (after "API key accepted"), so a connect refreshes the cache without failing the test:

```bash
    if [ "$code" = "200" ]; then
        echo "OK: server reachable and API key accepted."
        if write_pickers; then
            echo "Pickers refreshed."
        else
            echo "Note: could not refresh user/library pickers (see log)."
        fi
    else
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: PASS.

- [ ] **Step 5: Shellcheck + commit**

Run: `shellcheck src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd`
Expected: no findings (matches the CI ShellCheck gate).

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd test/rc_preloadd_render_test.sh
git commit -m "feat: write pickers.json cache on successful connection test

Root shells out to list-users/list-libraries/detect-pathmaps and writes
a world-readable pickers.json the unprivileged page reads. Atomic write;
only refreshed when all three succeed."
```

---

## Task 4: PHP cache read + freshness helpers

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/paths.php`
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/pickers.php`
- Test: `test/pickers_test.php`

**Interfaces:**
- Produces:
  - `wap_default_pickers_path(): string` -> `/var/local/preloadd/pickers.json`
  - `wap_read_pickers(string $path): ?array` -> decoded cache or `null` (missing/invalid)
  - `wap_pickers_fresh(?array $pickers, string $serverUrl): bool` -> cache present and its `server_url` equals `$serverUrl` (trailing slash ignored)
  - `wap_picker_users(?array $p): array`, `wap_picker_libraries(?array $p): array`, `wap_picker_pathmaps(?array $p): array` -> lists with `[]` defaults

- [ ] **Step 1: Write the failing test**

Create `test/pickers_test.php`:

```php
<?php

declare(strict_types=1);

require __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/pickers.php';

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

$tmp = tempnam(sys_get_temp_dir(), 'wappick');
file_put_contents($tmp, json_encode([
    'generated_at' => '2026-07-03T00:00:00Z',
    'server_url'   => 'http://tower:8096',
    'users'        => [['id' => 'id-a', 'name' => 'Alice']],
    'libraries'    => [['id' => 'lib-1', 'name' => 'Movies']],
    'pathmaps'     => ['rules' => [['from' => '/share/Movies', 'to' => '/mnt/user/Movies', 'source' => 'docker']], 'unraid_unc_fallback' => true],
]));

$p = wap_read_pickers($tmp);
check($p !== null, 'valid cache decodes');
check(wap_pickers_fresh($p, 'http://tower:8096'), 'fresh when url matches');
check(wap_pickers_fresh($p, 'http://tower:8096/'), 'fresh ignores trailing slash');
check(!wap_pickers_fresh($p, 'http://other:8096'), 'stale when url differs');
check(count(wap_picker_users($p)) === 1, 'users accessor');
check(wap_picker_users($p)[0]['id'] === 'id-a', 'user id readable');
check(count(wap_picker_libraries($p)) === 1, 'libraries accessor');
check(wap_picker_pathmaps($p)['unraid_unc_fallback'] === true, 'pathmaps accessor');

check(wap_read_pickers($tmp . '.missing') === null, 'missing file -> null');
file_put_contents($tmp, 'not json');
check(wap_read_pickers($tmp) === null, 'invalid json -> null');
check(!wap_pickers_fresh(null, 'http://tower:8096'), 'null cache never fresh');
check(wap_picker_users(null) === [], 'null users -> empty');

unlink($tmp);
if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "pickers_test: OK\n";
```

- [ ] **Step 2: Run test to verify it fails**

Run: `php test/pickers_test.php`
Expected: FAIL — `pickers.php` does not exist / functions undefined.

- [ ] **Step 3: Write minimal implementation**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/include/pickers.php`:

```php
<?php

declare(strict_types=1);

// Read-only accessors for pickers.json, the root-written cache of server-queried
// users / libraries / auto-detected path maps. The settings page renders as the
// unprivileged webGui user and cannot query the server (it cannot read the
// 0600-root API key), so rc.preloadd (root) writes this cache on a successful
// connection test and the page reads it here. No secrets are ever in this file.

require_once __DIR__ . '/paths.php';

/** Decode pickers.json, or null when absent or invalid. */
function wap_read_pickers(string $path): ?array
{
    if (!is_file($path) || !is_readable($path)) {
        return null;
    }
    $raw = file_get_contents($path);
    if ($raw === false) {
        return null;
    }
    $data = json_decode($raw, true);

    return \is_array($data) ? $data : null;
}

/** A cache is fresh when present and its server_url matches (trailing slash ignored). */
function wap_pickers_fresh(?array $pickers, string $serverUrl): bool
{
    if ($pickers === null) {
        return false;
    }
    $cached = rtrim((string) ($pickers['server_url'] ?? ''), '/');

    return $cached !== '' && $cached === rtrim($serverUrl, '/');
}

/** @return list<array{id:string,name:string}> */
function wap_picker_users(?array $p): array
{
    return ($p !== null && \is_array($p['users'] ?? null)) ? $p['users'] : [];
}

/** @return list<array{id:string,name:string}> */
function wap_picker_libraries(?array $p): array
{
    return ($p !== null && \is_array($p['libraries'] ?? null)) ? $p['libraries'] : [];
}

/** @return array{rules?:list<array{from:string,to:string,source:string}>,unraid_unc_fallback?:bool} */
function wap_picker_pathmaps(?array $p): array
{
    return ($p !== null && \is_array($p['pathmaps'] ?? null)) ? $p['pathmaps'] : [];
}
```

Add to `include/paths.php` (next to `wap_default_status_path`):

```php
function wap_default_pickers_path(): string
{
    return '/var/local/preloadd/pickers.json';
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `php test/pickers_test.php`
Expected: `pickers_test: OK`.

- [ ] **Step 5: Lint + commit**

Run: `make php-lint`
Expected: clean.

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/include/pickers.php src/usr/local/emhttp/plugins/watch-aware-preloader/include/paths.php test/pickers_test.php
git commit -m "feat: PHP helpers to read the pickers.json cache"
```

---

## Task 5: `USERS[]` / `LIBRARIES[]` array normalization

The checkbox pickers post arrays; the `.cfg` stores a comma-separated scalar. Normalize array posts to sanitized CSV, and add `LIBRARIES`.

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/settings.php`
- Test: `test/settings_test.php`

**Interfaces:**
- Produces: `wap_cfg_csv_from_list(mixed $v): string` — array -> sanitized, comma-joined, empties dropped; scalar -> sanitized scalar (legacy). `wap_sanitize_settings_post` now also sets `$post['LIBRARIES']` and routes `USERS`/`LIBRARIES` through it.

- [ ] **Step 1: Write the failing test**

Add to `test/settings_test.php`:

```php
// --- wap_cfg_csv_from_list ---
check(wap_cfg_csv_from_list(['id-a', 'id-b']) === 'id-a,id-b', 'array joined to csv');
check(wap_cfg_csv_from_list(['id-a', '', ' id-b ']) === 'id-a,id-b', 'array trims and drops empties');
check(wap_cfg_csv_from_list('legacy,names') === 'legacy,names', 'scalar passes through sanitized');
check(wap_cfg_csv_from_list(["a\"b", 'c']) === 'ab,c', 'array elements sanitized');

// --- wap_sanitize_settings_post: USERS[]/LIBRARIES[] arrays ---
$post = ['USERS' => ['id-a', 'id-b'], 'LIBRARIES' => ['lib-1']];
wap_sanitize_settings_post($post);
check($post['USERS'] === 'id-a,id-b', 'USERS array normalized to csv');
check($post['LIBRARIES'] === 'lib-1', 'LIBRARIES array normalized to csv');

$post2 = [];
wap_sanitize_settings_post($post2);
check($post2['LIBRARIES'] === '', 'LIBRARIES defaults empty');
```

- [ ] **Step 2: Run test to verify it fails**

Run: `php test/settings_test.php`
Expected: FAIL — `wap_cfg_csv_from_list` undefined and `LIBRARIES` unset.

- [ ] **Step 3: Write minimal implementation**

Add the helper to `include/settings.php`:

```php
/**
 * Normalize a posted list-or-scalar field into a sanitized comma-separated cfg
 * value. Checkbox pickers post an array (USERS[]/LIBRARIES[]); a legacy free-text
 * field posts a scalar. Each element is run through wap_cfg_sanitize_str; empty
 * elements are dropped.
 *
 * @param mixed $v array<int,scalar>|scalar|null
 */
function wap_cfg_csv_from_list(mixed $v): string
{
    if (\is_array($v)) {
        $parts = [];
        foreach ($v as $item) {
            if (!\is_scalar($item)) {
                continue;
            }
            $s = wap_cfg_sanitize_str((string) $item);
            if ($s !== '') {
                $parts[] = $s;
            }
        }

        return implode(',', $parts);
    }

    return wap_cfg_sanitize_str((string) ($v ?? ''));
}
```

Route `USERS`/`LIBRARIES` through it in `wap_sanitize_settings_post` (replace the current `USERS` line):

```php
    $post['USERS']     = wap_cfg_csv_from_list($post['USERS'] ?? '');
    $post['LIBRARIES'] = wap_cfg_csv_from_list($post['LIBRARIES'] ?? '');
    $post['PATH_MAPS'] = wap_cfg_sanitize_str((string) ($post['PATH_MAPS'] ?? ''));
```

- [ ] **Step 4: Run test to verify it passes**

Run: `php test/settings_test.php`
Expected: OK (all checks pass).

- [ ] **Step 5: Lint + commit**

Run: `make php-lint`
Expected: clean.

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/include/settings.php test/settings_test.php
git commit -m "feat: normalize USERS[]/LIBRARIES[] picker arrays to cfg csv"
```

---

## Task 6: Render the pickers, connect-gate, and auto-map table on the page

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`

**Interfaces:**
- Consumes: `wap_read_pickers`, `wap_default_pickers_path`, `wap_pickers_fresh`, `wap_picker_users`, `wap_picker_libraries`, `wap_picker_pathmaps` (Task 4); posts `USERS[]`/`LIBRARIES[]` consumed by Task 5.
- Produces: the rendered settings surface (validated live, not by unit test).

- [ ] **Step 1: Load the cache in the page's PHP head**

After the existing `secrets.php` require + `$wapKeySet` line, add:

```php
require_once "{$docroot}/plugins/watch-aware-preloader/include/pickers.php";
$wapPickers    = wap_read_pickers(wap_default_pickers_path());
$wapServerUrl  = (string) ($wapCfg['SERVER_URL'] ?? 'http://localhost:8096');
$wapConnected  = wap_pickers_fresh($wapPickers, $wapServerUrl);
// Current selections (csv of ids, or legacy names) for checkbox state.
$wapSelUsers   = array_filter(array_map('trim', explode(',', (string) ($wapCfg['USERS'] ?? ''))), 'strlen');
$wapSelLibs    = array_filter(array_map('trim', explode(',', (string) ($wapCfg['LIBRARIES'] ?? ''))), 'strlen');
```

- [ ] **Step 2: Replace the free-text Users field with the picker + gate**

Replace the current Users `<dl>` block (the `wap_users` input and its help) with:

```php
<dl>
    <dt>Users to preload</dt>
    <dd>
    <?php if (!$wapConnected) { ?>
        <em>Connect to a server to choose users: save the Server URL and API key above, then click <strong>Test connection</strong>.</em>
    <?php } elseif (($wapUsers = wap_picker_users($wapPickers)) === []) { ?>
        <em>The server returned no users.</em>
    <?php } else {
        foreach ($wapUsers as $u) {
            $id = (string) ($u['id'] ?? '');
            $nm = (string) ($u['name'] ?? $id);
            $checked = (\in_array($id, $wapSelUsers, true) || \in_array($nm, $wapSelUsers, true)) ? ' checked' : '';
            ?>
        <label style="display:block"><input type="checkbox" name="USERS[]" value="<?= htmlspecialchars($id) ?>"<?= $checked ?>> <?= htmlspecialchars($nm) ?></label>
    <?php } } ?>
    </dd>
</dl>
<blockquote class="inline_help">Users whose watch-state is preloaded. Leave all unchecked to include every user.</blockquote>
```

- [ ] **Step 3: Add the Libraries picker block** (immediately after the Users block)

```php
<dl>
    <dt>Libraries to scope</dt>
    <dd>
    <?php if (!$wapConnected) { ?>
        <em>Connect to a server to choose libraries.</em>
    <?php } elseif (($wapLibs = wap_picker_libraries($wapPickers)) === []) { ?>
        <em>The server returned no libraries.</em>
    <?php } else {
        foreach ($wapLibs as $l) {
            $id = (string) ($l['id'] ?? '');
            $nm = (string) ($l['name'] ?? $id);
            $checked = \in_array($id, $wapSelLibs, true) ? ' checked' : '';
            ?>
        <label style="display:block"><input type="checkbox" name="LIBRARIES[]" value="<?= htmlspecialchars($id) ?>"<?= $checked ?>> <?= htmlspecialchars($nm) ?></label>
    <?php } } ?>
    </dd>
</dl>
<blockquote class="inline_help">Only items in the checked libraries are preloaded. Leave all unchecked to include every library.</blockquote>
```

- [ ] **Step 4: Replace the Path maps field with the read-only auto-map table + advanced override**

Replace the current `wap_path_maps` `<dl>` and its help with:

```php
<dl>
    <dt>Detected path maps</dt>
    <dd>
    <?php
    $wapMaps  = wap_picker_pathmaps($wapPickers);
    $wapRules = (\is_array($wapMaps['rules'] ?? null)) ? $wapMaps['rules'] : [];
    if (!$wapConnected) { ?>
        <em>Connect and Test connection to auto-detect Docker bind-mount path maps.</em>
    <?php } elseif ($wapRules === []) { ?>
        <em>No explicit path maps detected; the Unraid share-name convention will be used.</em>
    <?php } else { ?>
        <table class="tablesorter"><thead><tr><th>From</th><th>To</th><th>Source</th></tr></thead><tbody>
        <?php foreach ($wapRules as $r) { ?>
            <tr><td><?= htmlspecialchars((string) ($r['from'] ?? '')) ?></td><td><?= htmlspecialchars((string) ($r['to'] ?? '')) ?></td><td><?= htmlspecialchars((string) ($r['source'] ?? '')) ?></td></tr>
        <?php } ?>
        </tbody></table>
        <?php if (!empty($wapMaps['unraid_unc_fallback'])) { ?>
        <blockquote class="inline_help">Plus the Unraid UNC / share-name convention for any unmatched <code>\\host\Share</code> or <code>/share/Share</code> path.</blockquote>
        <?php } ?>
    <?php } ?>
    </dd>
</dl>

<dl>
    <dt><label for="wap_path_maps">Advanced: manual path-map override</label></dt>
    <dd><input type="text" id="wap_path_maps" name="PATH_MAPS" value="<?= htmlspecialchars((string) ($wapCfg['PATH_MAPS'] ?? '')) ?>"></dd>
</dl>
<blockquote class="inline_help">Optional extra <code>from=&gt;to</code> pairs separated by semicolons; these override auto-detected rules. Usually leave blank.</blockquote>
```

Note the `PATH_MAPS` default is now blank (auto-detect handles the common case), replacing the old `/share=>/mnt/user` default.

- [ ] **Step 5: PHP lint + render smoke**

Run: `make php-lint`
Expected: clean (PHPStan + PHP-CS-Fixer pass on the `.page`).

Run a syntax check on the page body:
`php -l src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`
Expected: `No syntax errors detected` (the `.page` front-matter is before `<?php`; if `php -l` chokes on the header, extract the PHP body per the repo's existing page-lint approach, otherwise rely on `make php-lint`).

- [ ] **Step 6: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page
git commit -m "feat: render user/library pickers, connect-gate, auto-map table

The Users free-text field becomes a checkbox picker (value=id, checked by
id or legacy name); a Libraries picker scopes the sweep; a read-only table
shows auto-detected path maps with the manual field demoted to an advanced
override. All populated from the pickers.json cache; a fail-loud connect
gate shows when no fresh cache exists."
```

---

## Task 7: Live acceptance on outatime (rendered evidence)

Not a unit test — the binding acceptance gate for the page surface. Perform after Tasks 1-6 land on the branch and a release/dev build is installed on `outatime` (or the `src/` tree is synced to the box's plugin dir and `rc.preloadd render` run).

- [ ] **Step 1:** In the webGui (Settings -> Watch-Aware Preloader), confirm that before any Test connection the Users/Libraries blocks show the connect-gate text (not empty checkboxes).
- [ ] **Step 2:** Enter the Server URL + API key, Apply, then click **Test connection**; confirm "Pickers refreshed." and that `/var/local/preloadd/pickers.json` exists and is 0644.
- [ ] **Step 3:** Reload the page; confirm the Users and Libraries checkbox lists populate from the real server, and the Detected path maps table shows the Docker rules (28 rules on this box per the last validation) with the UNC-fallback note.
- [ ] **Step 4:** Check some users + libraries, Apply; confirm `config.toml` shows `[users] enabled` and `[libraries] enabled` with the chosen ids.
- [ ] **Step 5:** Click **Run now**; confirm the status panel reports a sweep that warms > 0 bytes with the scoped selection.
- [ ] **Step 6:** Capture rendered evidence (screenshot of the populated page + the status panel) for sign-off.

---

## Self-review notes

- **Spec coverage:** populate mechanism (Tasks 3-4), connect-gate (Task 6 steps 2-4), users picker id+name (Tasks 1,6), libraries picker + `[libraries]` render (Tasks 2,5,6), auto-map table (Task 6 step 4), `ResolveUserIDs` match-either (Task 1), testing (each task) + live acceptance (Task 7). Tier dials are explicitly PR2 (not covered here, by design).
- **Type consistency:** `pickers.json` keys (`server_url`, `users`, `libraries`, `pathmaps.rules[].{from,to,source}`, `unraid_unc_fallback`) are identical across the Go subcommand output (verified), the Bash assembler (Task 3), the PHP accessors (Task 4), and the page (Task 6). `USERS[]`/`LIBRARIES[]` post names match between Task 5 (normalize) and Task 6 (render).
- **Deferred, tracked:** library `type` is carried by `list-libraries` but unused here (icons are a later follow-up).
