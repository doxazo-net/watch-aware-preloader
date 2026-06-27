# Watch-Aware Media Preloader - Design

Date: 2026-06-26
Status: Approved design, ready for Phase 1 implementation plan
Plugin name: **Watch-Aware Preloader** (binary `preloadd`; repo/module slug
`watch-aware-preloader`).

## 1. Problem

Unraid array disks spin down to save power and noise. On playback start there is
an 8-10 second stall while the disk spins up. The popular workaround ([Marc Gutt's
"Video Preloader"][vp] bash script) reads the first ~60 MB of recently-*modified*
video files into the Linux page cache so playback starts from RAM instead of a cold
disk.

[vp]: https://forums.unraid.net/topic/97982-video-preloader-avoids-hdd-spinup-latency-when-starting-a-movie-or-episode-through-plex-jellyfin-or-emby/

Its core weakness: it uses filesystem modification time as a proxy for "what will I
watch next." That proxy is poor. The media server (Emby/Jellyfin) already knows the
real answer - resume points, next-up episodes, recently-added, what each user has
been watching - and the script throws that signal away. It also uses a single fixed
byte size for every file, which buffers ~120 s of a low-bitrate SD cartoon but under
7 s of a 60 Mbps 4K file - less than the spin-up it is trying to mask.

## 2. Goals / Non-goals

### Goals

- Preload the media each household user is genuinely likely to play next, using the
  media server's own watch state, not filesystem mtime.
- Size each preload by *duration* (cover the spin-up window consistently) regardless
  of resolution/bitrate, using metadata the server already provides.
- Preload resume candidates from the byte offset where playback will resume, not the
  file head.
- Re-evaluate on a schedule (cron one-shot) so library and watch-state changes are
  picked up each run; optionally react faster via a resident `--daemon` mode.
- Ship as an Unraid plugin (native `.plg`, host daemon, PHP settings page) supporting
  both Emby and Jellyfin, publishable via the user's `unraid-templates` repo and
  Community Apps.

### Non-goals (initially)

- Transcoding, streaming, or any media serving. This only warms the page cache.
- Replacing the media server's own caching or read-ahead.
- Phase 1 does not include the web UI or Jellyfin (see Phasing).

## 3. Environment context (validated on the target server)

- Unraid host `outatime` (192.168.1.126).
- 2x Xeon E5-2630L v4 = 40 threads; 188 GiB RAM; **no swap**.
- HDD array, measured ~8-10 s spin-up; non-trivial amount of 4K media (4K WEBDL TV
  confirmed, e.g. ~25 Mbps episodes, plus 4K movies).
- Emby is the primary server, running in Docker with each media share individually
  bind-mounted (`/mnt/user/<Share>` -> `/share/<Share>`). Reading the bind-mounted
  path from the host warms the **same** page cache (shared kernel), so host-side
  reads are correct and need no special handling beyond path mapping.
- Host `ffprobe` is broken (missing `libass.so.9`); Emby's container `ffprobe` works.
  This is moot - we get bitrate/duration from the API and never shell out to ffprobe.

Design implication: with this much RAM and no swap, the RAM budget is effectively a
"how many entries to keep warm" knob, not a memory-safety risk - preloads live in
reclaimable, clean (file-backed) page cache that the kernel drops instantly under
pressure and never swaps.

## 4. Architecture

Single static Go binary `preloadd` running as a host daemon, five internally
decoupled units:

```
                 Unraid host (single static Go binary: preloadd)
  +--------------------------------------------------------------+
  |  [media-server client]  one interface, 2 adapters            |
  |     Emby adapter ---+                                         |
  |     Jellyfin adapter+--> auth (API key), fetch watch signals, |
  |                         subscribe to live playback events     |
  |            |                                                  |
  |            v                                                  |
  |  [scorer] merge users -> dedupe -> rank into tiers ->         |
  |            |            ordered []PreloadTarget               |
  |            v                                                  |
  |  [preloader core] duration-based reads into page cache,       |
  |            |        byte-budget accounting, skip-if-warm      |
  |            v                                                  |
  |  [path mapper] server path -> host path                      |
  |  [state + config] config (TOML), warm-set ledger, dedupe      |
  +--------------------------------------------------------------+
       ^ websocket/webhook events        ^ config + status
       |                                 |
   Emby/Jellyfin server            PHP Settings page (Phase 2)
```

### Units

1. **Media-server client** - Go interface `WatchProvider` with Emby and Jellyfin
   adapters. Exposes `ResumeItems()`, `NextUp()`, `RecentlyAdded()`, `Sessions()`
   (for active-session exclusion), and `Subscribe()` (live playback events). Returns
   normalized structs carrying server path, bitrate, size, runtime, and resume
   position.
2. **Scorer** - pure function (no I/O): per-user signals -> ranked, deduped
   `[]PreloadTarget`. Trivially unit-testable.
3. **Preloader core** - takes targets + budget; performs the reads, tracks bytes,
   skips already-resident ranges.
