# Phase 1 - Engine MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go daemon (`preloadd`) that reads Emby watch state and warms the Linux page cache with the media each household user is most likely to play next, so playback starts without waiting for an array disk to spin up.

**Architecture:** Five decoupled units inside one static binary - an Emby client (fetch watch signals), a pure scorer (rank/dedupe into a tiered preload list), a path mapper (server path -> host path), a preloader (duration-based reads into page cache), and config. `cmd/preloadd` wires them into a loop: a periodic full sweep plus a fast `/Sessions` poll that triggers an immediate sweep on playback-state changes. The pipeline is idempotent (already-resident ranges are skipped), so triggered re-runs are cheap.

**Tech Stack:** Go 1.26, stdlib `net/http`, `log/slog`. Two deps: `github.com/BurntSushi/toml` (config), `golang.org/x/sys/unix` (Linux `mincore`). No CGO except the race detector in tests.

## Global Constraints

- Go 1.26+; single static binary, `CGO_ENABLED=0` for release builds.
- License MIT; do NOT copy GPL stillwater source verbatim - reimplement patterns.
- API keys are secrets: never log them; config files (`config.toml`, `*.local.toml`) are gitignored.
- Emby auth via the `X-Emby-Token` request header.
- Tier ordering (lower = higher priority): Resume(0), NextUp(1), RecentlyAdded(2), BingeAhead(3, reserved for Phase 3), BestEffort(4).
- Exclude any item in an active playback session (`/Sessions` `NowPlayingItem`) from all tiers.
- Duration-based head sizing: `headBytes = clamp(targetSeconds * bitrateBps/8, minHeadBytes, maxHeadBytes)`; fall back to `sizeBytes/runtimeSeconds` for bitrate when `BitrateBps == 0`.
- Cache warming must be portable (pread); residency detection (`mincore`) is Linux-only behind an interface with a portable "unknown" fallback.
- Every `//nolint` carries a `// reason` (nolintlint). TDD: test first, watch it fail, implement, watch it pass, commit.

---

### Task 1: Core domain types

**Files:**
- Create: `internal/core/core.go`
- Test: `internal/core/core_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Tier int` with consts `TierResume, TierNextUp, TierRecentlyAdded, TierBingeAhead, TierBestEffort` and `func (Tier) String() string`.
  - `type MediaItem struct { ID, Name, ServerPath string; BitrateBps, SizeBytes int64; Runtime, ResumeOffset time.Duration; UserID string }`
  - `type PreloadTarget struct { Item MediaItem; Tier Tier }`

- [ ] **Step 1: Write the failing test**

```go
package core

import "testing"

func TestTierString(t *testing.T) {
	cases := map[Tier]string{
		TierResume:        "resume",
		TierNextUp:        "next-up",
		TierRecentlyAdded: "recently-added",
		TierBingeAhead:    "binge-ahead",
		TierBestEffort:    "best-effort",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", int(tier), got, want)
		}
	}
}

func TestTierOrdering(t *testing.T) {
	// Lower value = higher priority; ordering must be stable for the scorer.
	if !(TierResume < TierNextUp && TierNextUp < TierRecentlyAdded &&
		TierRecentlyAdded < TierBingeAhead && TierBingeAhead < TierBestEffort) {
		t.Fatal("tier constants are not in ascending priority order")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestTier -v`
Expected: FAIL - `undefined: Tier`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package core holds the domain types shared across the preloader units.
package core

import "time"

// Tier is the preload priority class; a lower value is higher priority.
type Tier int

const (
	TierResume        Tier = iota // recent incompletes, not currently playing
	TierNextUp                    // next episode of an active series
	TierRecentlyAdded             // recently added, unwatched
	TierBingeAhead                // episode after next-up (reserved; Phase 3)
	TierBestEffort                // filesystem-recency fill
)

func (t Tier) String() string {
	switch t {
	case TierResume:
		return "resume"
	case TierNextUp:
		return "next-up"
	case TierRecentlyAdded:
		return "recently-added"
	case TierBingeAhead:
		return "binge-ahead"
	case TierBestEffort:
		return "best-effort"
	default:
		return "unknown"
	}
}

// MediaItem is a normalized media file surfaced by the media server.
type MediaItem struct {
	ID           string
	Name         string
	ServerPath   string        // path as the media server reports it
	BitrateBps   int64         // average bits per second; 0 if unknown
	SizeBytes    int64         // file size in bytes
	Runtime      time.Duration // total playback duration
	ResumeOffset time.Duration // playback position for resume items; 0 otherwise
	UserID       string        // the user account that surfaced this item
}

// PreloadTarget is a scored, ordered item ready to preload.
type PreloadTarget struct {
	Item core.MediaItem //nolint:revive // placeholder replaced below
	Tier Tier
}
```

Note: the `PreloadTarget.Item` field type is `MediaItem` (same package); write it as `Item MediaItem`. (The `core.MediaItem` above is a deliberate typo to catch in review - replace with `MediaItem`.)

- [ ] **Step 4: Fix the field type and run tests**

Replace the `PreloadTarget` struct with:

```go
// PreloadTarget is a scored, ordered item ready to preload.
type PreloadTarget struct {
	Item MediaItem
	Tier Tier
}
```

Run: `go test ./internal/core/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/
git commit -m "feat(core): add Tier, MediaItem, PreloadTarget domain types"
```

---

### Task 2: Scorer

**Files:**
- Create: `internal/scorer/scorer.go`
- Test: `internal/scorer/scorer_test.go`

**Interfaces:**
- Consumes: `core.MediaItem`, `core.Tier`, `core.PreloadTarget`.
- Produces:
  - `type Candidate struct { Item core.MediaItem; Tier core.Tier }`
  - `func Rank(candidates []Candidate, nowPlaying map[string]bool) []core.PreloadTarget`
  - Behavior: drop items whose `ID` is in `nowPlaying`; dedupe by `ID` keeping the lowest (highest-priority) tier; sort by tier ascending, then resume items with a larger `ResumeOffset` first, otherwise stable input order.

- [ ] **Step 1: Write the failing test**

```go
package scorer

import (
	"testing"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
)

func item(id string, off time.Duration) core.MediaItem {
	return core.MediaItem{ID: id, ResumeOffset: off}
}

func TestRankExcludesNowPlaying(t *testing.T) {
	cands := []Candidate{
		{Item: item("a", 0), Tier: core.TierNextUp},
		{Item: item("b", 0), Tier: core.TierResume},
	}
	got := Rank(cands, map[string]bool{"b": true})
	if len(got) != 1 || got[0].Item.ID != "a" {
		t.Fatalf("expected only 'a', got %+v", got)
	}
}

func TestRankDedupesKeepingHighestPriority(t *testing.T) {
	cands := []Candidate{
		{Item: item("a", 0), Tier: core.TierRecentlyAdded},
		{Item: item("a", 0), Tier: core.TierResume}, // higher priority wins
	}
	got := Rank(cands, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped target, got %d", len(got))
	}
	if got[0].Tier != core.TierResume {
		t.Fatalf("expected TierResume to win dedupe, got %v", got[0].Tier)
	}
}

