# FUSE-Proof Residency (Read-Timing Probe) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make page-cache residency detection work on Unraid's `fuse.shfs` (`/mnt/user`) by adding a read-timing probe that replaces `mincore` only where `mincore` is blind, so `-verify`'s cache-hit metric and the skip-optimization stop reporting `0%` / `skipped=0` on FUSE.

**Architecture:** One shared timed-probe primitive in `internal/pagecache`. `Cache.Resident` detects FUSE via `statfs` (`f_type == 0x65735546`); on FUSE it times a fixed-size probe read and classifies cached/cold (returning all-or-nothing byte counts through the unchanged interface), otherwise it uses the existing `mincore` path. The single probe call also emits a cold-latency diagnostic log. `mincore` stays the fast path on real disks; non-Linux is unchanged (warm unconditionally).

**Tech Stack:** Go 1.26+, `golang.org/x/sys/unix` (already a dep), `log/slog`, `github.com/BurntSushi/toml` (already a dep). No new dependencies.

## Global Constraints

- Go 1.26+, `net/http` stdlib, `log/slog` for logging; single static binary, no CGO.
- No new third-party dependencies.
- Pin GitHub Actions to SHAs; not relevant to this plan (no workflow changes).
- API keys / secrets never logged. (No secret handling in this plan.)
- `Cache.Resident` interface signature MUST NOT change.
- Defaults: `probe_bytes = 1048576` (1 MiB), `probe_threshold = 150ms`.
- FUSE magic constant: `0x65735546` (`FUSE_SUPER_MAGIC`).
- Lint the Linux build before pushing: `GOOS=linux golangci-lint run ./...`.
- Pure classifier + probe timing MUST be unit-tested with an injected clock and no real I/O.

---

### Task 1: Pure timing classifier

**Files:**
- Create: `internal/pagecache/classify.go`
- Test: `internal/pagecache/classify_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func classifyCached(elapsed, threshold time.Duration) bool` (package-private; used by the FUSE branch in Task 4).

- [ ] **Step 1: Write the failing test**

```go
package pagecache

import (
	"testing"
	"time"
)

func TestClassifyCached(t *testing.T) {
	const threshold = 150 * time.Millisecond
	cases := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{"well under threshold (RAM)", 2 * time.Millisecond, true},
		{"just under threshold", 149 * time.Millisecond, true},
		{"exactly at threshold is cold", threshold, false},
		{"over threshold (cold disk)", 800 * time.Millisecond, false},
		{"zero elapsed is cached", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyCached(tc.elapsed, threshold); got != tc.want {
				t.Errorf("classifyCached(%v, %v) = %v, want %v", tc.elapsed, threshold, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagecache/ -run TestClassifyCached -v`
Expected: FAIL - `undefined: classifyCached`.

- [ ] **Step 3: Write minimal implementation**

```go
package pagecache

import "time"

// classifyCached reports whether a probe read that took elapsed indicates the
// range was already page-cache resident. A read served from RAM returns far
// faster than the threshold; a read that touched a physical (possibly
// spun-down) disk returns slower. The threshold detects this categorical
// difference, so it need not scale with probe size.
func classifyCached(elapsed, threshold time.Duration) bool {
	return elapsed < threshold
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pagecache/ -run TestClassifyCached -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pagecache/classify.go internal/pagecache/classify_test.go
git commit -m "feat(pagecache): pure read-timing classifier"
```

---

### Task 2: Timed-read probe primitive

**Files:**
- Create: `internal/pagecache/probe.go`
- Test: `internal/pagecache/probe_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func timedRead(r io.Reader, n int64, now func() time.Time) (time.Duration, error)` - reads up to `n` bytes from `r`, timing the read with the injected clock. Stops at `n` bytes or EOF; a read error (other than EOF) returns `(0, err)`.
  - `func (c *osCache) probePath(path string, offset, n int64) (time.Duration, error)` - opens `path`, seeks to `offset`, and calls `timedRead` with `c.now`. (`osCache` and its `now` field are added in Task 4; this method references them, so it is added here but only compiles once Task 4's struct fields exist. To keep this task's build green, put `probePath` in Task 4 instead and ship only `timedRead` here.)

