# Phase 2 .plg Packaging + Cron (Slice B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Watch-Aware Preloader an install-by-URL Unraid plugin that ships the `preloadd` binary in a `.txz`, seeds config + a 0600 secrets file on flash, and runs the engine on a cron schedule.

**Architecture:** A `plugin/` + `src/` tree consumed by the `dkaser/unraid-plugin-release-action` (pinned SHA), which on a GitHub Release runs a Go build, packages `src/` into a `.txz`, templates the `.plg`, and attaches both to the release. Runtime lifecycle (seed config/secrets, install/remove cron) lives in a shipped `rc.preloadd` helper the `.plg` calls.

**Tech Stack:** Unraid plugin (`.plg` XML + Slackware `.txz`), bash, GitHub Actions, Go (build only). Validation: `shellcheck`, `actionlint`, `xmllint`, `jq`.

## Global Constraints

- Pin GitHub Actions to commit SHAs (with `# vX`); job-level least-privilege `permissions:`.
- Plugin `name` = `watch-aware-preloader`; flash dir `/boot/config/plugins/watch-aware-preloader/`; runtime dir `/usr/local/emhttp/plugins/watch-aware-preloader/`.
- Secrets file `secrets.toml` MUST be mode `0600`; the plugin owns this (engine read-side does not enforce it).
- Credentials never in `config.toml`; the seeded `config.toml` contains no `api_key`.
- Cron: persistent fragment `/boot/config/plugins/watch-aware-preloader/watch-aware-preloader.cron` + `/usr/local/sbin/update_cron`; lines carry no user field (run as root); default interval `*/15 * * * *`.
- Uninstall PRESERVES the flash config/secrets dir; removes cron + runtime.
- Release tag/version must be letter-free (Slackware limitation), e.g. `2026.07.01` or `0.1.0`.
- Commit messages end with: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- The built binary `src/usr/local/emhttp/plugins/watch-aware-preloader/preloadd` is CI-built, never committed (gitignored).

---

### Task 1: Plugin metadata, package description, gitignore

**Files:**
- Create: `plugin/plugin.json`
- Create: `src/install/slack-desc`
- Modify: `.gitignore`
- Delete: `plugin/.gitkeep`

**Interfaces:**
- Consumes: nothing.
- Produces: `plugin/plugin.json` with `name`/`package_name` = `watch-aware-preloader` (read by the release action + `.j2`); the Slackware `slack-desc`.

- [ ] **Step 1: Create `plugin/plugin.json`**

```json
{
  "name": "watch-aware-preloader",
  "package_name": "watch-aware-preloader"
}
```

- [ ] **Step 2: Create `src/install/slack-desc`**

Slackware package description: exactly 11 lines, each prefixed `watch-aware-preloader:`; line 1 is the "handy ruler". Keep every line <= 80 chars after the prefix.

```
      |-----handy-ruler------------------------------------------------------|
watch-aware-preloader: watch-aware-preloader
watch-aware-preloader:
watch-aware-preloader: Warms the Linux page cache with the media each household
watch-aware-preloader: user is most likely to play next, derived from the media
watch-aware-preloader: server's own watch state (Emby/Jellyfin), so playback
watch-aware-preloader: starts instantly instead of waiting for a disk spin-up.
watch-aware-preloader:
watch-aware-preloader: https://github.com/sydlexius/watch-aware-preloader
watch-aware-preloader:
watch-aware-preloader:
```

- [ ] **Step 3: Ignore the CI-built binary + remove the placeholder**

Append to `.gitignore`:

```
# Plugin binary is built in CI into the src/ tree, never committed
src/usr/local/emhttp/plugins/watch-aware-preloader/preloadd
```

Then remove the old placeholder:

```bash
git rm plugin/.gitkeep
```

- [ ] **Step 4: Validate**

Run: `jq . plugin/plugin.json >/dev/null && awk 'END{print NR" lines"}' src/install/slack-desc`
Expected: JSON parses (no error); `slack-desc` reports `11 lines`.

- [ ] **Step 5: Commit**

```bash
git add plugin/plugin.json src/install/slack-desc .gitignore
git commit -m "feat(plugin): add plugin metadata, slack-desc, ignore built binary

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `rc.preloadd` lifecycle helper (seed + cron)

**Files:**
- Create: `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd`

**Interfaces:**
- Consumes: nothing (invoked by the `.plg` in Task 3 as `rc.preloadd {install|remove|seed|update}`).
- Produces: an executable helper. `install` = seed + write cron + `update_cron`; `remove` = delete cron + `update_cron` (preserves flash config/secrets); `seed` = create flash dir + `config.toml` (if absent) + `secrets.toml` (0600); `update` = rewrite cron + `update_cron`.

- [ ] **Step 1: Write the script**

Create `src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd`:

```bash
#!/bin/bash
# rc.preloadd - install/remove lifecycle for the watch-aware-preloader plugin.
# Called by the .plg INLINE install/remove blocks. Idempotent: the .plg install
# re-runs at each Unraid boot, so seeding and cron install must be safe to repeat.
set -euo pipefail

