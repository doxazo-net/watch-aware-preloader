# Phase 2 - Settings UI + Packaging - Design

> Status: approved design of record for Phase 2. Tracks issue #2.
> Companion to the Phase 1 design (`docs/specs/2026-06-26-watch-aware-preloader-design.md`),
> sections 7-9. Date: 2026-06-30.

## 1. Goal

Make the `preloadd` engine installable and configurable on Unraid without editing
files by hand. Deliver an Unraid plugin (`.plg`) that installs the binary, wires a
cron entry to run `preloadd -once`, and provides a PHP settings page that writes
the same `config.toml` the engine already reads, plus a status panel that shows the
last run's results.

## 2. Phase 2 scope (design of record)

Full Phase 2, per issue #2, comprises:

1. **Status-output foundation (Go).** The engine emits a machine-readable run
   summary (`status.json`) that the settings page reads. *This is the first
   deliverable and ships as its own small PR - see section 4.*
2. **`.plg` manifest + install/remove.** A single self-describing `.plg` XML,
   installable by URL, that places the binary + PHP files, seeds `config.toml` if
   absent, creates the status directory, and installs a cron fragment.
3. **Cron entry.** On install and on settings-save, write `/etc/cron.d/preloadd`
   invoking `preloadd -once` at the configured interval. Remove on uninstall.
   Plus an on-demand "Run now" button.
4. **PHP settings page.** Writes `config.toml` (server, users, budget, buffer
   seconds, path-map rows). "Test connection" button. PSR-12, linted by PHPStan +
   PHP-CS-Fixer for `.php`/`.page`.
5. **Status panel.** Reads `status.json`: last run time, mode, per-tier and
   per-user counts, bytes warmed, ok/error.
6. **Optional daemon (deferred within Phase 2).** rc.d init script + `event/`
   hooks for `--daemon` mode, with settings-level mutual exclusivity vs cron.
   Explicitly optional; cron-first ships without it.

### Decomposition / PR plan

Per the "land foundations first" and "batch issues to cut CR churn" directives:

- **PR 1 (this cycle): status-output foundation.** Go-only, no PHP, no `.plg`.
  Small, CI-testable, unblocks the status panel. Detailed in section 4.
- **PR 2: `.plg` + cron + settings page + status panel** (items 2-5), batched.
- **PR 3 (optional, later): daemon rc.d + event hooks + mode toggle** (item 6).

## 3. Distribution model (informative; affects PR 2, not PR 1)

Unraid has two distinct distribution pipelines, and they are not interchangeable:

- **Docker container templates** (e.g. the maintainer's `unraid-templates` repo):
  XML *templates* describing containers. The whole repo is registered once with
  Community Applications (CA); thereafter every template in it appears in the CA
  store automatically.
- **Plugins** (this project): a single self-describing `.plg` XML manifest hosted
  at a stable raw URL. The `.plg` *is* both the template and the package - it
  carries metadata, a `<FILE>` list (binary, PHP, scripts) with checksums +
  download URLs, and inline `<INSTALL>`/`<REMOVE>` shell blocks. There is no
  separate plugin "template" file.

Consequences:

- **Install by URL works day one, no CA.** Users paste the `.plg` URL into
  Plugins -> Install Plugin. Self-update compares the version in the `.plg`.
- **Release artifacts live in this repo's GitHub Releases** (version-pinned raw
  URLs referenced by the `.plg`). They do **not** go in `unraid-templates`, which
  stays docker-only.
- **CA store listing is a separate, per-plugin submission** (moderated plugin
  feed, historically anchored to a forum support thread). Registering a docker
  *repo* with CA does not surface a plugin; a new plugin is its own registration
  even if it shares a repo with docker templates. This is a Phase 4 step.

> TO VERIFY before writing the Phase 4 packaging section: the exact current (2026)
> CA plugin-submission mechanics. The model above is well-established; the precise
> submission steps should be confirmed against Unraid's docs rather than assumed.

## 4. PR 1 - Status-output foundation (this cycle's deliverable)