> Implementation note: ship `timedRead` (pure, fully testable) in this task. `probePath` (the file-opening wrapper that needs `osCache.now`) is added in Task 4 alongside the struct field it depends on. This keeps every commit compiling.

- [ ] **Step 1: Write the failing test**

```go
package pagecache

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// advancingReader advances *clk by perRead on every Read call, simulating I/O
// latency without touching a real disk.
type advancingReader struct {
	data    []byte
	pos     int
	clk     *time.Time
	perRead time.Duration
	err     error // returned (after data exhausted) when set
}

func (r *advancingReader) Read(p []byte) (int, error) {
	*r.clk = r.clk.Add(r.perRead)
	if r.err != nil && r.pos >= len(r.data) {
		return 0, r.err
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestTimedReadMeasuresElapsed(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	r := &advancingReader{data: bytes.Repeat([]byte{0xAB}, 4096), clk: &clk, perRead: 10 * time.Millisecond}

	elapsed, err := timedRead(r, 4096, now)
	if err != nil {
		t.Fatalf("timedRead: %v", err)
	}
	if elapsed != 10*time.Millisecond {
		t.Errorf("elapsed = %v, want 10ms (one Read of the whole buffer)", elapsed)
	}
}

func TestTimedReadStopsAtN(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	// 1 MiB available, but only probe 4096 bytes: one Read suffices.
	r := &advancingReader{data: bytes.Repeat([]byte{1}, 1<<20), clk: &clk, perRead: 5 * time.Millisecond}

	elapsed, err := timedRead(r, 4096, now)
	if err != nil {
		t.Fatalf("timedRead: %v", err)
	}
	if elapsed != 5*time.Millisecond {
		t.Errorf("elapsed = %v, want 5ms (stopped at n=4096 after one Read)", elapsed)
	}
}

func TestTimedReadShortReadAtEOF(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	// Ask for 4096 but only 100 bytes exist; must classify on what was read.
	r := &advancingReader{data: bytes.Repeat([]byte{1}, 100), clk: &clk, perRead: 3 * time.Millisecond}

	elapsed, err := timedRead(r, 4096, now)
	if err != nil {
		t.Fatalf("timedRead at EOF should not error, got %v", err)
	}
	if elapsed < 3*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 3ms", elapsed)
	}
}

func TestTimedReadPropagatesError(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	wantErr := errors.New("boom")
	r := &advancingReader{data: nil, clk: &clk, perRead: time.Millisecond, err: wantErr}

	if _, err := timedRead(r, 4096, now); !errors.Is(err, wantErr) {
		t.Errorf("timedRead error = %v, want %v", err, wantErr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagecache/ -run TestTimedRead -v`
Expected: FAIL - `undefined: timedRead`.

- [ ] **Step 3: Write minimal implementation**

```go
package pagecache

import (
	"errors"
	"io"
	"time"
)

// timedRead reads up to n bytes from r and returns the wall-clock duration of
// the read, measured with the injected now clock. It stops at n bytes or EOF;
// a short read at EOF is not an error (a cached partial read is still fast).
// A non-EOF read error returns (0, err).
func timedRead(r io.Reader, n int64, now func() time.Time) (time.Duration, error) {
	start := now()
	const chunk = 64 << 10 // 64 KiB
	buf := make([]byte, chunk)
	var read int64
	for read < n {
		want := n - read
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		m, err := r.Read(buf[:want])
		read += int64(m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		if m == 0 {
			break
		}
	}
	return now().Sub(start), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pagecache/ -run TestTimedRead -v`