func TestRankOrdersByTierThenResumeOffset(t *testing.T) {
	cands := []Candidate{
		{Item: item("added", 0), Tier: core.TierRecentlyAdded},
		{Item: item("r-small", 1*time.Minute), Tier: core.TierResume},
		{Item: item("r-big", 30*time.Minute), Tier: core.TierResume},
		{Item: item("next", 0), Tier: core.TierNextUp},
	}
	got := Rank(cands, nil)
	wantOrder := []string{"r-big", "r-small", "next", "added"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d targets, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].Item.ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].Item.ID, id)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scorer/ -v`
Expected: FAIL - `undefined: Rank`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package scorer turns per-user watch signals into a ranked, deduped preload list.
package scorer

import (
	"sort"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
)

// Candidate is one item proposed for preloading at a given tier.
type Candidate struct {
	Item core.MediaItem
	Tier core.Tier
}

// Rank filters out actively-playing items, dedupes by item ID (keeping the
// highest-priority tier), and orders the result by tier then resume depth.
func Rank(candidates []Candidate, nowPlaying map[string]bool) []core.PreloadTarget {
	best := make(map[string]Candidate, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if nowPlaying[c.Item.ID] {
			continue
		}
		existing, seen := best[c.Item.ID]
		if !seen {
			best[c.Item.ID] = c
			order = append(order, c.Item.ID)
			continue
		}
		if c.Tier < existing.Tier {
			best[c.Item.ID] = c
		}
	}

	targets := make([]core.PreloadTarget, 0, len(order))
	for _, id := range order {
		c := best[id]
		targets = append(targets, core.PreloadTarget{Item: c.Item, Tier: c.Tier})
	}

	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Tier != targets[j].Tier {
			return targets[i].Tier < targets[j].Tier
		}
		// Within the resume tier, deeper resume positions go first.
		if targets[i].Tier == core.TierResume {
			return targets[i].Item.ResumeOffset > targets[j].Item.ResumeOffset
		}
		return false // stable: preserve input order
	})
	return targets
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scorer/ -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
git add internal/scorer/
git commit -m "feat(scorer): rank, dedupe, and exclude now-playing into tiered targets"
```

---

### Task 3: Path mapper

**Files:**
- Create: `internal/pathmap/pathmap.go`
- Test: `internal/pathmap/pathmap_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Rule struct { From, To string }`
  - `func New(rules []Rule) *Mapper`
  - `func (m *Mapper) ToHost(serverPath string) (string, bool)` - longest-matching `From` prefix wins; returns the rewritten host path and `true`, or `("", false)` if no rule matches.

- [ ] **Step 1: Write the failing test**

```go
package pathmap

import "testing"

func TestToHostLongestPrefixWins(t *testing.T) {
	m := New([]Rule{
		{From: "/share", To: "/mnt/user"},
		{From: "/share/TV_Shows", To: "/mnt/disk1/TV_Shows"},
	})
	got, ok := m.ToHost("/share/TV_Shows/Slow Horses/s05e01.mkv")
	if !ok {
		t.Fatal("expected a match")
	}
	want := "/mnt/disk1/TV_Shows/Slow Horses/s05e01.mkv"
	if got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostNoMatch(t *testing.T) {
	m := New([]Rule{{From: "/share", To: "/mnt/user"}})
	if _, ok := m.ToHost("/data/movie.mkv"); ok {
		t.Error("expected no match for unmapped path")
	}
}

func TestToHostEmptyRulesPassThrough(t *testing.T) {
	// With no rules, server path is assumed already host-correct.
	m := New(nil)
	got, ok := m.ToHost("/mnt/user/TV/x.mkv")
	if !ok || got != "/mnt/user/TV/x.mkv" {
		t.Errorf("empty mapper should pass through, got %q ok=%v", got, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pathmap/ -v`
Expected: FAIL - `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package pathmap rewrites media-server-reported paths to host filesystem paths.
package pathmap

import (
	"sort"
	"strings"
)

// Rule maps a server path prefix (From) to a host path prefix (To).
type Rule struct {
	From string
	To   string
}

// Mapper applies path rules, longest matching prefix first.
type Mapper struct {
	rules []Rule
}

// New returns a Mapper. Rules are sorted so the longest From prefix is tried
// first, giving deterministic results regardless of input order.
func New(rules []Rule) *Mapper {
	cp := make([]Rule, len(rules))
	copy(cp, rules)
	sort.SliceStable(cp, func(i, j int) bool {
		return len(cp[i].From) > len(cp[j].From)
	})
	return &Mapper{rules: cp}
}

// ToHost rewrites serverPath. With no rules, the path passes through unchanged
// (the server already reports host-correct paths). Returns false when rules
// exist but none match.
func (m *Mapper) ToHost(serverPath string) (string, bool) {
	if len(m.rules) == 0 {
		return serverPath, true
	}
	for _, r := range m.rules {
		if serverPath == r.From || strings.HasPrefix(serverPath, r.From+"/") {
			return r.To + strings.TrimPrefix(serverPath, r.From), true
		}
		if strings.HasPrefix(serverPath, r.From) {
			return r.To + strings.TrimPrefix(serverPath, r.From), true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pathmap/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pathmap/
git commit -m "feat(pathmap): map server paths to host paths, longest prefix wins"
```

---

### Task 4: RAM budget (sysinfo)

**Files:**
- Create: `internal/sysinfo/sysinfo.go`
- Test: `internal/sysinfo/sysinfo_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func ParseMemAvailable(r io.Reader) (int64, error)` - parses `/proc/meminfo` content, returns `MemAvailable` in bytes.
  - `func AvailableBytes() (int64, error)` - reads `/proc/meminfo`.
  - `func BudgetBytes(available int64, pct int) int64` - `available * pct / 100`, clamped to `>= 0`.

- [ ] **Step 1: Write the failing test**

```go
package sysinfo

import (
	"strings"
	"testing"
)

const sampleMeminfo = `MemTotal:       197565123 kB
MemFree:         6215254 kB
MemAvailable:  117000000 kB
Buffers:          123456 kB
`

func TestParseMemAvailable(t *testing.T) {
	got, err := ParseMemAvailable(strings.NewReader(sampleMeminfo))
	if err != nil {
		t.Fatal(err)
	}
	want := int64(117000000) * 1024 // kB -> bytes
	if got != want {
		t.Errorf("ParseMemAvailable = %d, want %d", got, want)
	}
}

func TestParseMemAvailableMissing(t *testing.T) {
	_, err := ParseMemAvailable(strings.NewReader("MemTotal: 100 kB\n"))
	if err == nil {
		t.Error("expected error when MemAvailable absent")
	}
}

func TestBudgetBytes(t *testing.T) {
	if got := BudgetBytes(1000, 50); got != 500 {
		t.Errorf("BudgetBytes(1000,50) = %d, want 500", got)
	}
	if got := BudgetBytes(1000, -5); got != 0 {
		t.Errorf("negative pct should clamp to 0, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sysinfo/ -v`
Expected: FAIL - `undefined: ParseMemAvailable`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package sysinfo reads host memory information for the preload budget.
package sysinfo

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ParseMemAvailable extracts MemAvailable (in bytes) from /proc/meminfo content.
func ParseMemAvailable(r io.Reader) (int64, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line) // ["MemAvailable:", "117000000", "kB"]
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed MemAvailable line: %q", line)
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsing MemAvailable value: %w", err)
		}
		return kb * 1024, nil
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("MemAvailable not found in meminfo")
}

// AvailableBytes reads /proc/meminfo and returns MemAvailable in bytes.
func AvailableBytes() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only file; close error not actionable
	return ParseMemAvailable(f)
}

// BudgetBytes returns pct percent of available, clamped to a non-negative value.
func BudgetBytes(available int64, pct int) int64 {
	if pct <= 0 || available <= 0 {
		return 0
	}
	return available * int64(pct) / 100
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sysinfo/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sysinfo/
git commit -m "feat(sysinfo): parse MemAvailable and compute preload budget"
```

---

### Task 5: Page cache (warm + residency)

**Files:**
- Create: `internal/pagecache/pagecache.go` (interface + portable warming)
- Create: `internal/pagecache/resident_linux.go` (build tag `linux`, mincore)
- Create: `internal/pagecache/resident_other.go` (build tag `!linux`, fallback)
- Test: `internal/pagecache/pagecache_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Cache interface { Warm(path string, offset, length int64) error; Resident(path string, offset, length int64) (resident int64, ok bool, err error) }`
  - `func New() Cache`
  - Portable `Warm` preads the range into a discard buffer. `Resident` returns `ok=false` on non-Linux; on Linux returns resident bytes via `mincore`.

- [ ] **Step 1: Write the failing test**

```go
package pagecache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWarmReadsRange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	data := make([]byte, 1<<20) // 1 MiB
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c := New()
	if err := c.Warm(p, 0, 4096); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	// Warming past EOF must not error (clamp to file size).
	if err := c.Warm(p, int64(len(data)-100), 4096); err != nil {
		t.Fatalf("Warm near EOF: %v", err)
	}
}