The engine emits a JSON run summary after every sweep. This decouples the engine
from the UI: Go writes JSON, PHP reads JSON, neither parses the other's logs.

### 4.1 `status.json` schema

Written at the end of each sweep to a configurable path (default
`/var/local/preloadd/status.json`). `/var/local` is tmpfs-backed on Unraid -
appropriate for ephemeral run state (config stays on persistent flash).

```json
{
  "schema_version": 1,
  "last_run": "2026-07-01T04:12:33Z",
  "mode": "once",
  "duration_ms": 1840,
  "ok": true,
  "error": "",
  "budget_bytes": 8589934592,
  "bytes_warmed": 2411724800,
  "preloaded": 33,
  "skipped": 4,
  "missing": 0,
  "by_tier": { "resume": 2, "next_up": 5, "recently_added": 26 },
  "by_user": { "3": 18, "7": 15 }
}
```

Decisions:

- `schema_version` (int) - the PHP reader fails loud on mismatch rather than
  mis-rendering.
- `last_run` - RFC3339 **UTC** in the file (machine format). The panel localizes
  to US Pacific for display per house style; the engine never localizes.
- `mode` - `once` | `verify` | `daemon`, so the panel reports how the numbers
  were produced.
- `ok` + `error` - a failed or partial run still writes status with `ok:false`
  and a short message, so the panel shows failures instead of silently stale data.
- `by_tier` - keys are `Tier.String()` names (`resume`, `next_up`,
  `recently_added`, ...), not integers - stable and readable.
- `by_user` - keys are the raw Emby `UserID`. The engine does **not** embed
  usernames; the panel resolves IDs to current names at display time. `UserID` is
  the stable join key (usernames rename, tokens rotate; the ID does not) and is
  exactly what both the admin-API-key and future per-user-auth flows return. See
  section 6.
- **No per-item titles.** Keeps the file small and free of media identifiers.

### 4.2 Go changes

New unit `internal/status/`:

