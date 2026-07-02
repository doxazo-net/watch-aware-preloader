# Phase 2 - Settings UI + Status Panel (Slice C) - Design

> Status: IMPLEMENTED (branch phase2-settings-ui). Live Unraid acceptance
> pending the install test on outatime. Third/final slice of Phase 2 PR 2. Tracks
> issue #2. Date: 2026-06-30.
> STACKS ON Slice B (#21, .plg packaging): the settings page lives in the same
> `src/usr/local/emhttp/plugins/watch-aware-preloader/` tree and wires `launch=`
> into the `.plg`. Implementation should branch on top of #21 (once merged), and
> its acceptance is a LIVE Unraid webGui test (per the "rendered evidence only"
> UI rule) - not statically verifiable.

## 1. Goal

Give the plugin a native Unraid settings page so the server URL, API key, users,
budget/buffer, path maps, and cron interval are all editable in the webGui, plus a
status panel that shows the last run (from `status.json`). This removes the last
"edit files by hand" step and completes Phase 2.

## 2. Architecture (data flow)

Native Unraid `.cfg` is the user-facing settings store; the Go engine's contract
(`config.toml`) is GENERATED from it, so PHP never parses/emits TOML.

```
  webGui settings form (.page, PHP)
        |  Save
        v
  /boot/config/plugins/watch-aware-preloader/watch-aware-preloader.cfg   (KEY="value", world-readable)
        |  (API key field handled separately, never in .cfg)
        v
  rc.preloadd render   (bash; reads .cfg -> writes config.toml + rewrites cron + update_cron)
        v
  config.toml (engine contract)   +   watch-aware-preloader.cron
        v
  preloadd (cron one-shot) -> status.json
        ^
  status panel (.page, PHP) reads status.json
```

Credentials path (separate, never in `.cfg`):

```
  API-key field (password input) --Save--> handler writes [server].api_key to
  secrets.toml (0600); never echoed back (UI shows only "key is set / not set").
```

### Why `.cfg`-native (not PHP-writes-TOML)
- Idiomatic Unraid: settings pages bind form fields to a `.cfg` via the standard
  `parse_plugin_cfg`/`$cfg` helpers; the framework handles read/write.
- PHP stays simple - no TOML parser/emitter in PHP (BurntSushi is Go-only).
- One TOML generator: `rc.preloadd` already seeds `config.toml`; it becomes the
  single renderer (`.cfg` -> `config.toml`), reused on Save, install, and boot.
- Revises Slice B's `rc.preloadd` seed into a `render` that derives the
  user-editable fields from the `.cfg` (with the same defaults when the `.cfg` is
  absent). This is a stacked change on #21.

## 3. Components (all under the plugin tree from Slice B)

- `WatchAwarePreloader.page` - the settings + status page (header block
  `Menu=/Title=/Icon=`, PHP body). `launch="Settings/WatchAwarePreloader"` added to
  the `.plg` (Slice B omitted it).
- `default.cfg` - default settings (`SERVER_URL`, `USERS`, `RAM_PERCENT`,
  `TARGET_SECONDS`, `PATH_MAP`, `CRON_INTERVAL`, ...). No API key key.
- `include/` PHP helpers: read `.cfg`, render the form, and the POST handlers
  (Save settings, Save API key, Test connection, Run now, Status render).
- `rc.preloadd` (modified): add `render` (write `config.toml` + `.cron` from the
  `.cfg`); Save calls it; install/boot call it. `secrets.toml` seeding/0600 stays.

## 4. Features (MVP; YAGNI)

- **Server:** base URL (text) + type (emby; jellyfin later). **API key** (password
  field -> `secrets.toml` 0600; write-only, shows set/unset).
- **Users:** comma-separated names to preload (empty = all).
- **Budget/buffer:** RAM percent, target seconds.
- **Path maps:** repeatable from/to rows.
- **Schedule:** cron interval (minutes) -> rewrites the `.cron` on save.
- **Buttons:** Save; **Test connection** (PHP curls `{url}/System/Info/Public`, and
  with the key `{url}/System/Info`, reports ok/fail - reads the key from
  `secrets.toml`, which the root webGui can read); **Run now** (`preloadd -once`).
- **Status panel:** reads `status.json` - last run (RFC3339 UTC -> displayed US
  Pacific, labeled), mode, ok/error, preloaded/skipped/missing, bytes warmed,
  per-tier counts, per-user counts (by `UserID`), and the current cron schedule.
  Missing/`schema_version`-mismatch file -> clear "no run yet" state, not a blank.
- **Deferred (not MVP):** per-library include/exclude UI (resume/next-up
  self-select; sensible defaults hold); per-user name resolution in the panel
  (show `UserID` for now - names would need an API call; enhancement); Jellyfin.

## 5. UI conventions

Match the native Unraid (Dynamix) settings-page markup so the page inherits the
webGui theme - this is NOT a bespoke-design surface, so the frontend-design skill
does not apply; follow Unraid's standard form/table conventions (as in the
reference plugin `dkaser/unraid-fileactivity`). Times shown US Pacific, labeled.
No secrets rendered in the page or logs.

## 6. Testing / acceptance

- **Static (CI-able):** PHPStan + PHP-CS-Fixer over the new `.php`/`.page`
  (`make php-lint`); `shellcheck` on the modified `rc.preloadd`; the Go engine is
  unchanged (suite stays green).
- **Live (hands-on, the real gate):** on `outatime` with the plugin installed -
  open the settings page, save settings (confirm `config.toml` + `.cron`
  regenerate correctly), set the API key (confirm `secrets.toml` 0600, not echoed),
  Test connection, Run now, and confirm the status panel renders a real
  `status.json`. Per the "rendered evidence only" rule, screenshots +
  getComputedStyle/selector checks on the live page are required to call the UI
  done - static file review is insufficient.

## 7. Sequencing / open items

- **Build stacks on #21** (Slice B). Do not start implementation until #21 is
  merged and its install test passes, so the PHP renders against a real installed
  plugin. Branch Slice C on top of the merged packaging.
- Modifying `rc.preloadd` (Slice B's file) is expected and part of this slice.
- Per-user name resolution and per-library UI are tracked as post-MVP
  enhancements.
- Jellyfin support is Phase 3 (#3); the type field is present but emby-only here.
- After Slice C, Phase 2 (#2) is complete and can be closed.