PLUGIN="watch-aware-preloader"
FLASH="/boot/config/plugins/${PLUGIN}"
RUNTIME="/usr/local/emhttp/plugins/${PLUGIN}"
CONFIG="${FLASH}/config.toml"
SECRETS="${FLASH}/secrets.toml"
CRON="${FLASH}/${PLUGIN}.cron"
BIN="${RUNTIME}/preloadd"
INTERVAL="*/15 * * * *"   # Slice C's UI will make this editable

seed() {
    mkdir -p "${FLASH}"
    if [ ! -f "${CONFIG}" ]; then
        cat > "${CONFIG}" <<'EOF'
# Watch-Aware Preloader configuration (seeded by the plugin).
# The API key is NOT here - it lives in secrets.toml (same folder, 0600) or the
# EMBY_API_KEY env var. A stray api_key in this file is a hard error at startup.
# The engine's default secret_path already points at this folder's secrets.toml.

[server]
type = "emby"
url = "http://localhost:8096"   # set to your Emby/Jellyfin base URL

[users]
# User names to preload for. Leave empty to include all users.
enabled = []

[preload]
ram_percent = 50      # share of available RAM used as the preload budget
target_seconds = 20   # seconds of playback to keep warm (covers disk spin-up)
min_head_mb = 8
max_head_mb = 250
tail_mb = 1

# Map media-server paths to host paths. Omit if the server reports host paths.
[[path_map]]
from = "/share"
to = "/mnt/user"

[schedule]
# Only used in --daemon mode; the cron one-shot uses the plugin's cron interval.
sweep_seconds = 60
session_poll_seconds = 5
EOF
    fi
    if [ ! -f "${SECRETS}" ]; then
        cat > "${SECRETS}" <<'EOF'
# Credentials ONLY. Never commit; never put these in config.toml.
[server]
api_key = ""
EOF
    fi
    chmod 600 "${SECRETS}"
}

install_cron() {
    mkdir -p "${FLASH}"
    cat > "${CRON}" <<EOF
# Generated by the watch-aware-preloader plugin
${INTERVAL} ${BIN} -once -config ${CONFIG} >/dev/null 2>&1
EOF
    /usr/local/sbin/update_cron
}

remove_cron() {
    rm -f "${CRON}"
    /usr/local/sbin/update_cron
}

case "${1:-}" in
    install) seed; install_cron ;;
    update)  install_cron ;;
    seed)    seed ;;
    remove)  remove_cron ;;
    *) echo "usage: rc.preloadd {install|remove|seed|update}" >&2; exit 1 ;;
esac
```

- [ ] **Step 2: Make it executable + lint**

Run:
```bash
chmod +x src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd
shellcheck src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd
```
Expected: `shellcheck` exits 0 with no findings.

- [ ] **Step 3: Smoke-test seed locally (redirected paths)**

Because the real paths are Unraid-only, verify the seeding logic with a temp root by sourcing the functions is out of scope; instead confirm syntax and a dry parse:
```bash
bash -n src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd && echo "parse ok"
```
Expected: `parse ok`. (Full behavior is validated on the `outatime` install test.)

- [ ] **Step 4: Commit**

```bash
git add src/usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd
git commit -m "feat(plugin): rc.preloadd - seed config/secrets(0600) + cron lifecycle

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `.plg` Jinja template

**Files:**
- Create: `plugin/watch-aware-preloader.j2`

**Interfaces:**
- Consumes: `rc.preloadd` (Task 2); the `.txz` produced by CI (Task 4); env vars `PLUGIN_VERSION`, `PLUGIN_CHECKSUM`, `PLUGIN_CHANGELOG` set by the release action.
- Produces: the templated `plugin/watch-aware-preloader.plg` (generated by CI, install-by-URL entry point).

- [ ] **Step 1: Write the template**

Create `plugin/watch-aware-preloader.j2` (mirrors the dkaser/unraid-fileactivity pattern; `launch` is intentionally omitted until Slice C adds the settings page):

