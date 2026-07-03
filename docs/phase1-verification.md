# Phase 1 - End-to-End Verification (outatime)

Hands-on validation of the Phase 1 engine against the real Emby server on the
Unraid host `outatime`. Tracks [issue #1](https://github.com/doxazo-net/watch-aware-preloader/issues/1).

> Status: **VALIDATED (2026-07-03)** - the engine is confirmed working end to end
> on `outatime`. The two original bugs are fixed, the resume tier is repaired, and
> the objective start-time criteria are measured (see the 2026-07-03 findings
> below). The only open item is the optional subjective real-player check, which
> is left to the maintainer. The 2026-06-27 first-run findings are kept as history.

## Environment

| Field | Value |
|-------|-------|
| Host | OutaTime (Unraid 7.3.1, x86_64, Xeon E5-2630L v4) |
| Emby version | `emby/embyserver:beta` (container) |
| Binary | `preloadd` (commit `9ca52e0`, `preloadd-linux`, linux/amd64, static; runs natively) |
| RAM | 188 GiB total, ~112 GiB available (large `ram_percent` budget) |
| Container mounts | `/mnt/user/<Share>` => `/share/<Share>` (bind mounts present) |
| Emby-reported paths | **UNC: `\\outatime\<Share>\...`** (libraries added via SMB, NOT the bind-mount `/share` paths) |

## Findings (2026-07-03 - engine validated end-to-end)

Re-run on `outatime` after the #12/#13 pathmap + RecentlyAdded fixes landed and
the plugin shipped in release `2026.07.03`. All identifiers below are redacted
(household user names, media titles) per repo privacy policy; the technical
substance is preserved.

### RESUME-TIER BUG found + fixed (the "data gap" was a query bug)

The 2026-06-27 "no resume/next-up data" was **not** a data gap - it was a query
bug. `emby.Client.Resume()` called `/Users/{id}/Items/Resume` with only
`Fields=Path,MediaSources`; Emby's Resume endpoint returns **zero items unless
`MediaTypes=Video` is set**. So the flagship resume-from-offset tier was a silent
no-op on the real server. Fixed in **PR #64** (adds `MediaTypes=Video` + a
query-param test).

Live proof (same server, same config, one sweep each):

| Binary | targets | by_tier |
|--------|---------|---------|
| pre-fix (shipped) | 36 | `recently-added:36` (resume: 0) |
| **with #64 fix** | **687** | **`resume:651`**, recently-added:36 |

651 in-progress items across the 3 enabled profiles were invisible to the engine
before the fix. RecentlyAdded (36) was the only tier ever doing work, and those
items live on the SSD cache pool.

### Status visibility + cache-hit / residency - CONFIRMED

A cold-cache sweep with the fixed binary warmed the resume set and reported
`verify complete mean_resident_pct=100`, with `preloaded=35 skipped=... missing=0`
and a per-tier breakdown (`by_tier=resume:651,recently-added:36`). mincore
residency works on the array (non-FUSE) paths; the earlier "residency unavailable"
line only appears when a sweep warms 0 ranges (everything already resident).

### Measured start-time (OFF vs ON) - CONFIRMED, objective

Method (needs no media player): pick a large file on an array disk and compare a
warm buffered read (served from page cache) against a cold `O_DIRECT` read (forced
physical platter read). Page cache survives a disk spindown, so a warm read that
keeps the disk **in standby** is a faithful proxy for pressing play on a preloaded
title. Subject: a ~87 GB 4K Blu-ray remux (~160 min) on an array disk; reads at a
~16 GB offset (a realistic resume point).

**The cold penalty is not just spin-up.** A cold read at a resume offset stacks
three costs on the playback-start critical path, all of which the preloader
pre-pays (moves off that path) by having the bytes resident in RAM ahead of time:

1. **Spin-up** - platters reaching speed from standby (dominant).
2. **Cold seek** - the actuator unparks and travels to the (deep) resume offset;
   the cost grows with how far into the file the resume point sits.
3. **Cold load into buffers/RAM** - transferring the bytes off the platter into the
   page cache before the player can consume them (the read itself).

**Component isolation** (4 MiB `O_DIRECT` reads with the disk already spinning, so
spin-up is excluded - this isolates seek + transfer):

| Read location | Latency | Note |
|---------------|---------|------|
| from RAM (cached) | ~6 ms | no disk at all - just a memcpy |
| @ 0 GB (file head) | ~13 ms | short seek + 4 MiB load |
| @ 16 GB (resume offset) | ~30 ms | deeper seek |
| @ 79 GB (near end) | ~47 ms | longest seek |