Expected: PASS (all four sub-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/pagecache/probe.go internal/pagecache/probe_test.go
git commit -m "feat(pagecache): injected-clock timed-read primitive"
```

---

### Task 3: Residency config (`[residency]` section)

**Files:**
- Modify: `internal/config/config.go` (add struct, defaults, validation)
- Test: `internal/config/config_test.go` (add decode/default/validate cases)

**Interfaces:**
- Consumes: nothing.
- Produces: `Config.Residency` of type `ResidencyConfig{ ProbeBytes int64; ProbeThreshold time.Duration }` with toml keys `probe_bytes` and `probe_threshold`, defaulted to `1<<20` and `150ms`, both validated `> 0`. Task 4/5 read `cfg.Residency.ProbeBytes` and `cfg.Residency.ProbeThreshold`.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestResidencyDefaults(t *testing.T) {
	c := &Config{}
	c.applyDefaults()
	if c.Residency.ProbeBytes != 1<<20 {
		t.Errorf("ProbeBytes default = %d, want %d", c.Residency.ProbeBytes, 1<<20)
	}
	if c.Residency.ProbeThreshold != 150*time.Millisecond {
		t.Errorf("ProbeThreshold default = %v, want 150ms", c.Residency.ProbeThreshold)
	}
}

func TestResidencyDecodesDurationString(t *testing.T) {
	const data = `
[server]
type = "emby"
url = "http://localhost:8096"
api_key = "x"

[residency]
probe_bytes = 2097152
probe_threshold = "200ms"
`
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Residency.ProbeBytes != 2<<20 {
		t.Errorf("ProbeBytes = %d, want %d", c.Residency.ProbeBytes, 2<<20)
	}
	if c.Residency.ProbeThreshold != 200*time.Millisecond {
		t.Errorf("ProbeThreshold = %v, want 200ms", c.Residency.ProbeThreshold)
	}
}

func TestResidencyRejectsNonPositive(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Config)
	}{
		{"probe_bytes <= 0", func(c *Config) { c.Residency.ProbeBytes = 0; c.Residency.ProbeBytes = -1 }},
		{"probe_threshold <= 0", func(c *Config) { c.Residency.ProbeThreshold = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validBase()
			tc.mut(c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected validation error for %s", tc.name)
			}
		})
	}
}
```

> Note: `validBase()` builds a `Config` that passes `Validate`. After Step 3 it must also set valid `Residency` values; update `validBase()` accordingly (see Step 3). Add `"time"` and (if missing) `"os"`/`"path/filepath"` imports to the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestResidency -v`
Expected: FAIL - `c.Residency undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, add the import and struct:

```go
import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// ResidencyConfig controls the read-timing probe used to detect page-cache
// residency on filesystems where mincore cannot (e.g. Unraid fuse.shfs).
type ResidencyConfig struct {
	ProbeBytes     int64         `toml:"probe_bytes"`     // fixed probe sample size
	ProbeThreshold time.Duration `toml:"probe_threshold"` // cached iff a probe read returns faster than this
}
```

Add the field to `Config`:

```go
type Config struct {
	Server   ServerConfig    `toml:"server"`
	Users    UsersConfig     `toml:"users"`
	Preload  PreloadConfig   `toml:"preload"`
	PathMap  []PathRule      `toml:"path_map"`
	Schedule ScheduleConfig  `toml:"schedule"`
	Residency ResidencyConfig `toml:"residency"`
}
```

Add to `applyDefaults()`:

```go
	if c.Residency.ProbeBytes == 0 {
		c.Residency.ProbeBytes = 1 << 20
	}
	if c.Residency.ProbeThreshold == 0 {
		c.Residency.ProbeThreshold = 150 * time.Millisecond
	}
```

Add to `Validate()` (before the final `return nil`):

```go
	if c.Residency.ProbeBytes <= 0 {
		return fmt.Errorf("residency.probe_bytes must be > 0, got %d", c.Residency.ProbeBytes)
	}
	if c.Residency.ProbeThreshold <= 0 {
		return fmt.Errorf("residency.probe_threshold must be > 0, got %v", c.Residency.ProbeThreshold)
	}
```

In `internal/config/config_test.go`, update `validBase()` so it passes the new validation (add after the existing field assignments, before `return c`):

```go
	c.Residency.ProbeBytes = 1 << 20
	c.Residency.ProbeThreshold = 150 * time.Millisecond
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (new `TestResidency*` plus all existing config tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): residency probe_bytes and probe_threshold settings"
```

---

### Task 4: FUSE-aware `Resident` (statfs dispatch + probe + cold diagnostic)

**Files:**
- Modify: `internal/pagecache/pagecache.go` (`New` signature, `osCache` fields, `Method`, `probePath`)
- Modify: `internal/pagecache/resident_linux.go` (split mincore body out; add FUSE dispatch, `isFUSE`, `residentByTiming`, `defaultStatfs`, `fuseSuperMagic`, `residencyMethod`)
- Modify: `internal/pagecache/resident_other.go` (`defaultStatfs`, `residencyMethod` stubs)
- Modify: `internal/pagecache/pagecache_test.go` (update two `New()` call sites)
- Modify: `cmd/preloadd/main.go` (update two `pagecache.New()` call sites)
- Test: `internal/pagecache/resident_dispatch_test.go` (new; FUSE-branch dispatch via injected statfs + clock)

**Interfaces:**
- Consumes: `classifyCached` (Task 1), `timedRead` (Task 2), `cfg.Residency.*` (Task 3).
- Produces:
  - `func New(probeBytes int64, threshold time.Duration, log *slog.Logger) Cache` (signature change from `New()`).
  - `osCache` fields: `probeBytes int64`, `threshold time.Duration`, `log *slog.Logger`, `now func() time.Time`, `statfs func(path string) (uint32, error)`, plus an `isFUSE` per-mount cache.
  - `func (c *osCache) Method(path string) string` returning `"mincore"`, `"timing"`, or `"unavailable"` (consumed in Task 5 via the `Methoder` optional interface).
  - `Methoder` interface: `interface{ Method(path string) string }`.

- [ ] **Step 1: Write the failing dispatch test**

Create `internal/pagecache/resident_dispatch_test.go`:

```go
package pagecache

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestCache builds an osCache with injected statfs + clock so the FUSE
// branch can be exercised without a real fuse.shfs mount or real disk latency.
func newTestCache(t *testing.T, fuse bool, readLatency time.Duration) *osCache {
	t.Helper()
	clk := time.Unix(0, 0)
	c := &osCache{
		probeBytes: 1 << 20,
		threshold:  150 * time.Millisecond,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:        func() time.Time { return clk },
		statfs: func(string) (uint32, error) {
			if fuse {
				return fuseSuperMagic, nil
			}
			return 0xEF53 /* ext2/3/4 */, nil
		},
	}
	// Advance the injected clock by readLatency on each probe read by wrapping now.
	// Simpler: precompute elapsed by making now jump once per Resident call.
	c.now = advanceOnceClock(&clk, readLatency)
	return c
}

// advanceOnceClock returns a now() that, the first time it is called after a
// reset, returns the base time, and on the next call returns base+latency, so a
// start/end pair around a probe measures exactly latency.
func advanceOnceClock(clk *time.Time, latency time.Duration) func() time.Time {
	calls := 0
	base := *clk
	return func() time.Time {
		calls++
		if calls == 1 {
			return base
		}
		return base.Add(latency)
	}
}

func writeFile(t *testing.T, dir string, size int) string {
	t.Helper()
	p := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResidentFUSEFastProbeReportsCached(t *testing.T) {
	p := writeFile(t, t.TempDir(), 4<<20)
	c := newTestCache(t, true /*fuse*/, 2*time.Millisecond /*fast => cached*/)
	got, known, err := c.Resident(p, 0, 1<<20)
	if err != nil || !known {
		t.Fatalf("Resident: err=%v known=%v", err, known)
	}
	if got != 1<<20 {
		t.Errorf("resident = %d, want %d (fast probe => fully cached)", got, 1<<20)
	}
}

func TestResidentFUSESlowProbeReportsCold(t *testing.T) {
	p := writeFile(t, t.TempDir(), 4<<20)
	c := newTestCache(t, true /*fuse*/, 800*time.Millisecond /*slow => cold*/)
	got, known, err := c.Resident(p, 0, 1<<20)
	if err != nil || !known {
		t.Fatalf("Resident: err=%v known=%v", err, known)
	}
	if got != 0 {
		t.Errorf("resident = %d, want 0 (slow probe => cold)", got)
	}
}

func TestMethodReflectsFilesystem(t *testing.T) {
	p := writeFile(t, t.TempDir(), 1<<20)
	if m := newTestCache(t, true, time.Millisecond).Method(p); m != "timing" {
		t.Errorf("Method on FUSE = %q, want timing", m)
	}
	if m := newTestCache(t, false, time.Millisecond).Method(p); m != "mincore" {
		t.Errorf("Method on non-FUSE = %q, want mincore", m)
	}
}
```

> This test file is `//go:build linux`-free intentionally? No - it references `fuseSuperMagic` and the FUSE branch, which are Linux-only. Add `//go:build linux` as the first line so it only runs on Linux (the CI Go job runs on Linux). On non-Linux dev machines these dispatch tests are skipped; the pure Task 1/2 tests still cover the classifier and timer everywhere.

Add `//go:build linux` as the first line of `resident_dispatch_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOOS=linux go vet ./internal/pagecache/` then on a Linux host `go test ./internal/pagecache/ -run 'TestResidentFUSE|TestMethod' -v`
Expected: FAIL to compile - `osCache` has no fields `probeBytes/threshold/log/now/statfs`, `fuseSuperMagic` undefined, `Method` undefined.

- [ ] **Step 3a: Update `osCache` and `New` in `pagecache.go`**

```go
import (
	"errors"
	"io"
	"log/slog"
	"os"
	"time"
)

// New returns the platform Cache implementation. probeBytes and threshold tune
// the read-timing residency probe used on filesystems where mincore is blind
// (e.g. fuse.shfs); log receives the cold-probe latency diagnostic.
func New(probeBytes int64, threshold time.Duration, log *slog.Logger) Cache {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &osCache{
		probeBytes: probeBytes,
		threshold:  threshold,
		log:        log,
		now:        time.Now,
		statfs:     defaultStatfs,
	}
}

type osCache struct {
	probeBytes int64
	threshold  time.Duration
	log        *slog.Logger
	now        func() time.Time
	statfs     func(path string) (uint32, error)

	fuseMu    sync.Mutex
	fuseCache map[string]bool // keyed by containing directory
}
```

Add `"sync"` to the import block. Add the `Method` method, `probePath`, and the `Methoder` interface:

```go
// Methoder optionally reports which residency mechanism a Cache uses for a path.
type Methoder interface {
	Method(path string) string
}

// Method reports the residency mechanism for path: "mincore", "timing" (FUSE),
// or "unavailable" (no residency support on this platform).
func (c *osCache) Method(path string) string { return residencyMethod(c, path) }

// probePath opens path, seeks to offset, and times a read of up to n bytes.
func (c *osCache) probePath(path string, offset, n int64) (time.Duration, error) {
	f, err := os.Open(path) //nolint:gosec // operator-configured media path
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return timedRead(f, n, c.now)
}
```

(`Warm` is unchanged. `Resident` still delegates to `residentImpl`, whose signature changes below.)

Change `Resident` to pass the cache:

```go
func (c *osCache) Resident(path string, offset, length int64) (int64, bool, error) {
	return residentImpl(c, path, offset, length)
}
```

- [ ] **Step 3b: Rework `resident_linux.go`**

Replace the file with FUSE dispatch plus the existing mincore body moved into `residentByMincore`:

```go
//go:build linux

package pagecache

import (
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

const fuseSuperMagic = 0x65735546 // FUSE_SUPER_MAGIC

// defaultStatfs returns the filesystem type (f_type) for path.
func defaultStatfs(path string) (uint32, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint32(st.Type), nil
}

// isFUSE reports whether path lives on a FUSE filesystem, caching the answer per
// containing directory to avoid repeated statfs within a sweep.
func (c *osCache) isFUSE(path string) (bool, error) {
	key := filepath.Dir(path)
	c.fuseMu.Lock()
	if c.fuseCache == nil {
		c.fuseCache = make(map[string]bool)
	}
	if v, ok := c.fuseCache[key]; ok {
		c.fuseMu.Unlock()
		return v, nil
	}
	c.fuseMu.Unlock()

	ft, err := c.statfs(path)
	if err != nil {
		return false, err
	}
	fuse := ft == fuseSuperMagic
	c.fuseMu.Lock()
	c.fuseCache[key] = fuse
	c.fuseMu.Unlock()
	return fuse, nil
}

// residencyMethod reports the mechanism Resident uses for path on Linux.
func residencyMethod(c *osCache, path string) string {
	fuse, err := c.isFUSE(path)
	if err != nil {
		return "mincore" // statfs failed; mincore is the default attempt
	}
	if fuse {
		return "timing"
	}
	return "mincore"
}

// residentImpl dispatches to mincore on ordinary filesystems and to a
// read-timing probe on FUSE, where mincore reports all-zero residency.
func residentImpl(c *osCache, path string, offset, length int64) (int64, bool, error) {
	if length <= 0 {
		return 0, true, nil
	}
	fuse, err := c.isFUSE(path)
	if err != nil {
		return 0, false, err
	}
	if fuse {
		return c.residentByTiming(path, offset, length)
	}
	return residentByMincore(path, offset, length)
}

// residentByTiming probes a fixed sample and classifies the whole range as fully
// resident (length) or cold (0). A cold classification logs a latency
// diagnostic; the elapsed value is a proxy (spin-up+seek+transfer) and never
// feeds skip/verify control logic.
func (c *osCache) residentByTiming(path string, offset, length int64) (int64, bool, error) {
	n := c.probeBytes
	if n > length {
		n = length
	}
	elapsed, err := c.probePath(path, offset, n)
	if err != nil {
		return 0, false, err
	}
	if classifyCached(elapsed, c.threshold) {
		return length, true, nil
	}
	c.log.Debug("cold probe", "path", path, "offset", offset, "elapsed_ms", elapsed.Milliseconds())
	return 0, true, nil
}

// residentByMincore mmaps the requested range and asks mincore which pages are
// resident, returning the resident byte count.
func residentByMincore(path string, offset, length int64) (int64, bool, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator-configured media, opening it is this package's purpose
	if err != nil {
		return 0, false, err
	}
	defer f.Close() //nolint:errcheck // read-only; close error not actionable

	fi, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	if offset >= fi.Size() {
		return 0, true, nil
	}
	if offset+length > fi.Size() {
		length = fi.Size() - offset
	}

	pageSize := int64(os.Getpagesize())
	alignedOff := offset - (offset % pageSize)
	mmapLen := length + (offset - alignedOff)

	data, err := unix.Mmap(int(f.Fd()), alignedOff, int(mmapLen),
		unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = unix.Munmap(data) }()

	pages := (mmapLen + pageSize - 1) / pageSize
	vec := make([]byte, pages)
	_, _, errno := unix.Syscall(unix.SYS_MINCORE,
		uintptr(unsafe.Pointer(&data[0])), //nolint:gosec // G103: audited mincore syscall; mincore requires the mapped region address
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&vec[0]))) //nolint:gosec // G103: audited mincore syscall; kernel writes residency bits into vec
	if errno != 0 {
		return 0, false, errno
	}

	requestEnd := offset + length
	var resident int64
	for i, v := range vec {
		if v&0x1 == 0 {
			continue
		}
		pageStart := alignedOff + int64(i)*pageSize
		pageEnd := pageStart + pageSize
		if pageStart < offset {
			pageStart = offset
		}
		if pageEnd > requestEnd {
			pageEnd = requestEnd
		}
		if pageStart < pageEnd {
			resident += pageEnd - pageStart
		}
	}
	return resident, true, nil
}
```

- [ ] **Step 3c: Update `resident_other.go` (non-Linux stubs)**

```go
//go:build !linux

package pagecache

// residentImpl cannot determine residency off Linux; callers warm unconditionally.
func residentImpl(_ *osCache, _ string, _, _ int64) (int64, bool, error) {
	return 0, false, nil
}

// defaultStatfs is unused off Linux but keeps New() platform-neutral.
func defaultStatfs(_ string) (uint32, error) { return 0, nil }

// residencyMethod reports that residency is unavailable off Linux.
func residencyMethod(_ *osCache, _ string) string { return "unavailable" }
```

- [ ] **Step 3d: Fix the two `New()` callers in `pagecache_test.go`**

Change both `c := New()` to:

```go
c := New(1<<20, 150*time.Millisecond, nil)
```

Add `"time"` to that test file's imports.

- [ ] **Step 3e: Fix the two `pagecache.New()` callers in `cmd/preloadd/main.go`**

Both call sites (the preloader construction and the `app.ReportResidency` call) become:

```go
pagecache.New(cfg.Residency.ProbeBytes, cfg.Residency.ProbeThreshold, log)
```

- [ ] **Step 4: Run the full package + build**

Run (on Linux, matching CI): `go test ./... && GOOS=linux go build ./...`
Expected: PASS; the new dispatch tests pass on Linux, all existing tests stay green, binary builds.
On a non-Linux dev host: `go test ./...` passes (dispatch tests skip via build tag) and `GOOS=linux go vet ./...` is clean.

- [ ] **Step 5: Commit**

```bash
git add internal/pagecache/ cmd/preloadd/main.go
git commit -m "feat(pagecache): FUSE-aware Resident via statfs dispatch + read-timing probe (#15)"
```

---

### Task 5: Label the residency method in `-verify` output

**Files:**
- Modify: `internal/app/verify.go` (per-range log includes `method`)
- Test: `internal/app/verify_test.go` (new or extend; assert method surfaced via `Methoder`)

**Interfaces:**
- Consumes: `pagecache.Methoder` (Task 4).
- Produces: no new exported symbols; `ReportResidency` enriches its per-range `residency` log with a `method` attribute when the cache implements `Methoder`.

- [ ] **Step 1: Write the failing test**

Create/extend `internal/app/verify_test.go`:

```go
package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
)

// methoderCache reports a fixed residency byte count and a fixed method.
type methoderCache struct {
	resident int64
	method   string
}

func (m methoderCache) Warm(string, int64, int64) error { return nil }
func (m methoderCache) Resident(_ string, _, length int64) (int64, bool, error) {
	return m.resident, true, nil
}
func (m methoderCache) Method(string) string { return m.method }

func TestReportResidencyLogsMethod(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	cache := methoderCache{resident: 1 << 20, method: "timing"}
	warmed := []preloader.WarmedRange{{Path: "/mnt/user/x.mkv", Offset: 0, Length: 1 << 20}}

	mean, known := ReportResidency(cache, warmed, log)
	if !known || mean != 100 {
		t.Fatalf("mean=%v known=%v, want 100 true", mean, known)
	}
	if !strings.Contains(buf.String(), "method=timing") {
		t.Errorf("residency log missing method=timing:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestReportResidencyLogsMethod -v`
Expected: FAIL - log lacks `method=timing`.

- [ ] **Step 3: Write minimal implementation**

In `internal/app/verify.go`, import `pagecache` is already present. In `ReportResidency`, replace the per-range success log:

```go
		method := "mincore"
		if m, ok := cache.(pagecache.Methoder); ok {
			method = m.Method(r.Path)
		}
		log.Info("residency", "path", r.Path, "percent", pct, "method", method)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -v`
Expected: PASS (new test plus existing app tests).

- [ ] **Step 5: Commit**

```bash
git add internal/app/verify.go internal/app/verify_test.go
git commit -m "feat(verify): label residency mechanism (mincore/timing) per range"
```

---

### Task 6: Docs + config sample + full gate

**Files:**
- Modify: `docs/phase1-verification.md` (note FUSE residency now works; how to read `method=timing`)
- Modify: a config sample if one exists (e.g. `config.example.toml`); otherwise skip
- Verify: whole repo gate

- [ ] **Step 1: Update the verification doc**

In `docs/phase1-verification.md`, add a short subsection under the residency/`-verify` discussion:

```markdown
### FUSE residency (/mnt/user)

On `fuse.shfs`, `mincore` is blind, so `preloadd` falls back to a read-timing
probe (configurable via `[residency] probe_bytes` / `probe_threshold`). On FUSE,
per-file residency is all-or-nothing: `100%` (probe served from RAM, fast) or
`0%` (probe touched disk, slow), and the per-range log shows `method=timing`.
A second `-verify` pass after a warm should report the warmed items cached
(`skipped > 0`), confirming the warm landed in the shared page cache.
```

- [ ] **Step 2: Update the config sample (only if one exists)**

If `config.example.toml` (or similar) exists, add:

```toml
[residency]
probe_bytes     = 1048576   # 1 MiB probe sample for FUSE read-timing
probe_threshold = "150ms"   # faster => already cached; slower => warmed from disk
```

If no sample config file exists in the repo, skip this step (do not create one in this plan).

- [ ] **Step 3: Run the full gate**

Run:
```bash
make fmt && make test && make test-race
GOOS=linux golangci-lint run ./...
```
Expected: all green; race detector clean; Linux lint clean.

- [ ] **Step 4: Commit**

```bash
git add docs/phase1-verification.md config.example.toml 2>/dev/null; git add docs/phase1-verification.md
git commit -m "docs: document FUSE read-timing residency and [residency] config"
```

---

## Self-Review

**Spec coverage:**
- Detection via statfs FUSE magic -> Task 4 (`isFUSE`, `fuseSuperMagic`, `defaultStatfs`). ✓
- Fixed-size probe + time threshold, pure classifier with injected clock -> Tasks 1, 2. ✓
- `Cache.Resident` interface unchanged; all-or-nothing on FUSE -> Task 4 (`residentByTiming` returns length/0). ✓
- Config `probe_bytes` / `probe_threshold`, defaults, validation, duration-string decode -> Task 3. ✓
- `New()` plumbed into both call sites -> Task 4 (Steps 3d/3e). ✓
- Single shared mechanism feeding classification + cold-latency diagnostic -> Task 4 (`residentByTiming`). ✓
- `-verify` labels `method=timing` -> Task 5. ✓
- mincore fast path unchanged on non-FUSE -> Task 4 (`residentByMincore` is the moved original body; dispatch only diverts FUSE). ✓
- Non-Linux unchanged -> Task 4 Step 3c (`residentImpl` stub still `known=false`). ✓
- Docs / manual acceptance -> Task 6. ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code. Task 6 Step 2 is conditional ("only if a sample exists") with an explicit skip rule, not a placeholder.

**Type consistency:** `classifyCached(elapsed, threshold time.Duration) bool` (Tasks 1, 4); `timedRead(io.Reader, int64, func() time.Time) (time.Duration, error)` (Tasks 2, 4); `New(int64, time.Duration, *slog.Logger) Cache` (Task 4, callers 3d/3e); `residentImpl(*osCache, string, int64, int64)` (linux + other stubs match); `Method(path string) string` / `Methoder` (Tasks 4, 5). Consistent.

> Deviation from spec noted: `New` takes a third `*slog.Logger` parameter (not in the spec's two-arg sketch) because the agreed cold-probe diagnostic must log. This is a necessary refinement; interface `Cache` is still unchanged.