```xml
<?xml version='1.0' standalone='yes'?>
<!DOCTYPE PLUGIN>

<PLUGIN
  name="watch-aware-preloader"
  author="sydlexius"
  version="{{ env['PLUGIN_VERSION'] }}"
  pluginURL="https://raw.githubusercontent.com/sydlexius/watch-aware-preloader/main/plugin/watch-aware-preloader.plg"
  support="https://github.com/sydlexius/watch-aware-preloader"
  min="7.0.0"
  icon="hdd"
>

<CHANGES>
<![CDATA[
###{{ env['PLUGIN_VERSION'] }}###
{{ env['PLUGIN_CHANGELOG'] }}

For older releases, see https://github.com/sydlexius/watch-aware-preloader/releases
]]>
</CHANGES>

<FILE Name="/boot/config/plugins/watch-aware-preloader/watch-aware-preloader-{{ env['PLUGIN_VERSION'] }}-noarch-1.txz">
<URL>https://github.com/sydlexius/watch-aware-preloader/releases/download/{{ env['PLUGIN_VERSION'] }}/watch-aware-preloader-{{ env['PLUGIN_VERSION'] }}-noarch-1.txz</URL>
<SHA256>{{ env['PLUGIN_CHECKSUM'] }}</SHA256>
</FILE>

<!-- install script (runs on install AND on each Unraid boot) -->
<FILE Run="/bin/bash">
<INLINE>
<![CDATA[
upgradepkg --install-new /boot/config/plugins/watch-aware-preloader/watch-aware-preloader-{{ env['PLUGIN_VERSION'] }}-noarch-1.txz

# remove superseded packages
rm -f $(ls /boot/config/plugins/watch-aware-preloader/watch-aware-preloader-*.txz 2>/dev/null | grep -v '{{ env['PLUGIN_VERSION'] }}')

bash /usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd install

echo ""
echo "----------------------------------------------------"
echo " watch-aware-preloader has been installed."
echo " Version: {{ env['PLUGIN_VERSION'] }}"
echo " Set your server URL + API key, then it runs every 15 min."
echo "----------------------------------------------------"
echo ""
]]>
</INLINE>
</FILE>

<!-- remove script: stop cron + runtime; PRESERVE flash config/secrets -->
<FILE Run="/bin/bash" Method="remove">
<INLINE>
<![CDATA[
bash /usr/local/emhttp/plugins/watch-aware-preloader/scripts/rc.preloadd remove
removepkg watch-aware-preloader
rm -rf /usr/local/emhttp/plugins/watch-aware-preloader
# NOTE: /boot/config/plugins/watch-aware-preloader (config.toml, secrets.toml) is
# intentionally preserved so a reinstall keeps settings. Delete it by hand to purge.
]]>
</INLINE>
</FILE>

</PLUGIN>
```

- [ ] **Step 2: Validate the XML structure (with the Jinja vars stubbed)**

Jinja `{{ ... }}` is not valid XML on its own, so substitute placeholders, then `xmllint`:
```bash
sed -E "s/\{\{[^}]*\}\}/X/g" plugin/watch-aware-preloader.j2 | xmllint --noout - && echo "xml ok"
```
Expected: `xml ok` (well-formed once vars are stubbed).

- [ ] **Step 3: Commit**

```bash
git add plugin/watch-aware-preloader.j2
git commit -m "feat(plugin): .plg template (txz download, install/remove via rc.preloadd)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Go build script + release workflow

**Files:**
- Create: `plugin/build.sh`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `plugin.json` + `.j2` (Tasks 1, 3); the dkaser action.
- Produces: CI that, on a GitHub Release, builds the binary into `src/`, packages the `.txz`, templates the `.plg`, and attaches artifacts.

- [ ] **Step 1: Write `plugin/build.sh`** (the action's `build_script`, prepares `src/`)

```bash
#!/bin/bash
# Build the static linux/amd64 preloadd binary into the plugin src/ tree so the
# release action packages it into the .txz. Run by the release workflow.
set -euo pipefail

version="${PLUGIN_VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
out="src/usr/local/emhttp/plugins/watch-aware-preloader/preloadd"
mkdir -p "$(dirname "${out}")"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "${out}" ./cmd/preloadd
echo "built ${out} (version ${version})"
```

- [ ] **Step 2: Write `.github/workflows/release.yml`**

Pin the action to the v1.5.0 commit SHA. Job-level least-privilege `permissions` (`contents: write` is required so the action can attach assets and commit the generated `.plg`).

```yaml
name: Release Unraid Plugin

on:
  release:
    types:
      - prereleased
      - released

permissions:
  contents: write

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: dkaser/unraid-plugin-release-action@946d02c81dd87c252e5a06d18f3e74b2ecc401dc # v1.5.0
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          go_dir: .
          build_script: ./plugin/build.sh
          build_prereleases: "true"
          changelog_releases: "5"
