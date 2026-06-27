# Phase 1 - End-to-End Verification (outatime)

Hands-on validation of the Phase 1 engine against the real Emby server on the
Unraid host `outatime`. Tracks [issue #1](https://github.com/sydlexius/watch-aware-preloader/issues/1).

> Status: **IN PROGRESS** - results below are placeholders (`TODO`) until the
> host run is done. Fill each in as the runbook steps complete.

## Environment

| Field | Value |
|-------|-------|
| Host | OutaTime (Unraid 7.3.1, x86_64, Xeon E5-2630L v4) |
| Emby version | `emby/embyserver:beta` (container) |
| Binary | `preloadd` (commit `9ca52e0`, `preloadd-linux`, linux/amd64, static; runs natively) |
| RAM | 188 GiB total, ~112 GiB available (large `ram_percent` budget) |
| Container mounts | `/mnt/user/<Share>` => `/share/<Share>` (bind mounts present) |
| Emby-reported paths | **UNC: `\\outatime\<Share>\...`** (libraries added via SMB, NOT the bind-mount `/share` paths) |

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

- [ ] Per-item tier/user log present: **TODO** (paste a few lines)
- [ ] `mean_resident_pct` reported (Linux mincore): **TODO %**

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
| Status visibility | TODO |
| Cache-hit verification | TODO |
| Measured start-time improvement | TODO |
| Subjective feel | TODO |

**Conclusion:** TODO (does Phase 1 meet its success criteria? any follow-ups filed?)
