# FUSE-proof page-cache residency via a shared timed-probe mechanism

- Status: Approved (design)
- Date: 2026-06-30
- Issue: #15 - "Residency/cache-hit detection fails on Unraid FUSE shares (/mnt/user); use read-timing like Video Preloader"
- Supersedes/extends: `internal/pagecache` residency detection from PR #10

## Problem

On the target Unraid host, `/mnt/user` is `fuse.shfs` - the user-share FUSE
overlay merging `/mnt/disk1..N`. `mincore(2)` cannot report page-cache residency
through a FUSE inode, so after a successful warm:

- `preloadd -verify` reports `mean_resident_pct=0` for every file (misleading).
- The "skip already-resident" optimization never fires, so every run re-warms
  everything (`skipped=0`).

Confirmed on `outatime`: two consecutive `-verify` passes both report
`preloaded=33, skipped=0, mean_resident_pct=0`. The warm itself works (blocking
reads pull pages into the underlying `/mnt/diskN` page cache, which is shared
with playback) - only the **measurement** and the **skip** are broken.

## Root cause

`internal/pagecache/resident_linux.go` uses `mmap`+`mincore` on the given path.
Through `fuse.shfs` this returns all-zero residency regardless of actual cache
state.

## Approach (decided)

Add a single **timed-probe** primitive to `internal/pagecache` and make it the
one source of truth for "is this range in the shared page cache, and how long did
a cold touch cost." Wall-clock read latency reflects the real shared page cache
regardless of the FUSE layer (the prior-art Video Preloader script relies on the
same heuristic). `mincore` remains the fast path on filesystems where it works;
the probe is used only where `mincore` cannot see through (FUSE).

Decisions locked during brainstorming:

1. **Detection:** on Linux, `statfs` the path; if `f_type == FUSE_SUPER_MAGIC`
   (`0x65735546`) use read-timing, else use `mincore`. Deterministic, per-path,
   no false fallbacks. Result cached per-mount.
2. **Classification model:** read a **fixed-size probe** (`probe_bytes`, default
   1 MiB) from the range start and time it; `cached` iff `elapsed < probe_threshold`
   (default `150ms`, mirroring the proven Video Preloader value). The threshold
   detects a *categorical* difference (touched a physical disk vs served from RAM),
   so it need not scale with probe size.
3. **Interface:** the `Cache.Resident` signature does **not** change. On FUSE it
   returns all-or-nothing byte counts - `length` (cached) or `0` (cold) - so the
   skip-optimization (`r >= length`) and `-verify` (`resident/length*100`) work
   untouched. On FUSE, `-verify` therefore shows per-file 100%/0% and the mean
   reads as "% of files cached"; the log line notes `method=timing` so the coarse
   number is not mistaken for a true `mincore` percentage.
4. **Single shared mechanism:** the one `probe()` call yields both the
   classification *and* a cold-latency diagnostic (logged, never fed back into
   control logic). No second timing path anywhere.

### Alternatives considered (and rejected)

- **Fall back when `mincore` reads 0:** circular - a genuinely cold file also reads
  0, so it cannot distinguish "FUSE lies" from "truly not cached," forcing probe
  reads everywhere.
- **Resolve the real `/mnt/diskN` path for `mincore`:** Unraid-coupled and brittle;
  `shfs`-specific path translation.
- **Throughput-rate threshold (bytes/sec):** adapts to any probe size but the
  threshold number is less intuitive and does not carry over from the script's
  proven `0.150s`.
- **Time the actual (variable-size) warm read:** couples warm and measure and makes
  a stable threshold harder; a fixed-size probe is cleaner.
- **Richer verify result type / config method override:** more interface surface
  and test work than the AC needs now (YAGNI). Can revisit if true per-file
  percentages on FUSE are later wanted.

## Architecture

```
Resident(path, off, len)
        |
        +- statfs(path).f_type == FUSE_SUPER_MAGIC ?
                 |
                 +- no  -> mincore (existing resident_linux.go logic)   [byte-granular]
                 +- yes -> probe(path, off): read min(probe_bytes, len), time it
                            +- classifyCached(elapsed, threshold) == true -> (len, true, nil)  [100%]
                            +- false -> log cold-probe elapsed_ms (diagnostic); (0, true, nil)  [0%]
```

### New components (all in `internal/pagecache`)

- **`classify.go`** (no build tag, pure, unit-tested on every platform):
  `classifyCached(elapsed, threshold time.Duration) bool` returns `elapsed < threshold`.
  This is the AC's "timing classifier with an injected clock (no real I/O)."
- **`probe.go`**: `probe(path string, offset, n int64, now func() time.Time) (time.Duration, error)`
  opens the file, seeks to `offset`, reads up to `n` bytes, and returns the
  `now()`-measured elapsed time. The clock is injected (`now func() time.Time`;
  production passes `time.Now`) so probe logic is testable with an injected
  reader + clock and no real disk.