4. **Path mapper** - server-reported path -> host path; auto-built from
   `docker inspect <server-container>` when the server is a local container, manual
   override in config. Backslashes are normalized to forward slashes, so libraries
   added over SMB that report Windows UNC paths (`\\host\Share\...`) map via a
   normal longest-prefix rule anchored on the UNC host (e.g. `\\host` -> `/mnt/user`).
5. **State + config** - TOML config (written by the PHP page in Phase 2, hand-edited
   in Phase 1), in-memory warm-set ledger, dedupe set.

### Reuse from stillwater (`~/Developer/stillwater`)

The hardest, riskiest layer already exists, production- and UAT-tested:
- `internal/connection/emby/client.go`, `internal/connection/jellyfin/client.go` -
  auth headers (`Emby UserId="..."`, `MediaBrowser Token="..."`), `Get`, `/Users`,
  `/Items` with `Fields=`.
- `internal/connection/httpclient` - shared HTTP client.
- `internal/connection.BuildRequestURL` / `ValidateBaseURL` - SSRF hardening (rejects
  embedded credentials, query, fragment; loopback/LAN rationale).
- `internal/webhook/{emby,jellyfin}.go` - webhook event parsing scaffold (UAT-confirmed
  against Emby 4.9; dot-separated event names in the `Event` field). We extend this
  with *playback* events (or the `/Sessions` websocket) for live triggers.

The media-server client unit is therefore an *extension* of stillwater's patterns,
not a from-scratch build. (Copy/adapt the patterns into the new project; do not create
a cross-repo dependency.)

## 5. Watch signals, tiers, and the scorer

Every returned item carries `MediaSources[].Path/.Bitrate/.Size`, `RunTimeTicks`, and
`UserData.PlaybackPositionTicks` - everything needed, in one fetch per signal.

| Tier | Signal | Endpoint (per enabled user) | What we preload |
|------|--------|------------------------------|-----------------|
| 1 | **Recent incompletes** (paused/stopped, NOT currently playing) | `GET /Users/{id}/Items/Resume` minus active sessions | bytes at the **resume offset** |
| 2 | **Next-up** (next episode of an active series) | `GET /Shows/NextUp?UserId={id}` | head (+ tail) |
| 3 | **Recently added**, unwatched, in enabled libraries | `GET /Users/{id}/Items/Latest` | head |
| 4 | **Binge look-ahead** (episode after next-up) | derived from NextUp + season query | head |
| 5 | **Best-effort fill** | filesystem recency (mtime), enabled libraries | head, until budget exhausted |

Active-session exclusion: the scorer fetches `GET /Sessions`, collects every
`NowPlayingItemId`, and removes those items from all tiers. An item being actively
streamed is already spun-up and resident; preloading it wastes budget. (This also
maps cleanly to events: a *play start* explicitly skips that file; a *pause/stop*
turns the item into a Tier-1 resume candidate and triggers preload of its next-up.)

Data flow:
```
 per enabled user -> [client] fetch tiers 1-4 --+
 enabled libraries -> [recency scan] tier 5 ----+
                                                v
        [scorer] normalize -> assign tier -> DEDUPE by file
                 -> sort (tier asc, then resume>recency) -> []PreloadTarget
                                                v
        [preloader] map path -> host; size from bitrate; read into cache;
                    subtract from budget; stop at 0
```

Phase 1 implements tiers 1-3 + tier 5; tier 4 (binge look-ahead) is Phase 3.

## 6. Preloader core (engineering decisions - owner: implementer)

These are documented defaults, not user-facing knobs:

- **Duration-based sizing**: `want_bytes = clamp(target_seconds * bitrate/8, 8MB, cap)`.
  `target_seconds` default ~20 s (covers 8-10 s spin-up plus decode/start margin).
  Bitrate comes from `MediaSources[].Bitrate`; fall back to `size/runtime` if absent.
- **Resume-offset reads** (Tier 1): `offset = (PositionTicks / 1e7) * bitrate/8`, read
  `want_bytes` from `offset` (e.g. `dd skip=`/`pread`), plus a small tail.
- **Tail read**: small (~1 MB) trailing read to cover MP4 `moov` atoms at EOF.
- **Warm detection**: `mincore(2)` (via `mmap` + `unix.Mincore`) to ask the kernel
  exactly which pages of the intended range are already resident, and skip them. This
  replaces Marc Gutt's "time a read, fast == cached" heuristic - deterministic, no
  threshold to tune, no wasted I/O.
- **Concurrency**: a small bounded worker pool (start 4-8) reads multiple files in
  parallel; capped to avoid thrashing array head seeks. Tunable; not exposed to users
  initially.
- **Budget**: `free_ram_usage_percent` (default 50) of currently-available RAM,
  byte-accounted because file sizes now vary. Page cache is reclaimable, so this is a
  warm-set-size knob, not a safety limit.
- **Path mapping**: auto-detected from `docker inspect`, manual override available.

## 7. Run model