func TestWarmMissingFile(t *testing.T) {
	c := New()
	if err := c.Warm("/no/such/file", 0, 10); err == nil {
		t.Error("expected error warming a missing file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pagecache/ -v`
Expected: FAIL - `undefined: New`.

- [ ] **Step 3: Write the interface + portable warming**

`internal/pagecache/pagecache.go`:

```go
// Package pagecache warms and inspects the OS page cache for media files.
package pagecache

import (
	"errors"
	"io"
	"os"
)

// Cache warms byte ranges into the page cache and (on supported platforms)
// reports how much of a range is already resident.
type Cache interface {
	// Warm reads [offset, offset+length) so the kernel caches those pages.
	// Ranges past EOF are clamped. Returns an error only on open/read failure.
	Warm(path string, offset, length int64) error
	// Resident reports how many bytes of [offset, offset+length) are already in
	// the page cache. ok is false when residency cannot be determined on this
	// platform (callers should then warm unconditionally).
	Resident(path string, offset, length int64) (resident int64, ok bool, err error)
}

// New returns the platform Cache implementation.
func New() Cache { return &osCache{} }

type osCache struct{}

func (c *osCache) Warm(path string, offset, length int64) error {
	if length <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only; close error not actionable

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 1<<20) // 1 MiB chunks
	remaining := length
	for remaining > 0 {
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		read, err := f.Read(buf[:n])
		remaining -= int64(read)
		if errors.Is(err, io.EOF) {
			return nil // clamped at EOF
		}
		if err != nil {
			return err
		}
		if read == 0 {
			return nil
		}
	}
	return nil
}

func (c *osCache) Resident(path string, offset, length int64) (int64, bool, error) {
	return residentImpl(path, offset, length)
}
```

`internal/pagecache/resident_other.go`:

```go
//go:build !linux

package pagecache

// residentImpl cannot determine residency off Linux; callers warm unconditionally.
func residentImpl(_ string, _, _ int64) (int64, bool, error) {
	return 0, false, nil
}
```

`internal/pagecache/resident_linux.go`:

```go
//go:build linux

package pagecache

import (
	"os"

	"golang.org/x/sys/unix"
)

// residentImpl mmaps the requested range and asks mincore which pages are
// resident, returning the resident byte count.
func residentImpl(path string, offset, length int64) (int64, bool, error) {
	if length <= 0 {
		return 0, true, nil
	}
	f, err := os.Open(path)
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
	// mmap must start on a page boundary; align the offset down and grow length.
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
	if err := unix.Mincore(data, vec); err != nil {
		return 0, false, err
	}

	var resident int64
	for _, v := range vec {
		if v&0x1 == 1 { // low bit set => page resident
			resident += pageSize
		}
	}
	if resident > length {
		resident = length
	}
	return resident, true, nil
}
```

- [ ] **Step 4: Add the dependency and run tests**

Run:
```bash
go get golang.org/x/sys/unix
go test ./internal/pagecache/ -v
```
Expected: PASS. On Linux, also confirm residency works:
```bash
GOOS=linux go build ./internal/pagecache/   # cross-compile check
```
Expected: builds with no error.

- [ ] **Step 5: Commit**

```bash
git add internal/pagecache/ go.mod go.sum
git commit -m "feat(pagecache): portable warming + Linux mincore residency"
```

---

### Task 6: Preloader

**Files:**
- Create: `internal/preloader/preloader.go`
- Test: `internal/preloader/preloader_test.go`

**Interfaces:**
- Consumes: `core.PreloadTarget`, `pathmap.Mapper`, `pagecache.Cache`.
- Produces:
  - `type Config struct { TargetSeconds int; MinHeadBytes, MaxHeadBytes, TailBytes int64 }`
  - `type FS interface { Stat(path string) (size int64, err error) }` (injectable; default wraps `os.Stat`)
  - `type Preloader struct { ... }` with `func New(cfg Config, cache pagecache.Cache, mapper *pathmap.Mapper, fs FS, log *slog.Logger) *Preloader`
  - `type RunStats struct { Preloaded, Skipped, Missing int; BytesWarmed int64; ByTier map[core.Tier]int }`
  - `func (p *Preloader) Run(ctx context.Context, targets []core.PreloadTarget, budgetBytes int64) RunStats`
  - `func HeadBytes(cfg Config, it core.MediaItem) int64` (exported for unit testing the sizing math)

- [ ] **Step 1: Write the failing test**

```go
package preloader

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
)

// fakeCache records Warm calls and reports nothing resident.
type fakeCache struct {
	warmed []warmCall
	resident int64
}
type warmCall struct {
	path           string
	offset, length int64
}

func (f *fakeCache) Warm(path string, offset, length int64) error {
	f.warmed = append(f.warmed, warmCall{path, offset, length})
	return nil
}
func (f *fakeCache) Resident(_ string, _, length int64) (int64, bool, error) {
	if f.resident < 0 {
		return 0, false, nil // residency unknown
	}
	return f.resident, true, nil
}

type fakeFS map[string]int64 // path -> size

func (m fakeFS) Stat(path string) (int64, error) {
	sz, ok := m[path]
	if !ok {
		return 0, io.EOF // stand-in for "not found"
	}
	return sz, nil
}

func testCfg() Config {
	return Config{TargetSeconds: 20, MinHeadBytes: 8 << 20, MaxHeadBytes: 250 << 20, TailBytes: 1 << 20}
}

func TestHeadBytesDurationBased(t *testing.T) {
	// 25 Mbps over 20s = 25e6/8*20 = 62.5 MB, within clamp.
	it := core.MediaItem{BitrateBps: 25_000_000}
	got := HeadBytes(testCfg(), it)
	want := int64(20) * 25_000_000 / 8
	if got != want {
		t.Errorf("HeadBytes = %d, want %d", got, want)
	}
}

func TestHeadBytesClampsLow(t *testing.T) {
	it := core.MediaItem{BitrateBps: 1_000_000} // 20s = 2.5MB < 8MB floor
	if got := HeadBytes(testCfg(), it); got != 8<<20 {
		t.Errorf("HeadBytes = %d, want floor 8MiB", got)
	}
}

func TestHeadBytesFallbackToSizeOverRuntime(t *testing.T) {
	it := core.MediaItem{SizeBytes: 600 << 20, Runtime: 45 * time.Minute} // ~bitrate
	got := HeadBytes(testCfg(), it)
	if got <= 0 {
		t.Fatal("expected positive head bytes from size/runtime fallback")
	}
}

func TestRunSkipsMissingAndBudgets(t *testing.T) {
	cache := &fakeCache{resident: -1} // unknown residency => always warm
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))

	targets := []core.PreloadTarget{
		{Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 25_000_000}, Tier: core.TierNextUp},
		{Item: core.MediaItem{ID: "missing", ServerPath: "/mnt/user/TV/none.mkv", BitrateBps: 25_000_000}, Tier: core.TierNextUp},
	}
	// Budget only fits one head + tail.
	budget := HeadBytes(testCfg(), targets[0].Item) + testCfg().TailBytes + 1
	stats := p.Run(context.Background(), targets, budget)

	if stats.Preloaded != 1 {
		t.Errorf("Preloaded = %d, want 1", stats.Preloaded)
	}
	if stats.Missing != 1 {
		t.Errorf("Missing = %d, want 1", stats.Missing)
	}
	if len(cache.warmed) == 0 || cache.warmed[0].path != "/mnt/user/TV/a.mkv" {
		t.Errorf("expected warm of a.mkv, got %+v", cache.warmed)
	}
}

func TestRunResumeUsesOffset(t *testing.T) {
	cache := &fakeCache{resident: -1}
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, ResumeOffset: 10 * time.Minute},
		Tier: core.TierResume,
	}}
	p.Run(context.Background(), targets, 1<<40)
	// offset = 600s * 8e6/8 = 600 * 1e6 = 600_000_000 bytes
	if cache.warmed[0].offset != 600_000_000 {
		t.Errorf("resume offset = %d, want 600000000", cache.warmed[0].offset)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/preloader/ -v`
Expected: FAIL - `undefined: New` / `undefined: HeadBytes`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package preloader warms the page cache for a ranked list of targets within a
// byte budget, sizing each read by playback duration.
package preloader

import (
	"context"
	"log/slog"
	"os"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/pagecache"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
)

// Config controls duration-based sizing and the tail read.
type Config struct {
	TargetSeconds int
	MinHeadBytes  int64
	MaxHeadBytes  int64
	TailBytes     int64
}

// FS abstracts file metadata for testability.
type FS interface {
	Stat(path string) (size int64, err error)
}

type osFS struct{}

func (osFS) Stat(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// DefaultFS returns an FS backed by the real filesystem.
func DefaultFS() FS { return osFS{} }

// RunStats summarizes a preload pass.
type RunStats struct {
	Preloaded   int
	Skipped     int
	Missing     int
	BytesWarmed int64
	ByTier      map[core.Tier]int
}

// Preloader executes preload passes.
type Preloader struct {
	cfg    Config
	cache  pagecache.Cache
	mapper *pathmap.Mapper
	fs     FS
	log    *slog.Logger
}

// New builds a Preloader.
func New(cfg Config, cache pagecache.Cache, mapper *pathmap.Mapper, fs FS, log *slog.Logger) *Preloader {
	return &Preloader{cfg: cfg, cache: cache, mapper: mapper, fs: fs, log: log}
}

// HeadBytes computes the duration-based head size for an item, clamped.
func HeadBytes(cfg Config, it core.MediaItem) int64 {
	bps := it.BitrateBps
	if bps <= 0 && it.Runtime > 0 {
		bps = int64(float64(it.SizeBytes) / it.Runtime.Seconds() * 8)
	}
	want := int64(cfg.TargetSeconds) * bps / 8
	if want < cfg.MinHeadBytes {
		want = cfg.MinHeadBytes
	}
	if want > cfg.MaxHeadBytes {
		want = cfg.MaxHeadBytes
	}
	return want
}

// Run warms targets in order until the budget is exhausted.
func (p *Preloader) Run(ctx context.Context, targets []core.PreloadTarget, budgetBytes int64) RunStats {
	stats := RunStats{ByTier: map[core.Tier]int{}}
	var used int64
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		hostPath, ok := p.mapper.ToHost(t.Item.ServerPath)
		if !ok {
			stats.Missing++
			p.log.Warn("no path mapping", "server_path", t.Item.ServerPath)
			continue
		}
		size, err := p.fs.Stat(hostPath)
		if err != nil {
			stats.Missing++
			p.log.Warn("stat failed", "path", hostPath, "err", err)
			continue
		}

		head := HeadBytes(p.cfg, t.Item)
		offset := resumeOffsetBytes(t.Item)
		if offset >= size {
			offset = 0
		}
		if offset+head > size {
			head = size - offset
		}
		cost := head + p.cfg.TailBytes
		if used+cost > budgetBytes {
			break // budget exhausted; remaining lower-priority targets dropped
		}

		// Skip ranges already fully resident.
		if resident, known, rerr := p.cache.Resident(hostPath, offset, head); rerr == nil && known && resident >= head {
			stats.Skipped++
			stats.ByTier[t.Tier]++
			continue
		}

		if err := p.cache.Warm(hostPath, offset, head); err != nil {
			stats.Missing++
			p.log.Warn("warm failed", "path", hostPath, "err", err)
			continue
		}
		if p.cfg.TailBytes > 0 && size > p.cfg.TailBytes {
			_ = p.cache.Warm(hostPath, size-p.cfg.TailBytes, p.cfg.TailBytes)
		}
		used += cost
		stats.Preloaded++
		stats.BytesWarmed += cost
		stats.ByTier[t.Tier]++
		p.log.Info("preloaded", "name", t.Item.Name, "tier", t.Tier.String(),
			"user", t.Item.UserID, "offset", offset, "bytes", head)
	}
	return stats
}

func resumeOffsetBytes(it core.MediaItem) int64 {
	if it.ResumeOffset <= 0 || it.BitrateBps <= 0 {
		return 0
	}
	return int64(it.ResumeOffset.Seconds()) * it.BitrateBps / 8
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/preloader/ -v`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
git add internal/preloader/
git commit -m "feat(preloader): duration-based, budgeted, resume-aware page-cache warming"
```

---

### Task 7: Emby client core

**Files:**
- Create: `internal/mediaserver/emby/client.go`
- Test: `internal/mediaserver/emby/client_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error)` - validates baseURL (http/https only; no creds/query/fragment).
  - `func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error` - sets `X-Emby-Token`, decodes JSON.

- [ ] **Step 1: Write the failing test**

```go
package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRejectsBadURL(t *testing.T) {
	for _, bad := range []string{"", "ftp://x", "http://user:pw@host", "http://h/?q=1"} {
		if _, err := New(bad, "k", nil); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestGetSendsTokenAndDecodes(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Emby-Token")
		_, _ = w.Write([]byte(`{"Value":42}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "secret", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Value int }
	if err := c.get(context.Background(), "/Test", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotToken != "secret" {
		t.Errorf("X-Emby-Token = %q, want secret", gotToken)
	}
	if out.Value != 42 {
		t.Errorf("decoded Value = %d, want 42", out.Value)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mediaserver/emby/ -v`
Expected: FAIL - `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package emby is a minimal read-only client for the Emby API.
package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single Emby server with an API key.
type Client struct {
	base   string
	apiKey string
	http   *http.Client
}

// New validates the base URL and returns a Client. The base URL must be
// http/https with no embedded credentials, query, or fragment.
func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("base URL has no host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base URL must not contain credentials, query, or fragment")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		base:   strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"),
		apiKey: apiKey,
		http:   httpClient,
	}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	full := c.base + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // close error not actionable on response

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("emby GET %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mediaserver/emby/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mediaserver/emby/
git commit -m "feat(emby): client core with URL validation and token auth"
```

---

### Task 8: Emby watch endpoints

**Files:**
- Create: `internal/mediaserver/emby/watch.go`
- Create: `internal/mediaserver/emby/testdata/resume.json`
- Test: `internal/mediaserver/emby/watch_test.go`

**Interfaces:**
- Consumes: `Client.get`, `core.MediaItem`.
- Produces (all on `*Client`):
  - `func (c *Client) Users(ctx) ([]User, error)` where `type User struct { ID, Name string }`
  - `func (c *Client) Resume(ctx, userID string) ([]core.MediaItem, error)` (sets `ResumeOffset`)
  - `func (c *Client) NextUp(ctx, userID string) ([]core.MediaItem, error)`
  - `func (c *Client) RecentlyAdded(ctx, userID string) ([]core.MediaItem, error)`
  - `func (c *Client) NowPlayingIDs(ctx) (map[string]bool, error)`

- [ ] **Step 1: Create the fixture**

`internal/mediaserver/emby/testdata/resume.json`:

```json
{
  "Items": [
    {
      "Id": "item1",
      "Name": "Slow Horses S05E01",
      "RunTimeTicks": 27063290000,
      "UserData": { "PlaybackPositionTicks": 6000000000 },
      "MediaSources": [
        { "Path": "/share/TV_Shows/Slow Horses/s05e01.mkv", "Bitrate": 25000000, "Size": 8471453856 }
      ]
    }
  ]
}
```

- [ ] **Step 2: Write the failing test**

```go
package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func serveFixture(t *testing.T, file string) *Client {
	t.Helper()
	body, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResumeMapsFields(t *testing.T) {
	c := serveFixture(t, "resume.json")
	items, err := c.Resume(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.ID != "item1" || it.ServerPath != "/share/TV_Shows/Slow Horses/s05e01.mkv" {
		t.Errorf("bad id/path: %+v", it)
	}
	if it.BitrateBps != 25000000 || it.SizeBytes != 8471453856 {
		t.Errorf("bad bitrate/size: %+v", it)
	}
	// RunTimeTicks 27063290000 / 1e7 = 2706.329s
	if it.Runtime != time.Duration(27063290000*100) {
		t.Errorf("runtime = %v", it.Runtime)
	}
	// PlaybackPositionTicks 6000000000 / 1e7 = 600s
	if it.ResumeOffset != 600*time.Second {
		t.Errorf("resume offset = %v, want 10m", it.ResumeOffset)
	}
	if it.UserID != "u1" {
		t.Errorf("user id = %q, want u1", it.UserID)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/mediaserver/emby/ -run TestResume -v`
Expected: FAIL - `c.Resume undefined`.

- [ ] **Step 4: Write minimal implementation**

```go
package emby

import (
	"context"
	"net/url"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
)

// ticksPerSecond is the Emby/Jellyfin tick unit: 100-nanosecond intervals.
const ticksPerSecond = 10_000_000

// User is an Emby user account.
type User struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type mediaSource struct {
	Path    string `json:"Path"`
	Bitrate int64  `json:"Bitrate"`
	Size    int64  `json:"Size"`
}

type embyItem struct {
	ID           string        `json:"Id"`
	Name         string        `json:"Name"`
	RunTimeTicks int64         `json:"RunTimeTicks"`
	UserData     struct {
		PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
	} `json:"UserData"`
	MediaSources []mediaSource `json:"MediaSources"`
}

type itemsResponse struct {
	Items []embyItem `json:"Items"`
}

func ticksToDuration(t int64) time.Duration {
	return time.Duration(t) * 100 // 100ns per tick
}

func (e embyItem) toCore(userID string) core.MediaItem {
	mi := core.MediaItem{
		ID:           e.ID,
		Name:         e.Name,
		Runtime:      ticksToDuration(e.RunTimeTicks),
		ResumeOffset: ticksToDuration(e.UserData.PlaybackPositionTicks),
		UserID:       userID,
	}
	if len(e.MediaSources) > 0 {
		mi.ServerPath = e.MediaSources[0].Path
		mi.BitrateBps = e.MediaSources[0].Bitrate
		mi.SizeBytes = e.MediaSources[0].Size
	}
	return mi
}

func (c *Client) itemsTo(ctx context.Context, path string, q url.Values, userID string) ([]core.MediaItem, error) {
	var resp itemsResponse
	if err := c.get(ctx, path, q, &resp); err != nil {
		return nil, err
	}
	out := make([]core.MediaItem, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, it.toCore(userID))
	}
	return out, nil
}

func mediaFields() url.Values {
	return url.Values{"Fields": {"Path,MediaSources"}}
}

// Users lists Emby user accounts.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var users []User
	if err := c.get(ctx, "/Users", nil, &users); err != nil {
		return nil, err
	}
	return users, nil
}

// Resume returns the user's in-progress items with their resume offsets.
func (c *Client) Resume(ctx context.Context, userID string) ([]core.MediaItem, error) {
	return c.itemsTo(ctx, "/Users/"+userID+"/Items/Resume", mediaFields(), userID)
}

// NextUp returns the next episode of each series the user is watching.
func (c *Client) NextUp(ctx context.Context, userID string) ([]core.MediaItem, error) {
	q := mediaFields()
	q.Set("UserId", userID)
	return c.itemsTo(ctx, "/Shows/NextUp", q, userID)
}

// RecentlyAdded returns recently added items for the user.
func (c *Client) RecentlyAdded(ctx context.Context, userID string) ([]core.MediaItem, error) {
	// /Items/Latest returns a bare array, not an {Items:[]} envelope.
	var items []embyItem
	if err := c.get(ctx, "/Users/"+userID+"/Items/Latest", mediaFields(), &items); err != nil {
		return nil, err
	}
	out := make([]core.MediaItem, 0, len(items))
	for _, it := range items {
		out = append(out, it.toCore(userID))
	}
	return out, nil
}

// NowPlayingIDs returns the set of item IDs in active playback sessions.
func (c *Client) NowPlayingIDs(ctx context.Context) (map[string]bool, error) {
	var sessions []struct {
		NowPlayingItem *struct {
			ID string `json:"Id"`
		} `json:"NowPlayingItem"`
	}
	if err := c.get(ctx, "/Sessions", nil, &sessions); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, s := range sessions {
		if s.NowPlayingItem != nil && s.NowPlayingItem.ID != "" {
			ids[s.NowPlayingItem.ID] = true
		}
	}
	return ids, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/mediaserver/emby/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mediaserver/emby/
git commit -m "feat(emby): map Resume/NextUp/Latest/Sessions to core media items"
```

---

### Task 9: Config

**Files:**
- Create: `internal/config/config.go`
- Create: `config.example.toml` (repo root)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `pathmap.Rule`, `preloader.Config`.
- Produces:
  - `type Config struct { Server ServerConfig; Users UsersConfig; Preload PreloadConfig; PathMap []PathRule; Schedule ScheduleConfig }`
  - `func Load(path string) (*Config, error)` (decode + `Validate`)
  - `func (c *Config) Validate() error`
  - Field types listed in the implementation below.

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `
[server]
type = "emby"
url = "http://192.168.1.126:8096"
api_key = "abc123"

[users]
enabled = ["jesse", "rachel"]

[preload]
ram_percent = 50
target_seconds = 20

[[path_map]]
from = "/share"
to = "/mnt/user"

[schedule]
sweep_seconds = 60
session_poll_seconds = 5
`

func TestLoadValid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.URL != "http://192.168.1.126:8096" || c.Server.APIKey != "abc123" {
		t.Errorf("server parsed wrong: %+v", c.Server)
	}
	if c.Preload.RAMPercent != 50 || c.Preload.TargetSeconds != 20 {
		t.Errorf("preload parsed wrong: %+v", c.Preload)
	}
	if len(c.PathMap) != 1 || c.PathMap[0].From != "/share" {
		t.Errorf("path_map parsed wrong: %+v", c.PathMap)
	}
}

func TestValidateRejectsBadPercent(t *testing.T) {
	c := &Config{}
	c.Server.Type = "emby"
	c.Server.URL = "http://h:8096"
	c.Server.APIKey = "k"
	c.Preload.RAMPercent = 150
	if err := c.Validate(); err == nil {
		t.Error("expected error for ram_percent > 100")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL - `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package config loads and validates the preloadd TOML configuration.
package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

type ServerConfig struct {
	Type   string `toml:"type"` // "emby" (Phase 1)
	URL    string `toml:"url"`
	APIKey string `toml:"api_key"`
}

type UsersConfig struct {
	Enabled []string `toml:"enabled"` // user names; empty => all users
}

type PreloadConfig struct {
	RAMPercent    int   `toml:"ram_percent"`
	TargetSeconds int   `toml:"target_seconds"`
	MinHeadMB     int64 `toml:"min_head_mb"`
	MaxHeadMB     int64 `toml:"max_head_mb"`
	TailMB        int64 `toml:"tail_mb"`
}

type PathRule struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

type ScheduleConfig struct {
	SweepSeconds       int `toml:"sweep_seconds"`
	SessionPollSeconds int `toml:"session_poll_seconds"`
}

// Config is the full preloadd configuration.
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Users    UsersConfig    `toml:"users"`
	Preload  PreloadConfig  `toml:"preload"`
	PathMap  []PathRule     `toml:"path_map"`
	Schedule ScheduleConfig `toml:"schedule"`
}

// Load decodes a TOML config file, applies defaults, and validates it.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Preload.RAMPercent == 0 {
		c.Preload.RAMPercent = 50
	}
	if c.Preload.TargetSeconds == 0 {
		c.Preload.TargetSeconds = 20
	}
	if c.Preload.MinHeadMB == 0 {
		c.Preload.MinHeadMB = 8
	}
	if c.Preload.MaxHeadMB == 0 {
		c.Preload.MaxHeadMB = 250
	}
	if c.Preload.TailMB == 0 {
		c.Preload.TailMB = 1
	}
	if c.Schedule.SweepSeconds == 0 {
		c.Schedule.SweepSeconds = 60
	}
	if c.Schedule.SessionPollSeconds == 0 {
		c.Schedule.SessionPollSeconds = 5
	}
}

// Validate checks required fields and ranges.
func (c *Config) Validate() error {
	if c.Server.Type != "emby" {
		return fmt.Errorf("server.type must be \"emby\" in Phase 1, got %q", c.Server.Type)
	}
	if c.Server.URL == "" {
		return fmt.Errorf("server.url is required")
	}
	if c.Server.APIKey == "" {
		return fmt.Errorf("server.api_key is required")
	}
	if c.Preload.RAMPercent < 1 || c.Preload.RAMPercent > 100 {
		return fmt.Errorf("preload.ram_percent must be 1-100, got %d", c.Preload.RAMPercent)
	}
	if c.Preload.TargetSeconds < 1 {
		return fmt.Errorf("preload.target_seconds must be >= 1")
	}
	return nil
}
```

- [ ] **Step 4: Add dependency, create example, run tests**

Run:
```bash
go get github.com/BurntSushi/toml
go test ./internal/config/ -v
```
Expected: PASS.

Create `config.example.toml`:

```toml
# preloadd configuration. Copy to config.toml and fill in your values.
# config.toml is gitignored because api_key is a secret.

[server]
type = "emby"
url = "http://192.168.1.126:8096"
api_key = "PUT-YOUR-EMBY-API-KEY-HERE"

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
sweep_seconds = 60          # full re-evaluation interval
session_poll_seconds = 5    # how often to poll /Sessions for live changes
```

- [ ] **Step 5: Commit**

```bash
git add internal/config/ config.example.toml go.mod go.sum
git commit -m "feat(config): TOML config load, defaults, and validation"
```

---

### Task 10: Pipeline assembly

**Files:**
- Create: `internal/app/pipeline.go`
- Test: `internal/app/pipeline_test.go`

**Interfaces:**
- Consumes: `emby.Client` methods, `scorer.Rank`, `preloader.Preloader`, `core`.
- Produces:
  - `type Provider interface { Users(ctx) ([]emby.User, error); Resume(ctx, id) ([]core.MediaItem, error); NextUp(ctx, id) ([]core.MediaItem, error); RecentlyAdded(ctx, id) ([]core.MediaItem, error); NowPlayingIDs(ctx) (map[string]bool, error) }`
  - `func CollectCandidates(ctx, p Provider, enabled []string) ([]scorer.Candidate, map[string]bool, error)` - resolves enabled users (empty = all), fetches tiers 1-3 per user, fetches now-playing once.
  - `func ResolveUserIDs(users []emby.User, enabled []string) []string`

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"context"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
)

type stubProvider struct {
	users   []emby.User
	resume  map[string][]core.MediaItem
	nextUp  map[string][]core.MediaItem
	latest  map[string][]core.MediaItem
	playing map[string]bool
}

func (s *stubProvider) Users(context.Context) ([]emby.User, error) { return s.users, nil }
func (s *stubProvider) Resume(_ context.Context, id string) ([]core.MediaItem, error) {
	return s.resume[id], nil
}
func (s *stubProvider) NextUp(_ context.Context, id string) ([]core.MediaItem, error) {
	return s.nextUp[id], nil
}
func (s *stubProvider) RecentlyAdded(_ context.Context, id string) ([]core.MediaItem, error) {
	return s.latest[id], nil
}
func (s *stubProvider) NowPlayingIDs(context.Context) (map[string]bool, error) {
	return s.playing, nil
}

func TestResolveUserIDsAllWhenEmpty(t *testing.T) {
	users := []emby.User{{ID: "1", Name: "jesse"}, {ID: "2", Name: "rachel"}}
	got := ResolveUserIDs(users, nil)
	if len(got) != 2 {
		t.Fatalf("expected all users, got %v", got)
	}
}

func TestResolveUserIDsFiltersByName(t *testing.T) {
	users := []emby.User{{ID: "1", Name: "jesse"}, {ID: "2", Name: "rachel"}}
	got := ResolveUserIDs(users, []string{"rachel"})
	if len(got) != 1 || got[0] != "2" {
		t.Fatalf("expected [2], got %v", got)
	}
}

func TestCollectCandidatesTiersAndPlaying(t *testing.T) {
	p := &stubProvider{
		users:   []emby.User{{ID: "1", Name: "jesse"}},
		resume:  map[string][]core.MediaItem{"1": {{ID: "r1"}}},
		nextUp:  map[string][]core.MediaItem{"1": {{ID: "n1"}}},
		latest:  map[string][]core.MediaItem{"1": {{ID: "l1"}}},
		playing: map[string]bool{"x": true},
	}
	cands, playing, err := CollectCandidates(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(cands))
	}
	tierByID := map[string]core.Tier{}
	for _, c := range cands {
		tierByID[c.Item.ID] = c.Tier
	}
	if tierByID["r1"] != core.TierResume || tierByID["n1"] != core.TierNextUp || tierByID["l1"] != core.TierRecentlyAdded {
		t.Errorf("tiers assigned wrong: %v", tierByID)
	}
	if !playing["x"] {
		t.Error("now-playing set not returned")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -v`
Expected: FAIL - `undefined: ResolveUserIDs`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package app assembles the media-server client, scorer, and preloader into a
// runnable pipeline.
package app

import (
	"context"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/sydlexius/watch-aware-preloader/internal/scorer"
)

// Provider is the subset of the Emby client the pipeline needs.
type Provider interface {
	Users(ctx context.Context) ([]emby.User, error)
	Resume(ctx context.Context, userID string) ([]core.MediaItem, error)
	NextUp(ctx context.Context, userID string) ([]core.MediaItem, error)
	RecentlyAdded(ctx context.Context, userID string) ([]core.MediaItem, error)
	NowPlayingIDs(ctx context.Context) (map[string]bool, error)
}

// ResolveUserIDs maps configured user names to IDs. An empty enabled list
// selects all users.
func ResolveUserIDs(users []emby.User, enabled []string) []string {
	if len(enabled) == 0 {
		ids := make([]string, 0, len(users))
		for _, u := range users {
			ids = append(ids, u.ID)
		}
		return ids
	}
	want := map[string]bool{}
	for _, n := range enabled {
		want[n] = true
	}
	var ids []string
	for _, u := range users {
		if want[u.Name] {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

// CollectCandidates fetches tiers 1-3 for each enabled user plus the global
// now-playing set.
func CollectCandidates(ctx context.Context, p Provider, enabled []string) ([]scorer.Candidate, map[string]bool, error) {
	users, err := p.Users(ctx)
	if err != nil {
		return nil, nil, err
	}
	playing, err := p.NowPlayingIDs(ctx)
	if err != nil {
		return nil, nil, err
	}

	var cands []scorer.Candidate
	add := func(items []core.MediaItem, tier core.Tier) {
		for _, it := range items {
			cands = append(cands, scorer.Candidate{Item: it, Tier: tier})
		}
	}
	for _, id := range ResolveUserIDs(users, enabled) {
		resume, err := p.Resume(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		add(resume, core.TierResume)

		nextUp, err := p.NextUp(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		add(nextUp, core.TierNextUp)

		latest, err := p.RecentlyAdded(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		add(latest, core.TierRecentlyAdded)
	}
	return cands, playing, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/
git commit -m "feat(app): collect tiered candidates and resolve enabled users"
```

---

### Task 11: Daemon loop and wiring

**Files:**
- Create: `internal/app/daemon.go`
- Modify: `cmd/preloadd/main.go` (replace the stub)
- Test: `internal/app/daemon_test.go`

**Interfaces:**
- Consumes: `CollectCandidates`, `scorer.Rank`, `preloader.Preloader`, `sysinfo`, `config.Config`.
- Produces:
  - `func RunOnce(ctx, p Provider, enabled []string, pre *preloader.Preloader, budget int64, log *slog.Logger) (preloader.RunStats, error)` - one full pipeline pass.
  - `func (d *Daemon) Loop(ctx) error` - periodic sweep + session-poll-triggered sweep.
  - `type Daemon struct { ... }` and `func NewDaemon(cfg *config.Config, p Provider, pre *preloader.Preloader, log *slog.Logger) *Daemon`

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/sydlexius/watch-aware-preloader/internal/pagecache"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
)

func TestRunOnceExcludesNowPlayingEndToEnd(t *testing.T) {
	p := &stubProvider{
		users:   []emby.User{{ID: "1", Name: "jesse"}},
		resume:  map[string][]core.MediaItem{"1": {{ID: "r1", ServerPath: "/x/r1.mkv", BitrateBps: 8_000_000}}},
		nextUp:  map[string][]core.MediaItem{"1": {{ID: "playing", ServerPath: "/x/p.mkv", BitrateBps: 8_000_000}}},
		latest:  map[string][]core.MediaItem{},
		playing: map[string]bool{"playing": true},
	}
	// fakeFS from preloader_test isn't visible here; use a stub FS inline.
	fs := stubFS{"/x/r1.mkv": 5 << 30, "/x/p.mkv": 5 << 30}
	cfg := preloader.Config{TargetSeconds: 20, MinHeadBytes: 8 << 20, MaxHeadBytes: 250 << 20, TailBytes: 1 << 20}
	pre := preloader.New(cfg, pagecache.New(), pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))

	stats, err := RunOnce(context.Background(), p, nil, pre, 1<<40, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// "playing" is excluded; only r1 should be preloaded.
	if stats.Preloaded != 1 {
		t.Errorf("Preloaded = %d, want 1 (now-playing excluded)", stats.Preloaded)
	}
}

type stubFS map[string]int64

func (m stubFS) Stat(path string) (int64, error) {
	if sz, ok := m[path]; ok {
		return sz, nil
	}
	return 0, io.EOF
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestRunOnce -v`
Expected: FAIL - `undefined: RunOnce`.

- [ ] **Step 3: Write the daemon implementation**

```go
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
	"github.com/sydlexius/watch-aware-preloader/internal/scorer"
	"github.com/sydlexius/watch-aware-preloader/internal/sysinfo"
)

// RunOnce performs one full pipeline pass: collect, rank, preload.
func RunOnce(ctx context.Context, p Provider, enabled []string, pre *preloader.Preloader, budget int64, log *slog.Logger) (preloader.RunStats, error) {
	cands, playing, err := CollectCandidates(ctx, p, enabled)
	if err != nil {
		return preloader.RunStats{}, err
	}
	targets := scorer.Rank(cands, playing)
	stats := pre.Run(ctx, targets, budget)
	log.Info("sweep complete",
		"targets", len(targets), "preloaded", stats.Preloaded,
		"skipped", stats.Skipped, "missing", stats.Missing,
		"bytes_warmed", stats.BytesWarmed, "by_tier", stats.ByTier)
	return stats, nil
}

// Daemon runs the periodic sweep and the session-triggered sweep.
type Daemon struct {
	cfg *config.Config
	p   Provider
	pre *preloader.Preloader
	log *slog.Logger
}

// NewDaemon wires the runtime loop.
func NewDaemon(cfg *config.Config, p Provider, pre *preloader.Preloader, log *slog.Logger) *Daemon {
	return &Daemon{cfg: cfg, p: p, pre: pre, log: log}
}

func (d *Daemon) budget() int64 {
	avail, err := sysinfo.AvailableBytes()
	if err != nil {
		d.log.Warn("reading available RAM failed; using 0 budget", "err", err)
		return 0
	}
	return sysinfo.BudgetBytes(avail, d.cfg.Preload.RAMPercent)
}

func (d *Daemon) sweep(ctx context.Context) {
	if _, err := RunOnce(ctx, d.p, d.cfg.Users.Enabled, d.pre, d.budget(), d.log); err != nil {
		d.log.Error("sweep failed", "err", err)
	}
}

// Loop runs until ctx is cancelled. A full sweep fires on the sweep interval;
// a fast session poll fires an extra sweep whenever the now-playing set changes
// (giving event-like latency without a websocket).
func (d *Daemon) Loop(ctx context.Context) error {
	d.sweep(ctx) // warm immediately on start

	sweepTick := time.NewTicker(time.Duration(d.cfg.Schedule.SweepSeconds) * time.Second)
	defer sweepTick.Stop()
	pollTick := time.NewTicker(time.Duration(d.cfg.Schedule.SessionPollSeconds) * time.Second)
	defer pollTick.Stop()

	var lastPlaying string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sweepTick.C:
			d.sweep(ctx)
		case <-pollTick.C:
			ids, err := d.p.NowPlayingIDs(ctx)
			if err != nil {
				d.log.Warn("session poll failed", "err", err)
				continue
			}
			if sig := playingSignature(ids); sig != lastPlaying {
				lastPlaying = sig
				d.log.Info("playback state changed; triggering sweep")
				d.sweep(ctx)
			}
		}
	}
}

func playingSignature(ids map[string]bool) string {
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	// Deterministic signature independent of map order.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	out := ""
	for _, k := range keys {
		out += k + ","
	}
	return out
}
```

- [ ] **Step 4: Replace `cmd/preloadd/main.go`**

```go
// Command preloadd is the watch-aware media preloader daemon.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sydlexius/watch-aware-preloader/internal/app"
	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/sydlexius/watch-aware-preloader/internal/pagecache"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "config.toml", "path to config file")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("preloadd starting", "version", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	client, err := emby.New(cfg.Server.URL, cfg.Server.APIKey, nil)
	if err != nil {
		log.Error("emby client init failed", "err", err)
		os.Exit(1)
	}

	rules := make([]pathmap.Rule, 0, len(cfg.PathMap))
	for _, r := range cfg.PathMap {
		rules = append(rules, pathmap.Rule{From: r.From, To: r.To})
	}
	preCfg := preloader.Config{
		TargetSeconds: cfg.Preload.TargetSeconds,
		MinHeadBytes:  cfg.Preload.MinHeadMB << 20,
		MaxHeadBytes:  cfg.Preload.MaxHeadMB << 20,
		TailBytes:     cfg.Preload.TailMB << 20,
	}
	pre := preloader.New(preCfg, pagecache.New(), pathmap.New(rules), preloader.DefaultFS(), log)

	d := app.NewDaemon(cfg, client, pre, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := d.Loop(ctx); err != nil && ctx.Err() == nil {
		log.Error("daemon loop exited with error", "err", err)
		os.Exit(1)
	}
	log.Info("preloadd stopped")
}
```

- [ ] **Step 5: Run tests and build**

Run:
```bash
go test ./internal/app/ -v
go build ./cmd/preloadd
```
Expected: tests PASS, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add internal/app/ cmd/preloadd/main.go
git commit -m "feat(app): daemon loop with periodic + session-triggered sweeps"
```

---

### Task 12: Verify subcommand (cache-hit proof) and full build gate

**Files:**
- Create: `internal/app/verify.go`
- Modify: `cmd/preloadd/main.go` (add `-verify` flag)
- Test: `internal/app/verify_test.go`

**Interfaces:**
- Consumes: `pagecache.Cache`, `preloader.HeadBytes`, `config`, `app` pipeline.
- Produces:
  - `func VerifyResidency(cache pagecache.Cache, hostPath string, offset, length int64) (residentPct float64, known bool, err error)` - returns the percentage of the range resident.

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"testing"
)

type residentCache struct {
	resident int64
	known    bool
}

func (c residentCache) Warm(string, int64, int64) error { return nil }
func (c residentCache) Resident(string, int64, length int64) (int64, bool, error) {
	if !c.known {
		return 0, false, nil
	}
	if c.resident > length {
		return length, true, nil
	}
	return c.resident, true, nil
}

func TestVerifyResidencyPercent(t *testing.T) {
	pct, known, err := VerifyResidency(residentCache{resident: 50, known: true}, "/x", 0, 100)
	if err != nil || !known {
		t.Fatalf("err=%v known=%v", err, known)
	}
	if pct != 50.0 {
		t.Errorf("pct = %v, want 50", pct)
	}
}

func TestVerifyResidencyUnknown(t *testing.T) {
	_, known, _ := VerifyResidency(residentCache{known: false}, "/x", 0, 100)
	if known {
		t.Error("expected known=false on platforms without mincore")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestVerify -v`
Expected: FAIL - `undefined: VerifyResidency`.

- [ ] **Step 3: Write minimal implementation**

`internal/app/verify.go`:

```go
package app

import "github.com/sydlexius/watch-aware-preloader/internal/pagecache"

// VerifyResidency reports what percentage of [offset, offset+length) is resident
// in the page cache. known is false on platforms without residency support.
func VerifyResidency(cache pagecache.Cache, hostPath string, offset, length int64) (float64, bool, error) {
	if length <= 0 {
		return 0, true, nil
	}
	resident, known, err := cache.Resident(hostPath, offset, length)
	if err != nil || !known {
		return 0, known, err
	}
	return float64(resident) / float64(length) * 100, true, nil
}
```

- [ ] **Step 4: Wire `-verify` into main and run the full gate**

In `cmd/preloadd/main.go`, add after `flag.Parse()`:

```go
	verify := flag.Bool("verify", false, "run one sweep, then report cache residency and exit")
```

(Declare it alongside `cfgPath` before `flag.Parse()`.) After building `pre` and `d`, before `d.Loop`, add:

```go
	if *verify {
		budget := d.Budget()
		stats, err := app.RunOnce(context.Background(), client, cfg.Users.Enabled, pre, budget, log)
		if err != nil {
			log.Error("verify sweep failed", "err", err)
			os.Exit(1)
		}
		log.Info("verify sweep done", "preloaded", stats.Preloaded, "skipped", stats.Skipped)
		return
	}
```

Add an exported `Budget()` method on `Daemon` (wrapping the existing unexported `budget()`):

```go
// Budget returns the current preload byte budget.
func (d *Daemon) Budget() int64 { return d.budget() }
```

Run the full gate:
```bash
go test ./... -v
CGO_ENABLED=1 go test -race -count=1 ./...
gofmt -l .
go vet ./...
golangci-lint run
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/preloadd
```
Expected: all green; gofmt prints nothing; linux build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/app/verify.go cmd/preloadd/main.go
git commit -m "feat(app): -verify subcommand reports page-cache residency"
```

---

### Task 13: End-to-end verification on the target server

**Files:**
- Create: `docs/phase1-verification.md` (the checklist + results)

This task has no unit test; it executes the four agreed success criteria against the real server (`outatime`). Record outputs in the doc.

- [ ] **Step 1: Build the Linux binary and copy it to the server**

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o bin/preloadd-linux ./cmd/preloadd
scp bin/preloadd-linux outatime:/boot/config/plugins/watch-aware-preloader/preloadd
scp config.example.toml outatime:/boot/config/plugins/watch-aware-preloader/config.toml
```
Then edit the remote `config.toml` to add the real Emby API key (Emby: Settings -> API Keys -> New). Confirm the `[[path_map]]` `from`/`to` match `docker inspect emby` (server reports `/share/...`, host is `/mnt/user/...`).

- [ ] **Step 2: Status visibility - run one verify sweep**

```bash
ssh outatime '/boot/config/plugins/watch-aware-preloader/preloadd -config /boot/config/plugins/watch-aware-preloader/config.toml -verify'
```
Expected: log lines showing per-item `preloaded` entries with tier/user, and a `verify sweep done` summary with a non-zero `preloaded` count. Record the output.

- [ ] **Step 3: Cache-hit verification**

Pick one preloaded file from the log. Confirm residency independently with `vmtouch` (or re-run `-verify`; the second pass should report those ranges `skipped` because they are resident):

```bash
ssh outatime 'vmtouch -v "/mnt/user/TV_Shows/<preloaded file>" | head'
```
Expected: a high resident percentage on the opening (and resume-offset) range. Record it.

- [ ] **Step 4: Measured start-time (off vs on)**

With the array disk spun down (`ssh outatime 'mdcmd spindown <N>'` or wait for spin-down), measure time-to-first-frame in Emby for a next-up title:
- Preloader OFF (kill the process): note the stall.
- Preloader ON (run `-verify` first to warm, then play): note the start time.

Record both. Expected: the ON case starts without the spin-up stall.

- [ ] **Step 5: Subjective feel + write up results**

Play several next-up / resume titles; confirm they start without the usual stall. Write all four results into `docs/phase1-verification.md`.

- [ ] **Step 6: Commit**

```bash
git add docs/phase1-verification.md
git commit -m "docs: Phase 1 end-to-end verification results on target server"
```

---

## Self-Review

**Spec coverage:**
- Media-server client (Emby): Tasks 7, 8. NextUp/Resume/Latest/Sessions covered.
- Scorer with tiers + dedupe + now-playing exclusion: Task 2; tiers assigned in Task 10.
- Resume-offset reads: Task 6 (`resumeOffsetBytes`, tested).
- Duration-based sizing from API bitrate: Task 6 (`HeadBytes`, tested).
- Path mapping: Task 3 (docker auto-detect deferred to Phase 2 by design - config rules cover Phase 1).
- RAM budget from available memory: Task 4 + wired in Task 11.
- mincore warm-detection + portable warming: Task 5.
- Event-driven + periodic backstop: Task 11 (`Loop`); websocket deferred to Phase 3 (fast session poll substitutes - documented).
- Config (TOML): Task 9.
- Success criteria (start-time, feel, status, cache-hit): Tasks 12 (verify) + 13 (E2E).
- Tier 4 (binge look-ahead) and Jellyfin: explicitly out of Phase 1 per spec phasing.

**Placeholder scan:** No TBD/TODO in steps; every code step shows full code. The single intentional "typo to catch" in Task 1 Step 3 is corrected in Step 4 (a deliberate TDD checkpoint, not a placeholder).

**Type consistency:** `core.MediaItem`/`core.PreloadTarget`/`core.Tier` used consistently. `Provider` interface in Task 10 matches the `emby.Client` methods built in Tasks 7-8. `preloader.Config` fields (`TargetSeconds`, `MinHeadBytes`, `MaxHeadBytes`, `TailBytes`) are consistent between Tasks 6, 11. `pagecache.Cache` signature identical across Tasks 5, 6, 12. `Daemon.budget()`/`Budget()` reconciled in Task 12.
