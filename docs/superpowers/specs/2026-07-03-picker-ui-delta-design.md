# Picker UI delta: settings-page rendering for the shipped engine backend

Design spec. Date: 2026-07-03. Status: approved for planning.

Delta on the approved design of record
[`2026-07-02-settings-ux-pickers-dials-autopathmap-design.md`](2026-07-02-settings-ux-pickers-dials-autopathmap-design.md)
(issue **#32**). That spec's engine backend is fully shipped: `list-users` /
`list-libraries` / `detect-pathmaps` subcommands (#42), library-scope filter
(#43), signal-tier dials config + scorer wiring (#44), and auto path-mapping
(#40/#41). What remains is section 5 of that spec: the `.page` rendering. This
delta resolves the one gap the parent spec under-specified (how the pickers get
populated across the webGui permission boundary) and the name->Id engine detail,
then sequences the UI into two PRs.

## 1. The populate-boundary problem

The parent spec (section 5.2) says "the page calls these [subcommands] (as it
already calls `rc.preloadd render`)." That assumption is wrong about privilege:
`rc.preloadd render` / `test` / `run-now` are invoked **as root** via
`/update.php` (`#command`), because only root can read the 0600-root
`secrets.toml` on the FAT flash. The `.page` itself renders as the unprivileged
webGui user and **cannot** shell out `preloadd list-users` at render time - it
cannot read the API key.

**Resolution: root writes a cache, the page reads it** - the same boundary the
status panel already uses (`rc.preloadd` writes `status.json` as root; the page
reads it). No setuid helper, no privileged AJAX endpoint, no new attack surface
on the flash secret.

## 2. Populate mechanism

`rc.preloadd test` (already root, already the "connect" action) gains a step: on
a **successful** connection test it runs the three read-only subcommands and
writes a world-readable `pickers.json` next to `status.json`
(`/var/local/preloadd/pickers.json`, overridable via the same `WAP_STATUS_PATH`
dir convention the rc script already uses for testability):

```json
{
  "generated_at": "2026-07-03T...Z",
  "server_url": "http://tower:8096",
  "users":     [{ "id": "<guid>", "name": "Alice" }],
  "libraries": [{ "id": "<id>",   "name": "Movies" }],
  "pathmaps":  { "rules": [{ "from": "/share/Movies", "to": "/mnt/user/Movies", "source": "docker" }],
                 "unraid_unc_fallback": true }
}
```

- The three subcommands each already emit exactly this JSON on stdout and never
  echo the API key; `test` captures and merges them.
- The write is best-effort and must not fail the connection test: a subcommand
  error is logged to stderr, and `pickers.json` is only rewritten when all three
  succeed (a partial cache is never written - fail loud, keep the last good
  cache).
- `library.type` (CollectionType) is intentionally **not** included yet; it is
  only needed for the deferred server-type/library icons (section 7).

Operator flow: enter Server URL + paste API key -> **Save** -> **Test
connection** -> reload the settings page -> pickers are populated from the fresh
cache. (Unraid posts these forms to a `progressFrame`; the page does not
live-refresh, so the populated pickers appear on the next render - standard
Unraid settings UX.)

## 3. Connect-gate (fail-loud)

The page decides gate-vs-pickers purely from the cache:

- `pickers.json` absent, OR its `server_url` != the currently configured
  `SERVER_URL` (stale after a server change): render an explicit gate -
  *"Connect to a server to choose users and libraries: save the Server URL and
  API key, then click Test connection."* Never render an empty checkbox list as
  if it meant "no users."
- A query that returned zero users/libraries is distinct from "not connected":
  if the cache exists and matches the URL but a list is empty, say so
  (*"the server reported no libraries"*) rather than showing the connect gate.

## 4. Rendering (`WatchAwarePreloader.page`)

- **Users** - checkbox list, one row per `pickers.users`, `value=id`,
  label=name. Checked when the id **or** a legacy name is present in the current
  `USERS` cfg value. Replaces today's free-text `USERS` input.
- **Libraries** - checkbox list, one row per `pickers.libraries`,
  `value=id`. Empty selection = all libraries (scope filter). New field
  `LIBRARIES`.
- **Auto-map table** - read-only table of `pathmaps.rules` (`from -> to` with a
  `manual`/`docker` source badge); a one-line note when
  `unraid_unc_fallback` is true (the host-agnostic UNC / share-name convention
  also applies). The existing free-text `PATH_MAPS` field is demoted into a
  collapsible *"Advanced: manual path-map override."*
- All other existing fields (server URL/type, RAM %, target seconds, cron
  interval, API key, Test/Run-now, status panel) are unchanged.

`presave.php` normalizes the posted checkbox arrays (`USERS[]`, `LIBRARIES[]`)
into the comma-separated cfg form that `rc.preloadd render` already consumes,
then `render` emits `users.enabled` / `libraries.enabled` as TOML arrays (verify
`render` already handles these keys from #42/#43; extend if not).

## 5. Engine change (`internal/app/pipeline.go`)

`ResolveUserIDs` matches each configured entry against **both** `u.ID` and
`u.Name`:

```
for each user u:
  if enabled contains u.ID OR enabled contains u.Name -> include u.ID
```

New picker configs (Ids) and legacy configs (names) both resolve, with no
migration step and no render-time `list-users` lookup. Emby Ids are 32-char
GUIDs and names are human strings, so an id/name collision is effectively
impossible. Empty `enabled` still means all users (unchanged).

## 6. Testing

- **Go unit:** `ResolveUserIDs` match-either table - entry matches by id, by
  name, by both, by neither; empty = all; legacy-name config still resolves.
- **rc.preloadd (off-host harness):** `test` writes `pickers.json` on a
  simulated successful connect and merges the three subcommand JSONs; a
  subcommand failure leaves the prior cache intact and does not fail the test.
- **PHP CLI (`settings_test.php`):** page renders the connect-gate when the
  cache is absent/stale and checkbox pickers when the cache matches;
  `presave.php` normalizes `USERS[]`/`LIBRARIES[]` arrays into the cfg form.
- **Live acceptance (outatime, rendered evidence required):** enter creds ->
  Test -> pickers populate from the real server -> select users + libraries ->
  Save -> a real sweep warms > 0 bytes -> auto-map table shows the resolved
  rules. Rendered evidence (selector match, screenshot) before sign-off, per the
  UI/UX rendered-evidence rule.

## 7. Non-goals / deferred

- **Signal-tier dials** -> PR2 (engine already shipped in #44; UI only).
- **Server-type / library icons** (Emby/Jellyfin logos) - not in the design of
  record; a clean follow-up once `library.type` is carried in `pickers.json`.
- **Cache-budget meter** (#39) - rides on top later, per the parent spec.
- **Live-refresh** of pickers without a page reload - out of scope; the
  reload-after-Test flow matches standard Unraid UX.

## 8. Delivery

- **PR1 (this spec, sections 2-6):** populate-cache plumbing + connect-gate +
  users & libraries pickers + read-only auto-map table + `ResolveUserIDs`
  match-either. Live acceptance: pickers populate, sweep warms > 0.
- **PR2:** signal-tier dials - four `[x] enabled` + `max_items` rows, `presave`
  writes `[tiers.*]`, `rc render` emits them. Live acceptance: disabling a tier
  drops its items from the next sweep.

Each PR is independently live-testable and targets <= ~1000 hand-written LOC.
