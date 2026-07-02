# Settings UX: user/library pickers, signal-tier dials, and auto path-mapping

Design spec. Date: 2026-07-02. Status: approved for planning.

Covers issues **#31** (docker-inspect auto path maps) and **#32** (server-queried
user + library pickers), plus a new operator control (signal-tier dials) and the
fix for the current "sweep warms 0 bytes" bug. The cache-budget meter (**#39**) is
explicitly out of scope here and rides on top of this work later.

## 1. Motivation

Once a server + API key are configured, the operator should **pick** what to
preload rather than type free text, tune which watch-signals contribute, and have
path mapping happen automatically. Today:

- Users are a comma-separated free-text field (`users.enabled`, matched by name -
  brittle; a rename silently drops a user).
- There is no library selection at all.
- The signal tiers (resume / next-up / recently-added / binge-ahead) are hardcoded
  priorities with no operator control.
- Path maps are manual and, on a real Unraid + Emby-in-Docker setup, wrong -
  causing every item to fail mapping and the sweep to warm **0 bytes**.

### 1.1 Live ground truth (outatime, Emby in Docker, verified 2026-07-02)

- Emby runs as a Docker container on the Unraid host.
- `docker inspect` reports bind mounts of the form `/mnt/user/<Share>` (host) ->
  `/share/<Share>` (container), one per library share.
- Emby `/Library/VirtualFolders` reports each library's **locations** as container
  paths (`/share/<Share>`).
- Emby **item** paths (what the engine actually receives from Resume/NextUp/Latest)
  are reported as **UNC** (`\\<host>\<Share>\...`) - the items were scanned under an
  older UNC-based library definition and still carry those paths.
- The stale manual map (`/media => /mnt/user/media`) matches **neither** form, so
  every candidate fails to map and nothing is warmed.

The important consequence: the two Emby surfaces disagree on path form, so a robust
mapper must handle both container-path and UNC forms. The unifying invariant on
Unraid is the **share name**: an Unraid share lives at `/mnt/user/<Share>`, is
exported over SMB as `\\<host>\<Share>`, and is commonly bind-mounted into the media
container at `/share/<Share>` (or similar). All three share the final `<Share>`
component.

## 2. Goals / non-goals

**Goals**
- Server-queried multi-select of **users to profile** (stable Ids, display names).
- Server-queried multi-select of **libraries** that **scope** the watch-state
  engine (empty = all libraries).
- **Signal-tier dials**: per-tier enable toggle + per-tier `max_items` cap; fixed
  priority order preserved.
- **Automatic path mapping** that makes the sweep warm > 0 bytes on a standard
  Unraid setup, with the manual field demoted to an advanced override.

**Non-goals (deferred)**
- Cache-budget meter (#39).
- Jellyfin adapter - design behind the existing `WatchProvider` interface so it
  drops in later, but implement Emby only now.
- Weighted/re-ranking scorer (dials are on/off + cap, not weights).

## 3. Architecture (Approach A: Go binary is the query backend)

All live queries and secret/URL handling stay in the Go binary; PHP stays a thin
renderer, matching the existing boundary (PHP never parses TOML; `rc.preloadd
render` does the work).

New read-only `preloadd` subcommands, each emitting JSON on stdout and never
echoing the API key:

- `preloadd list-users` -> `[{ "id", "name" }]` (Emby `/Users`).
- `preloadd list-libraries` -> `[{ "id", "name" }]` (Emby `/Library/VirtualFolders`).
- `preloadd detect-pathmaps` (implemented) -> an object
  `{ "rules": [{ "from", "to", "source" }], "unraid_unc_fallback": true }` where
  `source` is `manual` (from the configured `path_map`) or `docker` (from
  `docker inspect`). The `unraid_unc_fallback` boolean reports that the
  host-agnostic UNC / share-name convention (section 6.2) is applied at
  map time in addition to the listed rules; it is not enumerated per-rule
  because it applies to any `\\host\Share` path, not a fixed prefix. `rules`
  is always a JSON array (`[]` when empty), never `null`. The subcommand reads
  the config path from its own `-config` flag (default `config.toml`), so
  `preloadd detect-pathmaps -config <path>` honors the same flag as the run
  modes; a missing/invalid config is non-fatal and yields docker-only rules
  (a warning goes to stderr, keeping stdout valid JSON). Precedence at map
  time is manual rules -> docker rules (longest matching prefix) -> UNC/share
  convention (section 6.3).

The `.page` PHP calls these (as it already calls `rc.preloadd render`), parses the
JSON, and renders the pickers / read-only auto-map table. These commands reuse the
SSRF-hardened Emby client and the secrets store; they are pure reads.

## 4. Config schema

Rendered from the Unraid `.cfg` by `rc.preloadd render` (PHP never writes TOML):

```toml
[users]
enabled = ["<userId>", ...]          # stable Emby user Ids; empty => all users

[libraries]
enabled = ["<libraryId>", ...]       # scope filter; empty => all libraries

[tiers.resume]
enabled   = true
max_items = 20
[tiers.next_up]
enabled   = true
max_items = 20
[tiers.recently_added]
enabled   = true
max_items = 20
[tiers.binge_ahead]
enabled   = false
max_items = 10

[[path_map]]                         # AUTO-detected (rendered read-only) + manual overrides
from = "/share/Movies"
to   = "/mnt/user/Movies"
```

Migration: the existing `users.enabled` (names) is superseded by Ids. On render,
if only legacy names are present, resolve them to Ids once via `list-users`; a name
that no longer resolves is dropped with a logged warning (fail loud).

## 5. Settings UX flow and states

1. **Connect gate.** Pickers and the auto-map table are inert until server URL +
   API key are saved and a connection test succeeds (reuse the existing Test
   button). Before that: an explicit "Connect to a server to choose users and
   libraries" state - never a silent blank.
2. **Populate.** On successful connect, the page calls `list-users` /
   `list-libraries` / `detect-pathmaps` and renders:
   - Users: checkbox list (display name; value = Id).
   - Libraries: checkbox list (scope; empty selection = all).
   - Signal-tier dials: four rows, each `[x] enabled` + `max items [n]`.
   - Auto-detected path maps: read-only table (`from -> to`, source badge), with a
     collapsible "advanced: manual override" section for extra `path_map` rows.
3. **Unreachable / empty.** If a query fails or returns nothing, show the reason
   (unreachable, auth failed, no libraries) - fail loud, do not render an empty
   picker as if it were "no users."
4. **Save.** Selections write to the `.cfg`; `rc.preloadd render` regenerates
   `config.toml`; the engine reads it on the next sweep.

Acceptance for this surface is a **live Unraid webGui test** (rendered-evidence
rule), not static markup review.

## 6. Auto path-mapping (`internal/pathmap`)

Two complementary sources, merged; manual `path_map` rules always win as overrides.

**6.1 Docker inspect (`source = docker`).** Identify the media-server container by
image name (`emby*` / `jellyfin*`), confirmed against the configured server URL's
host when they are co-located. Read its bind mounts and emit `container-dest ->
host-source` rules (e.g. `/share/<Share>` -> `/mnt/user/<Share>`). Requires the
plugin and the container to share a host (true on this setup: cron runs `preloadd`
as root on the Unraid host). Implemented by shelling out to `docker inspect`
(first use of `exec.Command` in the tree) with a bounded timeout; absence of the
`docker` CLI or the container is a soft failure that falls back to 6.2.

**6.2 Unraid share-name convention (`source = share-convention`).** Independent of
where the media server runs. Normalize both path forms to the Unraid share root by
the final `<Share>` component:

- `\\<host>\<Share>\rest` -> `/mnt/user/<Share>/rest`
- `/share/<Share>/rest`   -> `/mnt/user/<Share>/rest`

Backslashes are normalized to forward slashes first. This covers the UNC item paths
the engine receives today and the container-path locations, and it degrades
gracefully when `docker inspect` is unavailable (remote media server) because
Unraid media always lives at `/mnt/user/<Share>`.

**6.3 Resolution order for a given item path:** explicit rules first (manual and
docker rules merged), matched by **longest `from` prefix**; manual rules are
appended ahead of docker rules, so a manual rule wins over a docker rule of the
**same** specificity (stable sort tiebreak). A docker rule with a strictly longer
matching prefix is more specific and wins - the current mapper does not treat
manual rules as an absolute override tier. If no explicit rule matches, the
Unraid UNC / share-name convention (6.2) applies; otherwise the path is unmapped
(logged; counts toward the `missing` stat). (If absolute manual precedence
regardless of specificity is later required, the mapper would need a source-aware
two-pass; out of scope here.)

## 7. Scorer changes (`internal/scorer`)

- Accept a per-tier config (`enabled`, `max_items`) and a library-scope set.
- Skip disabled tiers entirely; truncate each tier's candidates to `max_items`
  before the existing dedup/merge.
- Filter candidates to the selected libraries (by the item's library Id) when the
  scope set is non-empty.
- Priority order and the existing dedup (keep highest-priority tier; resume depth
  ordering) are unchanged.

## 8. Testing

- **Go unit:** `list-users` / `list-libraries` against a mock Emby; `detect-pathmaps`
  against fixture `docker inspect` JSON; `pathmap` normalization table (UNC,
  `/share`, `/media`, backslash, trailing-slash, unmapped); scorer tier-enable /
  cap / library-scope filtering.
- **PHP CLI:** `.cfg` -> `config.toml` render round-trip for the new keys
  (`users.enabled` Ids, `libraries.enabled`, `[tiers.*]`).
- **Live acceptance (outatime):** pickers populate from the real server; a real
  sweep warms > 0 bytes; auto-map table shows resolved rules. Rendered evidence
  required before sign-off.

## 9. Delivery plan (sequenced PRs, each ~<= 1000 hand-written LOC)

1. **Auto path-map + warms-0 fix (#31).** `internal/pathmap` two-source mapping +
   `preloadd detect-pathmaps` + engine wiring. Highest urgency; makes the plugin
   functional. Largely independent of the UI. Live acceptance: sweep warms > 0.
2. **User + library pickers (#32).** `list-users` / `list-libraries` subcommands,
   config schema (`users.enabled` Ids + `libraries.enabled`), `.page` multi-selects,
   connect-gate/empty states, read-only auto-map table.
3. **Signal-tier dials.** `[tiers.*]` config, scorer wiring, UI rows.

Budget meter (#39) is a later PR on top of these.

## 10. Risks / open items

- **Container identification** when multiple media containers exist or the server
  is remote: fall back to 6.2; consider an explicit "media container name" advanced
  field if auto-detect proves ambiguous in the field.
- **Item library Id availability** for scope filtering: confirm Resume/NextUp/Latest
  responses carry a library/parent Id (add a `Fields` param if needed).
- **Legacy name->Id migration** edge cases (duplicate display names): resolve by Id,
  warn on ambiguity.
- **`docker inspect` permissions / CLI absence:** bounded-timeout soft failure ->
  share-convention fallback; never hang the sweep.