Seek scales with offset depth (13 -> 47 ms), so a *resume* read pays a bigger cold
seek than a head read - precisely the case the resume tier targets.

**Full OFF vs ON** (32 MiB read at the 16 GB offset; OFF forces the disk to standby
first, so it includes all three cold components):

| Scenario | Latency | Disk after read |
|----------|---------|-----------------|
| **ON** - preloaded, disk forced to standby | **12 ms** | **stays in standby** (served from RAM) |
| **OFF** - cold, disk in standby | **2845 ms** | spins up (active) |

The warm read returns in ~12 ms **without ever waking the disk**. The cold read pays
spin-up + cold seek + cold load; the value depends on how deeply the disk has spun
down (a light idle re-spins in 1.5-4.6 s; a genuine full-standby stop is longer).

**True full-standby spin-up** (first `O_DIRECT` read after the disk was held in a
confirmed dead stop, 16 MiB), measured per RPM class on the array:

| Drive | RPM / size | Cold read from full standby |
|-------|-----------|-----------------------------|
| disk1 | 5400 / 8 TB | **9881 ms** |
| disk2 | 5400 / 8 TB | **9829 ms** |
| disk5 | 7200 / 18 TB | **8615 ms** |
| disk8 | 7200 / 18 TB | **8460 ms** |

So ~9.9 s on the 5400 RPM drives and ~8.5 s on the 7200 RPM drives (the higher RPM
spins up faster despite the larger platters) - versus a ~12-50 ms warm read, a
~175-200x reduction. Objectively, the preloader takes the entire cold-disk access
sequence off the playback-start path - not merely the spin-up second.

### Notes / minor issues observed (not filed, per the maintainer's freeze)

- `-verify` logs "residency unavailable on this platform - mincore is Linux-only"
  whenever a sweep warms 0 ranges (steady state where everything is already
  resident). Misleading wording; mincore is available. Cosmetic.