```

- [ ] **Step 3: Make build.sh executable + lint both files**

Run:
```bash
chmod +x plugin/build.sh
shellcheck plugin/build.sh
actionlint .github/workflows/release.yml
```
Expected: both exit 0 with no findings. (If `actionlint` flags the unverified third-party action's `uses` pin, that is acceptable - it is intentionally SHA-pinned.)

- [ ] **Step 4: Confirm the build script actually builds locally**

Run:
```bash
PLUGIN_VERSION=0.0.0-test ./plugin/build.sh && file src/usr/local/emhttp/plugins/watch-aware-preloader/preloadd
```
Expected: prints "built ..."; `file` reports an `ELF 64-bit ... x86-64` executable. Then remove the local artifact (it is gitignored, but keep the tree clean): `rm -f src/usr/local/emhttp/plugins/watch-aware-preloader/preloadd`.

- [ ] **Step 5: Commit**

```bash
git add plugin/build.sh .github/workflows/release.yml
git commit -m "ci(plugin): Go build script + release workflow (dkaser action, pinned)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Remove `config.example.toml`; document installation

**Files:**
- Delete: `config.example.toml`
- Modify: `README.md`

**Interfaces:**
- Consumes: the seeded config template now lives in `rc.preloadd` (Task 2); the `.plg` install path (Task 3).
- Produces: docs only.

- [ ] **Step 1: Remove the example config**

```bash
git rm config.example.toml
```

Rationale (per the Phase 2 decision): the plugin seeds a commented `config.toml` on flash, so a standalone example in the repo root is redundant and would contradict the UI-first config surface. `secrets.example.toml` is kept as the credential-format reference.

- [ ] **Step 2: Add an Installation section to `README.md`**

Insert a `## Installation (Unraid plugin)` section before the `## License` heading:

```markdown
## Installation (Unraid plugin)

Install by URL (Plugins -> Install Plugin), pointing at the `.plg` from a release:

```
https://github.com/sydlexius/watch-aware-preloader/releases/latest/download/watch-aware-preloader.plg
```

On install the plugin:
- extracts the `preloadd` binary to `/usr/local/emhttp/plugins/watch-aware-preloader/`
- seeds `/boot/config/plugins/watch-aware-preloader/config.toml` (edit the server URL)
  and `secrets.toml` (mode `0600`; put your API key under `[server].api_key`)
- installs a cron job running `preloadd -once` every 15 minutes

Set the server URL in `config.toml` and the API key in `secrets.toml`, and it
starts warming the cache on the next cron tick. Uninstalling removes the cron job
and binary but preserves your `config.toml`/`secrets.toml` on the flash drive.

Releases are tagged with letter-free versions (Slackware requirement), e.g.
`2026.07.01`.
```

- [ ] **Step 3: Verify + format**

Run: `test ! -e config.example.toml && echo "removed" && make fmt`
Expected: prints `removed`; `make fmt` succeeds (no Go changes).

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "docs: remove config.example.toml; document plugin installation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/specs/2026-06-30-phase2-plg-packaging-design.md`):
- S3 build pipeline (dkaser action, pinned SHA, go build) -> Task 4. Binary delivery in `.txz` -> Task 4 build.sh + Task 3 `<FILE>`. Config location/defaults -> Task 2 seed. Cron (persistent `.cron` + `update_cron`, `*/15`) -> Task 2. Secrets 0600 seeding -> Task 2. Remove `config.example.toml` -> Task 5. Uninstall preserves flash -> Task 3 remove INLINE + Task 2 `remove`.
- S4 repo layout -> Tasks 1-4. S5 lifecycle -> Tasks 2-3. S6 testing (shellcheck/actionlint/xmllint/jq; manual install acceptance) -> per-task validate steps + noted as out-of-band.
- `event/started` from the earlier draft is intentionally dropped: Unraid re-runs the `.plg` install at boot, which re-invokes `rc.preloadd install` (idempotent), so cron re-applies without a separate hook. Documented in the spec.

**2. Placeholder scan:** No TBD/TODO. Every file has complete content. The `support` URL uses the GitHub repo (no forum thread yet - acceptable, updatable in Phase 4). `xmllint` step explicitly stubs the Jinja vars rather than leaving them ambiguous.

**3. Consistency:** `name`/`package_name` = `watch-aware-preloader` across `plugin.json`, `.j2`, paths, txz name. `rc.preloadd` action verbs (`install`/`remove`/`seed`/`update`) match the `.plg` INLINE calls (`install`, `remove`) and the spec. Cron path `${FLASH}/${PLUGIN}.cron` consistent with `update_cron`. `build_script` path `./plugin/build.sh` matches the created file; `go_dir: .` matches the repo-root `go.mod`. The txz filename in the `.j2` `<FILE Name>`/`<URL>` matches the action's `<package_name>-<version>-noarch-1.txz` convention.
