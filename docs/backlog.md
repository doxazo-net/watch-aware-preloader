# Backlog / future research

Items captured during design and build that are deliberately out of the current
phase's scope. Each should graduate to its own spec before implementation.

## B1: Per-drive spin-up profiling vs. worst-case buffer sizing

**Problem.** Phase 1 sizes each preload by a single global `target_seconds`
(default ~20s) on the assumption that spin-up is ~8-10s across the array. Real
arrays are heterogeneous: drives differ in spin-up latency (model, age, sleep
state, controller), so a fixed buffer can *under-cover* a slow disk (stutter
survives) while *over-covering* a fast one (wasted RAM, fewer warm entries).

**Goal of this task:** determine whether measuring per-drive spin-up is worth
the complexity, and if so which of two strategies to adopt:

- **(a) Worst-case buffer estimate.** Profile every array disk's spin-up once,
  take the maximum, and set `target_seconds` to cover the slowest disk. Simple,
  conservative, no per-file disk resolution needed. Cost: over-buffers files on
  faster disks, reducing how many entries fit in the RAM budget.
- **(b) Per-disk profiling.** Maintain a spin-up estimate per physical disk and
  size each file's buffer to the disk it actually lives on. RAM-optimal and
  precise. Cost: requires resolving each file to its physical disk and a place
  to store/refresh per-disk profiles.

**The hard part - file -> physical disk resolution.** Unraid's `/mnt/user` is a
FUSE union (`shfs`) that deliberately hides which `/mnt/diskN` (or pool/cache)
holds a given file. The media server reports `/mnt/user/...` (or, via a Docker
bind mount, an even more abstracted container path), so neither the API path nor
the user-share path reveals the backing disk. Strategy (b) needs a resolver:
- probe `/mnt/disk*/<share>/<relpath>` (and pool mounts) for the real file, or
- read shfs/extended-attribute hints if exposed, or
- query Unraid's own allocation metadata.
The native host daemon (Phase 1 already runs on the host) is the only component
that *can* resolve this; a containerized design could not. This is a concrete
reason the native-plugin architecture pays off here.

**Spin-up probing.** Measuring a disk's true spin-up means forcing it down
(`mdcmd spindown N` / `hdparm -y`) then timing a cold read - disruptive to do
automatically. Options: a one-time opt-in calibration during setup, or passive
estimation from observed cold-read latencies during normal operation (no forced
spin-down). Evaluate both for intrusiveness vs. accuracy.

**Deliverable:** a short spike/research note that (1) measures spin-up spread
across this server's disks, (2) prototypes the `/mnt/user` -> `/mnt/diskN`
resolver and reports its reliability, and (3) recommends (a) vs (b) with the
data. Feeds a follow-up spec if (b) is justified.

**Relates to:** the preloader's `HeadBytes` sizing (Phase 1 Task 6) and the path
mapper (Phase 1 Task 3).