- `Status` struct mirroring the schema; `Write(path string, s Status) error`.
- **Atomic write:** `MkdirAll` parent (0750) -> marshal -> write a uniquely-named
  temp file (`os.CreateTemp(dir, "status-*.tmp")`) in the same dir -> `os.Rename`
  over the target (atomic on the same filesystem, so the reader never sees a
  partial file; the unique temp name also makes concurrent writers - a daemon
  loop plus a manual `-once` - safe, last rename wins). File mode **0600**
  (`os.CreateTemp`'s default; no explicit chmod). Rationale: `status.json` holds
  Emby UserIDs, so keep it least-privilege; 0600/0750 also satisfy gosec
  G302/G301 without a suppression. The PR-2 reader is the Unraid webGui (emhttp /
  PHP), which runs as root and can read a root-owned 0600 file. (Earlier drafts
  said 0644/0755; tightened during implementation.)
- **Never fatal:** a status-write failure logs at WARN and does not fail the
  sweep. Warming already happened; a missing status file must not turn a
  successful warm into a failed run.

`internal/preloader`:

- `RunStats` gains `ByUser map[string]int`, populated alongside the existing
  `ByTier` as targets are processed (the preloader already holds
  `target.Item.UserID`). Single source of truth for run aggregation (approach A;
  rejected alternative: aggregate in `app.RunOnce`, which splits stat ownership
  across two layers and re-walks the targets).

`cmd/preloadd` / `internal/app`:

- After each sweep, the mode handler builds a `Status` from `RunStats` + timing +
  mode + error and calls `status.Write`. Applies to `-once`, `-verify`,
  `-daemon` (daemon overwrites after every loop iteration). An errored run still
  writes with `ok:false`.

### 4.3 Config

New top-level key (status output is not scheduling, so not under
`ScheduleConfig`):

```toml
status_path = "/var/local/preloadd/status.json"   # default if omitted
```

Default applied at config load like the other defaults; overridable so tests
write to a temp dir.

### 4.4 Testing

- `internal/status`: golden-JSON marshal shape; atomic-write behavior (tmp
  cleaned up, rename result correct); parent-dir creation; write-to-bad-path
  returns an error (and is non-fatal at the call site).
- `internal/preloader`: extend run-stats tests to assert `ByUser` counts across a
  multi-user, multi-tier target set.
- No Unraid/host dependency; all pure Go, runs in CI.

## 5. PR 2/3 design notes (built later, on the foundation)

**Config surface = the UI, not a hand-edited file (maintainer directive,
2026-06-30).** In Phase 2 the settings page is the config surface; `config.toml`
becomes a UI-generated artifact (the wire format between UI and engine), not
something users copy-and-edit. Consequences:

- **Remove `config.example.toml`.** The `.plg` generates a default `config.toml`
  on first install programmatically (issue #2 Task 1's "create config.toml if
  absent"). Do this removal *in PR 2, together with* the `.plg`-seed work - not
  in the status-foundation PR, so the only config template is never deleted
  before its generator exists. Advanced/headless/dev users configure by editing
  the generated `config.toml` directly; the schema is documented in this spec and
  the README, not a shipped example file.
- **Credentials are NOT stored in `config.toml` (maintainer directive,
  2026-06-30).** The UI writes only non-secret settings to `config.toml` (server
  URL/type, users, libraries, RAM %, buffer seconds, path map, schedule,
  `status_path`). The API key - and future per-user tokens (section 6) - live in
  a separate secret store with restricted permissions (0600, root-owned on
  Unraid), never in the UI-generated config file and never in git. This changes
  the current model, where `server.api_key` sits in `config.toml`; migrating the
  key out of the TOML is part of the PR 2 credentials work and is designed
  together with issue #18. The internal-only `status_path` key is defaulted and
  not surfaced in the primary UI.
- **Settings page** writes the non-secret `config.toml` keys (section 8 of the
  Phase 1 design). "Test connection" calls the server auth/ping using the key
  from the secret store. Status panel reads `status.json`; on `schema_version`
  mismatch or missing file it shows a clear "no run yet / version mismatch"
  state, not a blank panel.
- **Cron fragment** `/etc/cron.d/preloadd` rewritten on settings-save from the
  configured interval; removed on uninstall. "Run now" invokes `preloadd -once`.
- **config.toml on uninstall:** preserve per Unraid convention (config lives on
  persistent flash); document the choice explicitly in the remove script. The
  separate secret store follows the same preserve-or-purge decision, documented
  explicitly.
- **Daemon (PR 3):** rc.d `start`/`stop`/`restart` + `event/started` &
  `event/stopping_svcs`; selecting daemon mode removes the cron entry and vice
  versa (mutual exclusivity enforced in the settings page).

## 6. Forward-compatibility: per-user authentication (future, own spec)

A stated goal is to let household users authenticate with their own Emby/Jellyfin
accounts, in addition to the admin API key. This is a substantial capability that
reshapes the credentials model and the settings UI, and it gets its **own
brainstorm -> spec -> plan cycle** (tracked separately). It is out of scope for
Phase 2's build.

Nothing in Phase 2 blocks it, because the design keys everything user-scoped on
the stable Emby `UserID`:

- Run stats (`by_user`) key on `UserID`.
- Enabled-users config keys on `UserID`.
- Future per-user credentials will key on `UserID`.

Expected future shape (informative, not built here): the credentials model
supports two auth modes side by side - (a) admin API key iterating `?UserId=`
(today), and (b) per-user access tokens obtained via `POST
/Users/AuthenticateByName` (returns an access token + `User.Id`), stored per user,
preloading only that user's watch state. This touches `internal/config`
(per-user tokens), `internal/mediaserver` (auth flow + token refresh/revocation),
and the settings UI (a per-user "Sign in" flow).

## 7. Open items

- Confirm current (2026) CA plugin-submission mechanics before the Phase 4
  packaging write-up (section 3 note).
- Per-user authentication (section 6) tracked in issue #18 (own spec/phase).
- Daemon-mode rc.d/event-hook work (PR 3) is optional and may slip past the
  cron-first release.