- **FUSE detection** in `resident_linux.go`: `statfs(path)`, compare `f_type` to
  `0x65735546`, behind an injectable `statfsFunc` seam so tests can force either
  branch without a real FUSE mount. Result cached per-mount (keyed by mount/`f_fsid`)
  so repeated checks within a sweep do not re-`statfs`.
- **`resident_other.go`** (non-Linux) is unchanged - still `known=false`,
  warm-unconditionally.

### The shared mechanism

`probe()` is invoked in exactly one place - the FUSE branch of `residentImpl` -
and returns the raw `elapsed`. From that single value:

1. **Classification** -> `classifyCached` -> residency byte count, which the
   skip-optimization and `-verify` both consume via the unchanged `Cache.Resident`
   interface.
2. **Cold-latency diagnostic** -> when classified cold,
   `log.Debug("cold probe", "path", p, "offset", off, "elapsed_ms", elapsed.Milliseconds())`.
   Explicitly a proxy: the elapsed value conflates spin-up + seek + transfer, only
   includes spin-up when the disk happened to be asleep, and cannot be attributed
   to a specific backing spindle through `shfs`. It is observability only and is
   never fed back into skip/verify control logic. The clean spin-up figure still
   comes from the separate hands-on start-time test in `docs/phase1-verification.md`.

Warm stays a separate, larger operation; the probe's bytes happen to contribute to
the warm when an item turns out cold (the probe read warms its own sample).

## Config plumbing

New `[residency]` section in `config.toml` (separate from `[preload]` because it
governs *measurement*, not read sizing):

```toml
[residency]
probe_bytes     = 1048576   # 1 MiB fixed probe sample
probe_threshold = "150ms"   # cached if the probe returns faster than this
```

- Add a `ResidencyConfig` struct to `Config` with two fields: `ProbeBytes int64`
  (toml key `probe_bytes`) and `ProbeThreshold time.Duration` (toml key
  `probe_threshold`).
- Defaults in the existing defaults method: `ProbeBytes = 1<<20`,
  `ProbeThreshold = 150 * time.Millisecond`. Validation: both `> 0`.
- `pagecache.New()` changes from `New() Cache` to
  `New(probeBytes int64, threshold time.Duration) Cache`, stored on `osCache`.
  The callers that construct the cache (`cmd/preloadd`, plus the preloader/verify
  wiring) pass the config values. The pure classifier and probe receive these as
  parameters, not globals.

Note: this repo only ever *reads* config, and `BurntSushi/toml` parses a quoted
duration string (`"150ms"`) into `time.Duration` on decode, so the human-friendly
string form is correct. A config test asserts it decodes.

## Threshold semantics and edge cases

- The probe reads `min(probe_bytes, range_length)` from the range start (a range
  shorter than `probe_bytes`, e.g. a tiny tail, is probed in full).
- `classifyCached(elapsed, threshold) = elapsed < threshold`. Pure and total.
- A read **error** during probe -> `Resident` returns `(0, false, err)` (unknown
  -> caller warms unconditionally, matching today's semantics).
- A **short read** at EOF -> classify on whatever was read (a cached partial read
  is still fast).
- `range_length == 0` -> trivially resident `(0, true, nil)`, unchanged.

## Test plan (TDD)

- **`classify_test.go`** - table-driven, injected durations vs threshold (below /
  equal / above / zero). No real I/O. (The AC's named requirement.)
- **`probe_test.go`** - probe logic with an injected reader + injected clock: a
  fake reader whose `Read` advances the fake clock by a set amount; assert elapsed
  is measured correctly and that short-read / error paths behave. No real disk.
- **FUSE-dispatch test** - force the `statfsFunc` seam to the FUSE branch and assert
  it routes to probe-and-classify, returning `len` (cached) or `0` (cold) correctly,
  without a real FUSE mount.
- **Config test** - `[residency]` round-trips, defaults apply, `"150ms"` parses,
  zero/negative rejected.
- **No regression** - existing `pagecache_test.go` / `preloader_test.go` `mincore`
  paths stay green on a non-FUSE filesystem.

## Acceptance criteria (from #15)

- [ ] On `/mnt/user` (fuse.shfs), a second `-verify` pass reports the warmed items
      as already-cached (not 0%).
- [ ] Skip-optimization avoids re-charging budget for already-cached ranges on FUSE.
- [ ] Threshold is configurable; unit tests cover the timing classifier with an
      injected clock (no real I/O).
- [ ] Non-FUSE behavior unchanged (`mincore` fast path still used where valid).

### Manual acceptance (on `outatime`, after merge)

Two consecutive `-verify` passes on `/mnt/user`: the first warms (cold probes,
`preloaded > 0`); the second reports the same items cached (`skipped > 0`, mean
reads as ~100%), and the "`skipped=0` every run" behavior no longer occurs.

## Out of scope

- True per-file residency percentages on FUSE (would need a richer interface).
- Precise spin-up benchmarking (the cold-probe elapsed is a proxy only; the clean
  measurement is the separate hands-on start-time test).
- Non-Linux residency (unchanged; warms unconditionally).