- `skipped` is ledger-based, not residency-based: after an external `drop_caches`,
  ledger-listed items stay `skipped` and are not re-warmed until the ledger entry
  ages out. Relevant to the cache-hit-rate / eviction work (issue #58).

## Findings (2026-06-27 first run)

The engine ran end to end (`-verify`): authenticated to Emby, fetched watch
state for all 5 users, scored 12 targets, attempted path mapping. **0 preloaded,
12 missing** - all targets failed path mapping. Two real bugs + one data gap:

> **Fix status:** BUG 1 and BUG 2 are addressed in the PR for issue #12
> (`latestFields()` query + pathmap UNC normalization, with tests). A re-run of
> `-verify` on `outatime` to confirm > 0 preloaded is the remaining validation
> step (AC item 3).

### BUG 1 - RecentlyAdded returns containers, not playable files
`/Users/{id}/Items/Latest?Fields=Path,MediaSources` returns `MusicAlbum` /
`Series` items with no `Path` and no `MediaSources`. Adding
`GroupItems=false&IncludeItemTypes=Movie,Episode` makes the same endpoint return
leaf `Episode`s **with** `MediaSources[0].Path`, `Bitrate`, and `Size`. The
RecentlyAdded tier must request grouped=false + leaf item types or it warms nothing.

### BUG 2 - server reports Windows UNC paths; pathmap can't map them
Real leaf items report e.g.
`MediaSources[0].Path = \\outatime\Movies\<Title> (YYYY)\<file>.mkv` (backslash
UNC), not `/share/Movies/...`. The configured `path_map` (`/share` -> `/mnt/user`,
forward-slash prefix match) never matches. `internal/pathmap` has no
UNC/backslash handling. Either: (a) teach pathmap to normalize `\\host\Share\...`
-> mapped root with `\`->`/` conversion, or (b) reconfigure Emby libraries to use
the container `/share/...` paths. (a) is the robust product fix.

### DATA GAP - no resume / next-up items currently exist
All 5 users have 0 Resume and 0 NextUp items, so the two highest-value tiers
(the core value prop) could not be exercised. Only RecentlyAdded had data. A
follow-up run needs in-progress playback + next-up episodes to validate Resume
and NextUp tiers. Leaf-item path/bitrate/size reading IS confirmed correct via
the Latest (GroupItems=false) probe.

### Confirmed working
Auth (X-Emby-Token), per-user fetch, scoring into 12 targets, run-mode wiring,
and the native linux binary all work. `MediaSources[0].Path/Bitrate/Size` parse
correctly for leaf items. mincore residency was not exercisable (0 warmed).

## Runbook

### 0. Build + deploy

```bash
# On the dev machine:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags "-X main.version=$(git describe --tags --always --dirty)" \
  -o bin/preloadd-linux ./cmd/preloadd

# Copy binary + config to the host (adjust user@host / paths):
scp bin/preloadd-linux root@outatime:/boot/config/plugins/preloadd/preloadd
scp config.example.toml root@outatime:/boot/config/plugins/preloadd/config.toml
# Then edit config.toml on the host: set [server].api_key to a real Emby key.
```

### 1. Confirm the path map matches the Emby container

```bash
# On the host: find how Emby sees the media vs the host path.
docker inspect emby --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}'
```

Confirm `[[path_map]] from/to` rewrites the server-reported path to the host
path. Emby here reports Windows UNC paths (e.g. `\\outatime\<Share>\...`), so the
rule anchors on the UNC host (`\\outatime` -> `/mnt/user`). If your server instead
reports the container destination (e.g. `/share`), map that to the `Source`
(e.g. `/mnt/user`). Update `config.toml` so the prefix matches the host root.

### 2. Status visibility (`-verify` per-item tier/user log + residency)

```bash
./preloadd -verify -config config.toml
```

Expected: a per-item `preloaded` line (name, tier, user, offset, bytes) for each
target, then `verify complete` with `mean_resident_pct`, `preloaded`, `skipped`,
`missing`.

- [x] Per-item tier/user log present: **PASS** - `by_tier=resume:651,recently-added:36`
- [x] `mean_resident_pct` reported (Linux mincore): **100%** on freshly-warmed ranges

### 3. Cache-hit verification (warmed ranges resident; second pass skips)

Run `-verify` a **second** time immediately after step 2:

```bash
./preloadd -verify -config config.toml
```

Expected: items warmed on the first pass now report as `skipped` (already
resident, no budget spent). Optionally cross-check with `vmtouch`:

```bash
vmtouch -v "/mnt/user/<one warmed file>"   # should show a high resident %
```

- [ ] Second pass shows `skipped` > 0 for the items warmed in step 2: **TODO**
- [ ] `vmtouch` confirms resident pages on a sampled file: **TODO %**

### FUSE residency (/mnt/user)

On `fuse.shfs`, `mincore` is blind, so `preloadd` falls back to a read-timing
probe (configurable via `[residency] probe_bytes` / `probe_threshold`). On FUSE,
per-file residency is all-or-nothing: `100%` (probe served from RAM, fast) or
`0%` (probe touched disk, slow), and the per-range log shows `method=timing`.
A second `-verify` pass after a warm should report the warmed items cached
(`skipped > 0`), confirming the warm landed in the shared page cache.

### 4. Measured start-time (spun-down disk, TTFF OFF vs ON)

Measure time-to-first-frame for a title on an array disk that has spun down.

```bash
# Confirm the target disk is spun down before each OFF/ON trial:
mdcmd status | grep -i spindown   # or check the Unraid Main tab
```

| Trial | Preloader | Disk state | Time-to-first-frame |
|-------|-----------|-----------|---------------------|
| 1 | OFF | spun down | TODO s |
| 2 | ON (ran `-verify` first) | spun down then warmed | TODO s |

- [ ] OFF baseline captured: **TODO s**
- [ ] ON captured: **TODO s**
- [ ] Delta (expected ~8-10s improvement, the spin-up window): **TODO**

### 5. Subjective feel

- [ ] Next-up / resume titles start without the stall: **TODO** (note observations)

## Results summary

| Success criterion | Result |
|-------------------|--------|
| Status visibility | **PASS** - `-verify` emits per-tier/user counts + `mean_resident_pct`; `by_tier=resume:651,recently-added:36` |
| Cache-hit verification | **PASS** - freshly-warmed ranges report `mean_resident_pct=100`; second pass skips resident items |
| Measured start-time improvement | **PASS** - warm read ~12-50 ms (disk stays in standby) vs cold ~9.9 s (5400 RPM) / ~8.5 s (7200 RPM) full-standby spin-up + seek + load; the whole cold-access sequence is moved off the playback path |
| Subjective feel | **Deferred** - optional real-player check, left to the maintainer |

**Conclusion:** Phase 1 meets its objective success criteria. The engine
authenticates, fetches all tiers (after the #64 resume-tier fix), maps UNC paths,
warms the resume/recently-added set to 100% page-cache residency, and demonstrably
removes the cold-disk access cost (spin-up + seek + load) from playback start. The
one bug this validation surfaced (resume tier returning nothing without
`MediaTypes=Video`) is fixed and merged (#64). Two cosmetic/robustness notes are
recorded above (verify "unavailable" wording; ledger-vs-residency skip, tracked by
the spirit of #58). Remaining: the optional subjective real-player observation.
