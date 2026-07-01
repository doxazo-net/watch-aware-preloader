# Phase 2 - .plg Packaging + Cron (Slice B) - Design

> Status: approved design of record. Second slice of Phase 2 PR 2. Tracks issue #2.
> Builds on the credentials secret store (PR #20, merged). Date: 2026-06-30.

## 1. Goal

Make Watch-Aware Preloader installable on Unraid by URL, with the engine wired to
run on a cron schedule - no file editing required to get it running. This slice
delivers the plugin tree, the CI release pipeline that packages it, and the
install/boot/uninstall lifecycle (binary, seeded config + secrets, cron). The PHP
settings page and status panel are Slice C.

## 2. Unraid packaging facts (grounded in plugin-dev docs)

- Unraid runs from RAM; plugins are **reinstalled on every boot** by extracting a
  cached `.txz` Slackware package from flash into
  `/usr/local/emhttp/plugins/watch-aware-preloader/`. Files cannot be placed
  directly - the binary must ride inside the `.txz`.
- Persistent state lives on flash at `/boot/config/plugins/watch-aware-preloader/`
  (config, secrets, plugin `.cfg`, the cached `.txz`).
- The `.plg` is an XML installer: metadata + a `<FILE Name>` that downloads the
  versioned `.txz` (SHA-256 verified) + inline `<FILE Run>` install/remove scripts.
- Sources: plugin-docs.mstrhakr.com (filesystem + lifecycle), DeepWiki unraid/api.

## 3. Decisions

- **Build pipeline:** adopt `dkaser/unraid-plugin-release-action`, pinned to a
  commit SHA (v1.5.0 = `946d02c81dd87c252e5a06d18f3e74b2ecc401dc`), per the repo's
  pin-actions-to-SHA rule. On a GitHub Release it builds the `.txz` from `src/`
  (running the Go build via its `go_dir`/`build_script` input), generates the
  `.plg` from a Jinja template, and attaches both to the release.
- **Binary delivery:** the static `preloadd` (built in CI) ships inside the `.txz`
  at `usr/local/emhttp/plugins/watch-aware-preloader/preloadd`; it is re-extracted
  to RAM on every boot.
- **Config location:** the engine's built-in `secret_path` default is already
  `/boot/config/plugins/watch-aware-preloader/secrets.toml`, so the seeded config
  needs no `secret_path` override - the defaults line up with the flash layout.
- **Cron:** default interval **every 15 minutes**. Use the canonical Unraid
  mechanism: write a persistent fragment to
  `/boot/config/plugins/watch-aware-preloader/watch-aware-preloader.cron` and run
  `/usr/local/sbin/update_cron` (dcron merges `/boot/config/plugins/*/*.cron` into
  root's crontab and re-applies at boot, so it survives reboots). Fragment lines
  carry no user field (run as root). Slice C's UI will make the interval editable
  (re-writing the `.cron` + `update_cron`). Exact mechanism confirmed on the
  `outatime` install test.
- **Secrets seeding:** seed `secrets.toml` (0600) with a placeholder if absent -
  the plugin OWNS `0600` enforcement here (the engine read-side does not).
- **Remove `config.example.toml`:** its documented content moves into the seeded
  default `config.toml` (commented template on the user's flash). Keep
  `secrets.example.toml` as the credential-format reference for headless/manual
  setup.
- **Uninstall:** remove the cron fragment (+ `update_cron`) and the RAM runtime
  dir; **preserve** `/boot/config/plugins/watch-aware-preloader/` (config +
  secrets) so a reinstall keeps settings. Documented explicitly in the remove
  script.

## 4. Repository layout (for the release action)

```
plugin/
  plugin.json                 # metadata: name, package_name, min Unraid, icon, support URL
  plugin.j2                   # Jinja template -> the .plg (metadata, <FILE> txz download+SHA, INSTALL/REMOVE)
src/
  usr/local/emhttp/plugins/watch-aware-preloader/
    preloadd                  # built by CI (go build) into the txz; NOT committed
    event/started             # boot hook: re-write cron fragment + update_cron from the flash .cfg
    (Slice C adds: WatchAwarePreloader.page, include/*.php, images/)
  install/
    slack-desc                # required 11-line package description
    doinst.sh                 # first-extract: create flash dir, seed config.toml + secrets.toml(0600), install cron
.github/workflows/
    release.yml               # on GitHub Release -> dkaser action (pinned SHA), least-privilege permissions
```

`config.example.toml` is deleted from the repo root in this slice.

## 5. Lifecycle behavior

- **Install / boot (doinst.sh + event/started):**
  1. `mkdir -p /boot/config/plugins/watch-aware-preloader` (flash).
  2. If `config.toml` absent, seed a commented default (type=emby, placeholder
     `url`, no `api_key`; relies on the engine's default `secret_path`).
  3. If `secrets.toml` absent, seed a `0600` placeholder (`[server].api_key = ""`);
     always `chmod 600` it.
  4. Write the cron fragment
     `/boot/config/plugins/watch-aware-preloader/watch-aware-preloader.cron`
     (interval default `*/15`, no user field) invoking
     `preloadd -once -config /boot/config/plugins/watch-aware-preloader/config.toml`;
     run `/usr/local/sbin/update_cron`.
- **Run:** cron invokes the one-shot each interval (the primary run model). The
  engine reads config from flash and the key from the secret store; writes
  `status.json` to its default path.
- **Uninstall (.plg REMOVE):** remove the `.cron` fragment + `update_cron`;
  `removepkg` + remove the RAM runtime dir; preserve the flash config/secrets
  (documented).

The install/remove/seed/cron logic lives in a shipped helper script
(`scripts/rc.preloadd {install|remove|seed|update}`) that the `.plg`'s INLINE
install/remove blocks call, so the `.plg` stays thin and the logic is
`shellcheck`-testable. Unraid re-runs the `.plg` install at each boot, which
re-seeds (idempotent) and re-applies cron.

## 6. Testing

- **CI-buildable / lint:** `plugin.json`/`plugin.j2` well-formed; `.plg` XML valid;
  shell scripts pass `shellcheck` (add to CI); the release workflow is
  actionlint-clean and least-privilege.
- **Go:** unchanged; existing suite stays green (no engine code changes expected in
  this slice; if any wiring changes, TDD as usual).
- **Manual install test (on `outatime`, hands-on):** cut a pre-release, install the
  `.plg` by URL, confirm binary extracted, config + `secrets.toml`(0600) seeded,
  cron fragment present, a cron run produces `status.json`; then uninstall and
  confirm cron removed + flash config preserved. This is the real acceptance gate;
  it is out-of-band (not a Go unit test).
- No third-party CI action runs untrusted at PR time beyond the release event; it
  is pinned to a SHA.

## 7. Open items / forward references

- **Requires cutting a release** to produce the installable artifact: after this
  slice merges, tag a version (v0.x, likely a pre-release) so the action builds the
  `.txz` + `.plg`. The install-by-URL target is the `.plg` on that release.
- Exact `plugin.j2` fields and the dkaser action input names are finalized against
  the action's current docs during implementation (pinned SHA v1.5.0).
- Slice C adds the PHP `.page` + status panel under the same
  `src/.../watch-aware-preloader/` tree and makes the cron interval + credentials
  editable in the UI.
- Community Applications store listing remains a Phase 4 step (separate submission;
  see the Phase 2 settings/packaging design's distribution section).
- `outatime` currently runs a manual `/root/preloadd/` deploy; installing the real
  plugin there later will supersede it (the plugin uses the `/boot` flash paths).
