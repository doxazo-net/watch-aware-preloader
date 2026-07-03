# Picker UI PR2 (signal-tier dials) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add settings-page signal-tier dials so operators toggle each preload tier (resume / next-up / recently-added) and cap its per-user item count, writing `[tiers.*]` into the engine config.

**Architecture:** PR2 of the #32 picker UI (stacks on PR1, branch `feat/picker-ui-pr1`). The tier engine (`config.TiersConfig` + scorer wiring) shipped in #44; PR1 wired pickers and `[libraries]`. This slice adds the three cfg keys per tier, `rc render` emission of the `[tiers.*]` blocks, presave normalization, and the three UI rows. Same flat-cfg → `rc render` → TOML pattern as every other field.

**Tech Stack:** Bash (`rc.preloadd`), PHP (`.page` + `settings.php`), plain-PHP `test/*_test.php`, Bash render-contract harness.

## Global Constraints

- Bash; `rc.preloadd` + harness must pass CI ShellCheck.
- PHP PSR-12; must pass `make php-lint` (PHPStan + PHP-CS-Fixer, covering `.php` AND `.page`).
- Three tiers only: `resume`, `next_up`, `recently_added` (the engine's `TiersConfig`; no `binge_ahead`).
- `rc render` MUST emit all three `[tiers.*]` blocks whenever it renders. A missing tier block decodes to the zero value (`enabled=false`) and would silently disable that tier. Emitting the block also sets `tiersDefined=true`, so the config loader uses the emitted values verbatim (the "absent [tiers] => all enabled" default is intentionally bypassed).
- Behavior-preserving defaults: `enabled=true`, `max_items=0` (0 = no cap). This matches the current no-`[tiers]` behavior so an upgrade with default settings preloads exactly as before.
- `enabled` renders as a bare TOML boolean literal (`true`/`false`), never a quoted string. `max_items` renders as a validated bare integer.
- No secrets touched.

---

## File structure

**Bash**
- Modify `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd` — add `cfg_bool` helper; read six tier cfg keys; emit three `[tiers.*]` blocks.
- Modify `test/rc_preloadd_render_test.sh` — assert the three blocks + bool/int rendering + all-disabled honored.

**PHP**
- Modify `src/.../include/settings.php` — normalize the six tier fields in `wap_sanitize_settings_post`.
- Modify `src/.../WatchAwarePreloader.page` — three tier-dial rows after the Libraries picker.
- Modify `test/settings_test.php` — tier-field normalization cases.

**cfg keys** (flat, matching the existing style): `TIER_RESUME_ENABLED`, `TIER_RESUME_MAX`, `TIER_NEXTUP_ENABLED`, `TIER_NEXTUP_MAX`, `TIER_RECENT_ENABLED`, `TIER_RECENT_MAX`. `*_ENABLED` is `"1"`/`"0"`; `*_MAX` is a non-negative int (0 = no cap).

---

## Task 1: `rc render` emits `[tiers.*]`

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd` (add `cfg_bool`; extend `render`)
- Test: `test/rc_preloadd_render_test.sh`

**Interfaces:**
- Consumes: `cfg_get`, `num_or` (existing).
- Produces: `cfg_bool VALUE -> "true"|"false"` (true iff VALUE is `1`/`true`/`yes`/`on`, case-insensitive). `config.toml` gains three `[tiers.<name>]` blocks each with `enabled = <bool>` and `max_items = <int>`, sourced from the six `TIER_*` cfg keys, defaulting to `enabled=true`, `max_items=0`.

- [ ] **Step 1: Write the failing test**

Add to `test/rc_preloadd_render_test.sh` (reuse the file's actual `$RC`/`$CFG`/`$CONFIG`/`fail` names — read the top first). Assert both a populated and an all-disabled case:

```bash
# tiers: defaults (no TIER_* keys) -> all enabled, max_items 0
"$RC" render
grep -q '^\[tiers.resume\]$'         "$CONFIG" || fail "missing [tiers.resume]"
grep -q '^\[tiers.next_up\]$'        "$CONFIG" || fail "missing [tiers.next_up]"
grep -q '^\[tiers.recently_added\]$' "$CONFIG" || fail "missing [tiers.recently_added]"
grep -q '^enabled = true$'           "$CONFIG" || fail "tier enabled default not true"
grep -q '^max_items = 0$'            "$CONFIG" || fail "tier max_items default not 0"

# tiers: explicit values incl. a disabled tier and a cap
{ printf 'TIER_RESUME_ENABLED="1"\nTIER_RESUME_MAX="15"\n'; \
  printf 'TIER_NEXTUP_ENABLED="0"\nTIER_NEXTUP_MAX="0"\n'; \
  printf 'TIER_RECENT_ENABLED="1"\nTIER_RECENT_MAX="5"\n'; } >> "$CFG"
"$RC" render
# resume enabled=true max=15; next_up enabled=false; recently_added max=5
awk '/^\[tiers.next_up\]$/{f=1;next} f&&/^enabled = /{print;exit}' "$CONFIG" | grep -q 'enabled = false' || fail "next_up not disabled"
awk '/^\[tiers.resume\]$/{f=1;next} f&&/^max_items = /{print;exit}' "$CONFIG" | grep -q 'max_items = 15' || fail "resume max not 15"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: FAIL at "missing [tiers.resume]" — `render` does not emit tiers yet.

- [ ] **Step 3: Write minimal implementation**

Add the helper near `num_or` in `rc.preloadd`:

```bash
# cfg_bool VALUE -> "true" if VALUE is a truthy flag (1/true/yes/on, any case),
# else "false". Renders a bare TOML boolean for the [tiers.*] enabled fields.
cfg_bool() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
        1 | true | yes | on) printf 'true' ;;
        *) printf 'false' ;;
    esac
}
```

In `render()`, read the six keys alongside the others (defaults: enabled `1`, max `0`):

```bash
    local tr_en tr_max tn_en tn_max tc_en tc_max
    tr_en="$(cfg_bool "$(cfg_get "$src" TIER_RESUME_ENABLED 1)")"
    tr_max="$(num_or "$(cfg_get "$src" TIER_RESUME_MAX 0)" 0)"
    tn_en="$(cfg_bool "$(cfg_get "$src" TIER_NEXTUP_ENABLED 1)")"
    tn_max="$(num_or "$(cfg_get "$src" TIER_NEXTUP_MAX 0)" 0)"
    tc_en="$(cfg_bool "$(cfg_get "$src" TIER_RECENT_ENABLED 1)")"
    tc_max="$(num_or "$(cfg_get "$src" TIER_RECENT_MAX 0)" 0)"
```

Emit the blocks in the heredoc, immediately after the `[libraries]` block and before `[preload]`:

```bash
[libraries]
enabled = ${libraries_toml}

[tiers.resume]
enabled = ${tr_en}
max_items = ${tr_max}

[tiers.next_up]
enabled = ${tn_en}
max_items = ${tn_max}

[tiers.recently_added]
enabled = ${tc_en}
max_items = ${tc_max}

[preload]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash test/rc_preloadd_render_test.sh`
Expected: PASS.

- [ ] **Step 5: Config round-trip + shellcheck, then commit**

Run: `go test ./internal/config/...`
Expected: PASS (the emitted blocks decode into `TiersConfig`; an explicit `enabled=false` is honored because `tiersDefined` is true).

Run: `shellcheck src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd test/rc_preloadd_render_test.sh`
Expected: clean.

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd test/rc_preloadd_render_test.sh
git commit -m "feat: render [tiers.*] dials from TIER_* cfg keys

Emit all three tier blocks (resume/next_up/recently_added) with a bare
bool enabled + int max_items, defaulting to enabled=true/max_items=0 to
preserve the pre-dials behavior. cfg_bool maps a cfg flag to a TOML bool."
```

---

## Task 2: presave normalizes the tier fields

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/include/settings.php` (`wap_sanitize_settings_post`)
- Test: `test/settings_test.php`

**Interfaces:**
- Produces: `wap_sanitize_settings_post` additionally sets `TIER_RESUME_ENABLED`/`TIER_NEXTUP_ENABLED`/`TIER_RECENT_ENABLED` to `"1"` or `"0"` (a checkbox posts a value only when checked; absent => `"0"`) and `TIER_RESUME_MAX`/`TIER_NEXTUP_MAX`/`TIER_RECENT_MAX` to a clamped int string (0..10000, default 0). Defaults chosen so a first save with all boxes checked and blank caps reproduces the pre-dials all-enabled/no-cap behavior.

- [ ] **Step 1: Write the failing test**

Add to `test/settings_test.php`:

```php
// --- tier dials ---
$post = [
    'TIER_RESUME_ENABLED' => '1', 'TIER_RESUME_MAX' => '15',
    'TIER_NEXTUP_MAX' => '0',                 // NEXTUP_ENABLED absent (unchecked)
    'TIER_RECENT_ENABLED' => 'on', 'TIER_RECENT_MAX' => '5',
];
wap_sanitize_settings_post($post);
check($post['TIER_RESUME_ENABLED'] === '1', 'resume enabled normalized to 1');
check($post['TIER_RESUME_MAX'] === '15', 'resume max preserved');
check($post['TIER_NEXTUP_ENABLED'] === '0', 'absent tier checkbox => 0');
check($post['TIER_RECENT_ENABLED'] === '1', 'any present checkbox value => 1');
check($post['TIER_RECENT_MAX'] === '5', 'recent max preserved');

$empty = [];
wap_sanitize_settings_post($empty);
check($empty['TIER_RESUME_ENABLED'] === '0', 'all-absent => disabled flag 0');
check($empty['TIER_RESUME_MAX'] === '0', 'max default 0');
```

- [ ] **Step 2: Run test to verify it fails**

Run: `php test/settings_test.php`
Expected: FAIL — the `TIER_*` keys are not set by `wap_sanitize_settings_post` yet.

- [ ] **Step 3: Write minimal implementation**

Add a helper and the six normalizations to `include/settings.php`. Helper:

```php
/**
 * A checkbox posts a value only when checked, so presence (any non-empty scalar)
 * means enabled; absence means disabled. Returns "1" or "0".
 *
 * @param mixed $v
 */
function wap_cfg_checkbox(mixed $v): string
{
    return (\is_scalar($v) && (string) $v !== '' && (string) $v !== '0') ? '1' : '0';
}
```

In `wap_sanitize_settings_post`, before the closing brace:

```php
    foreach (['RESUME', 'NEXTUP', 'RECENT'] as $t) {
        $post["TIER_{$t}_ENABLED"] = wap_cfg_checkbox($post["TIER_{$t}_ENABLED"] ?? null);
        $post["TIER_{$t}_MAX"]     = (string) wap_cfg_clamp_int($post["TIER_{$t}_MAX"] ?? null, 0, 10000, 0);
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `php test/settings_test.php`
Expected: OK.

- [ ] **Step 5: Lint + commit**

Run: `make php-lint`
Expected: clean.

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/include/settings.php test/settings_test.php
git commit -m "feat: normalize tier-dial fields in presave

Each TIER_*_ENABLED becomes 1/0 (checkbox presence) and TIER_*_MAX a
clamped int (0..10000, 0 = no cap)."
```

---

## Task 3: render the three tier-dial rows

**Files:**
- Modify: `src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page`

**Interfaces:**
- Consumes: the `$wapCfg` array (cfg values, populated as in the existing fields). Posts `TIER_*_ENABLED` (checkbox) and `TIER_*_MAX` (number) consumed by Task 2.
- Produces: three tier rows rendered after the Libraries picker (validated live, not unit-tested).

- [ ] **Step 1: Add a small render helper in the page's PHP head**

After the existing `$wapRules` hoist line in the head PHP block, add:

```php
// Signal tiers for the dial rows: [cfg-prefix, label]. Default enabled (checked)
// and max 0 when the cfg key is absent, preserving pre-dials behavior.
$wapTiers = [
    ['RESUME', 'Resume points'],
    ['NEXTUP', 'Next-up episodes'],
    ['RECENT', 'Recently added'],
];
```

- [ ] **Step 2: Add the tier-dial block after the Libraries `<dl>`**

Insert immediately after the Libraries block's closing `</dl>` + its help `<blockquote>`:

```php
<dl>
    <dt>Signal tiers</dt>
    <dd>
    <?php foreach ($wapTiers as [$wapTk, $wapTlabel]) {
        $wapTEn  = ($wapCfg["TIER_{$wapTk}_ENABLED"] ?? '1') !== '0';
        $wapTMax = (int) ($wapCfg["TIER_{$wapTk}_MAX"] ?? 0);
        ?>
        <label style="display:block">
            <input type="checkbox" name="TIER_<?= $wapTk ?>_ENABLED" value="1"<?= $wapTEn ? ' checked' : '' ?>>
            <?= htmlspecialchars($wapTlabel) ?>
            &nbsp; max items <input type="number" name="TIER_<?= $wapTk ?>_MAX" min="0" max="10000" step="1" value="<?= $wapTMax ?>" style="width:6em">
        </label>
    <?php } ?>
    </dd>
</dl>
<blockquote class="inline_help">Which watch-signals contribute to the preload set, and a per-user item cap for each (0 = no limit). Priority order is fixed; unchecking a tier skips it entirely.</blockquote>
```

Note: `$wapTk` is a fixed literal from `$wapTiers` (never user input), so it needs no escaping in the attribute; the label is `htmlspecialchars`-escaped and the max value is cast to `int`.

- [ ] **Step 3: PHP lint + syntax check**

Run: `make php-lint`
Expected: clean (run `make php-fix` first if CS-Fixer reports a fixable nit, then re-lint).

Run: `php -l` on the front-matter-stripped page body (extract everything after the `---` line to a temp `.php`), expecting `No syntax errors detected`.

- [ ] **Step 4: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/WatchAwarePreloader.page
git commit -m "feat: render signal-tier dial rows on the settings page

Three rows (resume / next-up / recently-added), each an enable checkbox
plus a per-user max-items cap, posting TIER_*_ENABLED / TIER_*_MAX."
```

---

## Task 4: Live acceptance on outatime (rendered evidence)

Not a unit test — the binding acceptance gate. Perform after Tasks 1-3 land and the branch is installed/synced on `outatime`.

- [ ] **Step 1:** In the webGui, confirm three tier rows render (Resume points / Next-up episodes / Recently added), each with an enable checkbox (checked by default) and a max-items number field.
- [ ] **Step 2:** Uncheck one tier (e.g. Recently added), set another's max to a small number, Apply; confirm `config.toml` shows that tier `enabled = false` and the cap on the other.
- [ ] **Step 3:** Run now; confirm the disabled tier contributes 0 items in the status panel's by-tier breakdown and the capped tier is bounded.
- [ ] **Step 4:** Re-check all tiers with max 0, Apply, Run now; confirm behavior matches the pre-dials all-enabled baseline. Capture rendered evidence (screenshot of the rows + by-tier status).

---

## Self-review notes

- **Spec coverage:** the design-of-record's signal-tier dials (`enabled` + `max_items`, three tiers, fixed priority) are covered by config emission (Task 1), presave (Task 2), UI (Task 3), and live acceptance (Task 4). Scorer/config engine already shipped in #44.
- **Behavior preservation:** defaults (`enabled=true`, `max_items=0`) + emitting all three blocks + `tiersDefined=true` mean a default install preloads exactly as before the dials existed.
- **Type consistency:** the six `TIER_*` cfg keys are identical across `rc render` (Task 1 read + emit), presave (Task 2 normalize), and the page (Task 3 read + post). `enabled` is a TOML bool via `cfg_bool`; `max_items` a bare int via `num_or`.