**Decision (revised): cron-invoked one-shot is the primary model; the resident
daemon is an optional mode.** This matches how Unraid schedules work (Fix Common
Problems, the Mover, the User Scripts plugin are all cron-invoked one-shot
programs, not resident services), and it removes the supervision burden
(init-script, restart-on-crash, surviving plugin updates) the maintainer was
wary of. The earlier "long-running event-driven service" plan is retained only as
the opt-in `--daemon` mode.

- **One-shot (`preloadd -once`, also the default with no flag)**: run exactly one
  full sweep - re-fetch watch state from the API, re-score, warm the page cache
  within budget - then exit. "One-shot" describes the process lifecycle, not the
  evaluation frequency: cron re-invokes the program every interval, so each run is
  a complete fresh evaluation that picks up library changes (new episodes land in
  the recently-added tier, new resume points and next-up surface, recency fill
  catches the rest). Worst-case staleness is one cron interval.
- **Packaging (Phase 2)**: the `.plg` installs a **cron entry** (e.g. every few
  minutes) invoking `preloadd -once`, plus an on-demand "run now" button. No
  rc.d init script is required for the default model.
- **Optional daemon (`preloadd --daemon`)**: the resident loop - a periodic sweep
  plus a fast `/Sessions` poll that triggers a sweep on playback-state changes,
  for anyone who wants sub-cron-interval reaction. When used, the `.plg` installs
  an rc.d init script + Unraid event hooks to supervise it. `sweep_seconds` /
  `session_poll_seconds` config apply to this mode only.
- **`preloadd -verify`**: one sweep then report page-cache residency, for testing.

A true websocket subscription (instant, no polling) remains a possible Phase 3
enhancement to the `--daemon` mode; the cron one-shot model does not need it.

## 8. Configuration surface (what the user touches)

Phase 1: a TOML file. Phase 2: the PHP settings page writes the same file.
- Server: type (emby/jellyfin), base URL, API key; "Test connection".
- Users: which accounts drive preloading (default all enabled).
- Libraries: include/exclude (sensible defaults; resume/next-up already self-select).
- RAM budget percent (default 50).
- Buffer length `target_seconds` (default ~20; "raise if playback still stutters").
- Path mapping: auto-detected rows + manual override.
- Status panel (Phase 2): currently-warm set, last run, per-tier/per-user counts.

## 9. Phasing

Each phase is its own spec -> plan -> build -> ship.
1. **Phase 1 - Engine MVP**: Go binary, Emby only, cron one-shot (`-once`) primary
   with optional `--daemon`, tiers 1-3 + tier 5 fill, TOML config, runs on the
   target server. Proves the core. *(First spec.)*
2. **Phase 2 - Settings UI + packaging**: PHP settings page, `.plg`, **cron entry**
   (+ optional rc.d init script for `--daemon`); installable/configurable without
   editing files.
3. **Phase 3 - Jellyfin adapter + binge look-ahead (tier 4)**: second platform; polish.
4. **Phase 4 - Public release**: Community Apps template, docs, versioned releases.

## 10. Success criteria (Phase 1)

All four agreed:
1. **Measured start-time** - time-to-first-frame on a cold (spun-down) title, preloader
   off vs on; must beat raw spin-up.
2. **Subjective feel** - next-up / resume titles start without the usual stall.
3. **Status visibility** - log/status output showing what was preloaded each cycle and
   why (tier/user), to confirm sensible choices.
4. **Cache-hit verification** - confirm via `mincore`/`vmtouch` that the intended byte
   ranges are resident after a run.

## 11. Risks and open items

- **Websocket playback events**: confirm Emby exposes the needed play/pause/stop
  session events over websocket with an API key and no extra Emby plugin; fall back to
  the Webhooks plugin if not. (Validate early in Phase 1.)
- **Path mapping when the server is not a local container** (remote/VM Emby): the host
  cannot warm cache for files it cannot read locally; document this limitation and rely
  on manual path mapping / skip unreachable paths.
- **Resume-offset accuracy**: VBR means `position*bitrate` is approximate; mitigate by
  reading a slightly wider window around the computed offset.
- **`mincore` correctness across the array/FUSE (`/mnt/user` shfs)**: validate that
  `mincore` reports residency correctly through Unraid's user share layer; fall back to
  reading the underlying disk path or to the timing heuristic if it does not.
- **New project repo location**: decided - dedicated repo `watch-aware-preloader`
  (standalone; copies stillwater patterns rather than depending on it).

## 12. Testing approach

- **Scorer**: pure-function unit tests over recorded API fixtures (tier assignment,
  dedupe, active-session exclusion, ordering).
- **Client adapters**: table tests against captured Emby/Jellyfin JSON responses
  (reuse stillwater's fixture style).
- **Preloader core**: integration test on a temp file - verify the intended ranges are
  resident afterward via `mincore`; verify resume-offset math; verify budget stop.
- **End-to-end on target**: the four success criteria above, run on `outatime`.
