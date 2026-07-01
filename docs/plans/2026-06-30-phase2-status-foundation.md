# Phase 2 Status-Output Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `preloadd` emit a machine-readable JSON run summary (`status.json`) after every sweep, so a future PHP settings page can render last-run status without parsing logs.

**Architecture:** A new leaf package `internal/status` defines the JSON schema struct and an atomic writer (temp-file + rename). The preloader gains per-user run counts (`RunStats.ByUser`) alongside the existing `ByTier`. A single new app entry point, `SweepAndRecord`, wraps the existing `RunOnce` to time the sweep, build the status record, and write it - used by all three run modes (`-once`, `-verify`, `-daemon`) so status emission is uniform and `RunOnce` stays unchanged.

**Tech Stack:** Go 1.26+, stdlib only (`encoding/json`, `os`, `path/filepath`, `time`, `log/slog`). No new dependencies.

## Global Constraints

- Go 1.26+, stdlib only; no CGO, no new third-party deps.
- Logging via `log/slog`.
- `status.json` stores `last_run` as RFC3339 **UTC** (machine format); localization to US Pacific happens in the future UI, never in the engine.
- No per-item media titles in `status.json` (privacy; keeps the file small).
- Status writing is **best-effort / non-fatal**: a write failure logs at WARN and never turns a successful warm into a failed run.
- Atomic write only: temp file in the same directory, then `os.Rename` over the target.
- Default status path: `/var/local/preloadd/status.json` (tmpfs on Unraid; overridable via config for tests).
- `by_tier` keys are the snake_case labels `buildStatus` derives from `core.Tier.String()` (which is kebab-case, e.g. `next-up` -> `next_up`); `by_user` keys are raw Emby `UserID`.
- Commit messages end with: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Before any push: `GOOS=linux golangci-lint run ./...` (lint the Linux path).

---

### Task 1: Config `status_path` key

**Files:**
- Modify: `internal/config/config.go` (add field to `Config`, default in `applyDefaults`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `config.Config.StatusPath string` (TOML key `status_path`), defaulted to `/var/local/preloadd/status.json` when omitted.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestStatusPathDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.StatusPath != "/var/local/preloadd/status.json" {
		t.Errorf("StatusPath default = %q, want /var/local/preloadd/status.json", c.StatusPath)
	}
}

func TestStatusPathOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	// Prepend at the root: a bare key AFTER a [table] header would bind to
	// that table (schedule.status_path), not the root status_path.
	body := "status_path = \"/tmp/custom/status.json\"\n" + sample
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.StatusPath != "/tmp/custom/status.json" {
		t.Errorf("StatusPath = %q, want /tmp/custom/status.json", c.StatusPath)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestStatusPath -v`
Expected: FAIL - `c.StatusPath undefined (type Config has no field or method StatusPath)`.

- [ ] **Step 3: Add the field and default**

In `internal/config/config.go`, add a field to the `Config` struct (after the `Residency` line):

```go
// Config is the full preloadd configuration.
type Config struct {
	Server     ServerConfig    `toml:"server"`
	Users      UsersConfig     `toml:"users"`
	Preload    PreloadConfig   `toml:"preload"`
	PathMap    []PathRule      `toml:"path_map"`
	Schedule   ScheduleConfig  `toml:"schedule"`
	Residency  ResidencyConfig `toml:"residency"`
	StatusPath string          `toml:"status_path"` // where the engine writes status.json
}
```

In `applyDefaults()`, add (at the end, before the closing brace):

```go
	if c.StatusPath == "" {
		c.StatusPath = "/var/local/preloadd/status.json"
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (new tests plus all existing config tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add status_path key for the status.json output

Default /var/local/preloadd/status.json (tmpfs on Unraid); overridable
for tests. Foundation for the Phase 2 status panel.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Per-user run counts (`RunStats.ByUser`)

**Files:**
- Modify: `internal/preloader/preloader.go` (add `ByUser` field; populate in `Run`)
- Test: `internal/preloader/preloader_test.go`

**Interfaces:**
- Consumes: nothing from prior tasks.
- Produces: `preloader.RunStats.ByUser map[string]int` - counts of processed (preloaded or skipped-resident) items keyed by `MediaItem.UserID`. Mirrors `ByTier` exactly (both increment on the same two code paths; neither counts `Missing`).

- [ ] **Step 1: Write the failing test**

Add to `internal/preloader/preloader_test.go`:

```go
func TestRunByUserCounts(t *testing.T) {
	cache := &fakeCache{resident: -1} // nothing resident -> everything preloads
	fs := fakeFS{
		"/mnt/user/TV/a.mkv": 5 << 30,
		"/mnt/user/TV/b.mkv": 5 << 30,
		"/mnt/user/TV/c.mkv": 5 << 30,
	}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{
		{Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, UserID: "3"}, Tier: core.TierResume},
		{Item: core.MediaItem{ID: "b", ServerPath: "/mnt/user/TV/b.mkv", BitrateBps: 8_000_000, UserID: "3"}, Tier: core.TierNextUp},
		{Item: core.MediaItem{ID: "c", ServerPath: "/mnt/user/TV/c.mkv", BitrateBps: 8_000_000, UserID: "7"}, Tier: core.TierNextUp},
	}
	stats := p.Run(context.Background(), targets, 1<<40)

	if stats.Preloaded != 3 {
		t.Fatalf("Preloaded = %d, want 3", stats.Preloaded)
	}
	if got := stats.ByUser["3"]; got != 2 {
		t.Errorf("ByUser[3] = %d, want 2", got)
	}
	if got := stats.ByUser["7"]; got != 1 {
		t.Errorf("ByUser[7] = %d, want 1", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/preloader/ -run TestRunByUserCounts -v`
Expected: FAIL - `stats.ByUser undefined (type RunStats has no field or method ByUser)`.

- [ ] **Step 3: Add the field and populate it**

In `internal/preloader/preloader.go`, add `ByUser` to `RunStats`:

```go
// RunStats summarizes a preload pass.
type RunStats struct {
	Preloaded   int
	Skipped     int
	Missing     int
	BytesWarmed int64
	ByTier      map[core.Tier]int
	ByUser      map[string]int
	Warmed      []WarmedRange
}
```

In `Run`, initialize the map:

```go
	stats := RunStats{ByTier: map[core.Tier]int{}, ByUser: map[string]int{}}
```

Increment it at the two sites where `ByTier` is incremented. In the skip-resident branch:

```go
		if p.resident(hostPath, pl.offset, pl.head) && p.resident(hostPath, pl.tailOffset, pl.tail) {
			stats.Skipped++
			stats.ByTier[t.Tier]++
			stats.ByUser[t.Item.UserID]++
			continue
		}
```

And in the successful-preload block, alongside `stats.ByTier[t.Tier]++`:

```go
		stats.Preloaded++
		stats.BytesWarmed += cost
		stats.ByTier[t.Tier]++
		stats.ByUser[t.Item.UserID]++
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/preloader/ -v`
Expected: PASS (new test plus all existing preloader tests).

- [ ] **Step 5: Commit**

```bash
git add internal/preloader/preloader.go internal/preloader/preloader_test.go
git commit -m "feat(preloader): add per-user run counts (RunStats.ByUser)

Mirrors ByTier: increments on the preload and skip-resident paths,
keyed by MediaItem.UserID. Feeds status.json by_user.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `internal/status` package (schema + atomic writer)

**Files:**
- Create: `internal/status/status.go`
- Test: `internal/status/status_test.go`

**Interfaces:**
- Consumes: nothing (leaf package; stdlib only).
- Produces:
  - `status.SchemaVersion` (untyped int const, value `1`).
  - `status.Status` struct with JSON tags exactly as in the schema below.
  - `status.Write(path string, s Status) error` - atomically writes `s` as indented JSON to `path`, creating the parent dir; returns an error on failure (caller treats it as non-fatal).

- [ ] **Step 1: Write the failing test**

Create `internal/status/status_test.go`:

```go
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sampleStatus() Status {
	return Status{
		SchemaVersion: SchemaVersion,
		LastRun:       "2026-07-01T04:12:33Z",
		Mode:          "once",
		DurationMs:    1840,
		OK:            true,
		BudgetBytes:   8 << 30,
		BytesWarmed:   2411724800,
		Preloaded:     33,
		Skipped:       4,
		Missing:       0,
		ByTier:        map[string]int{"resume": 2, "next_up": 5, "recently_added": 26},
		ByUser:        map[string]int{"3": 18, "7": 15},
	}
}

func TestWriteCreatesFileAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "status.json")
	if err := Write(path, sampleStatus()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	var got Status
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != SchemaVersion || got.Mode != "once" || got.Preloaded != 33 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.ByTier["recently_added"] != 26 || got.ByUser["7"] != 15 {
		t.Errorf("maps round-tripped wrong: %+v / %+v", got.ByTier, got.ByUser)
	}
}

func TestWriteJSONKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := Write(path, sampleStatus()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"schema_version", "last_run", "mode", "duration_ms", "ok", "error",
		"budget_bytes", "bytes_warmed", "preloaded", "skipped", "missing",
		"by_tier", "by_user",
	} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing JSON key %q", k)
		}
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	if err := Write(path, sampleStatus()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "status.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir contents = %v, want exactly [status.json]", names)
	}
}

func TestWriteErrorsOnBadDir(t *testing.T) {
	// A path whose parent is an existing regular file cannot be MkdirAll'd.
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(file, "status.json") // parent is a file, not a dir
	if err := Write(path, sampleStatus()); err == nil {
		t.Error("expected error when parent path is a regular file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/status/ -v`
Expected: FAIL to build - `undefined: Status` / `undefined: Write` / `undefined: SchemaVersion`.

- [ ] **Step 3: Write the implementation**

Create `internal/status/status.go`:

```go
// Package status defines the machine-readable run summary that preloadd writes
// after each sweep for the settings page to read, plus an atomic file writer.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is the status.json schema version. Bump on any incompatible
// change so the reader can fail loud rather than mis-render an old file.
const SchemaVersion = 1

// Status is the summary of a single preload sweep, serialized to status_path.
// last_run is RFC3339 UTC; callers must not localize it here.
type Status struct {
	SchemaVersion int            `json:"schema_version"`
	LastRun       string         `json:"last_run"`
	Mode          string         `json:"mode"` // once | verify | daemon
	DurationMs    int64          `json:"duration_ms"`
	OK            bool           `json:"ok"`
	Error         string         `json:"error"`
	BudgetBytes   int64          `json:"budget_bytes"`
	BytesWarmed   int64          `json:"bytes_warmed"`
	Preloaded     int            `json:"preloaded"`
	Skipped       int            `json:"skipped"`
	Missing       int            `json:"missing"`
	ByTier        map[string]int `json:"by_tier"`
	ByUser        map[string]int `json:"by_user"`
}

// Write atomically writes s as indented JSON to path. It creates the parent
// directory (0755) if needed, writes to a uniquely-named temp file in the same
// directory, then renames it over path so a concurrent reader never observes a
// partial file. Any failure is returned; the caller treats it as non-fatal.
func Write(path string, s Status) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating status dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling status: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "status-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp status file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // harmless no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp status file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp status file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod status file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming status file: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/status/ -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
git add internal/status/
git commit -m "feat(status): add status.json schema + atomic writer

New leaf package: Status struct (schema_version 1) and an atomic
Write (temp file + rename, parent dir created, 0644). Non-fatal at
the call site.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Wire status writing into all run modes

**Files:**
- Create: `internal/app/record.go` (`SweepAndRecord` + `buildStatus`)
- Create: `internal/app/record_test.go`
- Modify: `internal/app/daemon.go` (`(*Daemon).sweep` calls `SweepAndRecord`)
- Modify: `cmd/preloadd/main.go` (`once` and `verify` cases call `SweepAndRecord`)

**Interfaces:**
- Consumes: `preloader.RunStats.ByUser` (Task 2); `status.Status`, `status.Write`, `status.SchemaVersion` (Task 3); `config.Config.StatusPath` (Task 1); existing `app.RunOnce`, `app.Provider`.
- Produces:
  - `app.SweepAndRecord(ctx context.Context, p Provider, enabled []string, pre *preloader.Preloader, budget int64, mode, statusPath string, log *slog.Logger) (preloader.RunStats, error)` - runs one sweep, writes status (best-effort), returns `RunOnce`'s `(stats, err)` unchanged.
  - `app.buildStatus(mode string, budget int64, dur time.Duration, stats preloader.RunStats, runErr error) status.Status` - pure mapping (unexported; tested in-package).

- [ ] **Step 1: Write the failing test**

Create `internal/app/record_test.go`:

```go
package app

import (
	"errors"
	"testing"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
	"github.com/sydlexius/watch-aware-preloader/internal/status"
)

func TestBuildStatusMapsFields(t *testing.T) {
	stats := preloader.RunStats{
		Preloaded:   3,
		Skipped:     1,
		Missing:     2,
		BytesWarmed: 1024,
		ByTier:      map[core.Tier]int{core.TierResume: 1, core.TierNextUp: 3},
		ByUser:      map[string]int{"3": 2, "7": 2},
	}
	s := buildStatus("once", 8<<30, 1500*time.Millisecond, stats, nil)

	if s.SchemaVersion != status.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", s.SchemaVersion, status.SchemaVersion)
	}
	if s.Mode != "once" || !s.OK || s.Error != "" {
		t.Errorf("mode/ok/error wrong: %+v", s)
	}
	if s.DurationMs != 1500 || s.BudgetBytes != 8<<30 || s.BytesWarmed != 1024 {
		t.Errorf("numeric fields wrong: %+v", s)
	}
	if s.ByTier["resume"] != 1 || s.ByTier["next_up"] != 3 {
		t.Errorf("ByTier keys not stringified: %+v", s.ByTier)
	}
	if s.ByUser["3"] != 2 || s.ByUser["7"] != 2 {
		t.Errorf("ByUser wrong: %+v", s.ByUser)
	}
}

func TestBuildStatusRecordsError(t *testing.T) {
	s := buildStatus("once", 0, time.Second, preloader.RunStats{}, errors.New("boom"))
	if s.OK {
		t.Error("OK should be false on error")
	}
	if s.Error != "boom" {
		t.Errorf("Error = %q, want boom", s.Error)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestBuildStatus -v`
Expected: FAIL - `undefined: buildStatus`.

- [ ] **Step 3: Write `record.go`**

Create `internal/app/record.go`:

```go
package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
	"github.com/sydlexius/watch-aware-preloader/internal/status"
)

// SweepAndRecord runs one sweep via RunOnce, times it, and writes the status
// file. It is the single sweep entry point for every run mode, so all modes
// emit status uniformly. The status write is best-effort: a failure is logged
// at WARN and never turns a successful warm into a failed run. RunOnce's stats
// and error are returned unchanged.
func SweepAndRecord(ctx context.Context, p Provider, enabled []string, pre *preloader.Preloader, budget int64, mode, statusPath string, log *slog.Logger) (preloader.RunStats, error) {
	start := time.Now()
	stats, runErr := RunOnce(ctx, p, enabled, pre, budget, log)
	s := buildStatus(mode, budget, time.Since(start), stats, runErr)
	if err := status.Write(statusPath, s); err != nil {
		log.Warn("writing status file failed", "path", statusPath, "err", err)
	}
	return stats, runErr
}

// buildStatus maps a RunStats plus run metadata into a status.Status. by_tier
// keys are stringified tier names; by_user keys are raw UserIDs. last_run is
// stamped in UTC.
func buildStatus(mode string, budget int64, dur time.Duration, stats preloader.RunStats, runErr error) status.Status {
	// Tier.String() is kebab-case (e.g. "next-up"); status.json uses snake_case
	// like its other keys, so convert hyphens to underscores here. Leave
	// Tier.String() untouched (it is shared log-output code).
	byTier := make(map[string]int, len(stats.ByTier))
	for tier, n := range stats.ByTier {
		byTier[strings.ReplaceAll(tier.String(), "-", "_")] = n
	}
	byUser := make(map[string]int, len(stats.ByUser))
	for id, n := range stats.ByUser {
		byUser[id] = n
	}
	s := status.Status{
		SchemaVersion: status.SchemaVersion,
		LastRun:       time.Now().UTC().Format(time.RFC3339),
		Mode:          mode,
		DurationMs:    dur.Milliseconds(),
		OK:            runErr == nil,
		BudgetBytes:   budget,
		BytesWarmed:   stats.BytesWarmed,
		Preloaded:     stats.Preloaded,
		Skipped:       stats.Skipped,
		Missing:       stats.Missing,
		ByTier:        byTier,
		ByUser:        byUser,
	}
	if runErr != nil {
		s.Error = runErr.Error()
	}
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -run TestBuildStatus -v`
Expected: PASS.

- [ ] **Step 5: Wire the daemon sweep**

In `internal/app/daemon.go`, change `(*Daemon).sweep` to route through `SweepAndRecord`:

```go
func (d *Daemon) sweep(ctx context.Context) {
	if _, err := SweepAndRecord(ctx, d.p, d.cfg.Users.Enabled, d.pre, d.budget(), "daemon", d.cfg.StatusPath, d.log); err != nil {
		d.log.Error("sweep failed", "err", err)
	}
}
```

- [ ] **Step 6: Wire the `once` and `verify` modes in main**

In `cmd/preloadd/main.go`, in the `verify` case, replace the `app.RunOnce(...)` call:

```go
	case "verify":
		budget := d.Budget()
		stats, verifyErr := app.SweepAndRecord(context.Background(), client, cfg.Users.Enabled, pre, budget, "verify", cfg.StatusPath, log)
		if verifyErr != nil {
			log.Error("verify sweep failed", "err", verifyErr)
			os.Exit(1)
		}
```

In the `once` case, replace the `app.RunOnce(...)` call:

```go
	case "once":
		stats, sweepErr := app.SweepAndRecord(context.Background(), client, cfg.Users.Enabled, pre, d.Budget(), "once", cfg.StatusPath, log)
		if sweepErr != nil {
			log.Error("sweep failed", "err", sweepErr)
			os.Exit(1)
		}
```

- [ ] **Step 7: Verify the whole build and test suite pass**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages; binary builds.

- [ ] **Step 8: Commit**

```bash
git add internal/app/record.go internal/app/record_test.go internal/app/daemon.go cmd/preloadd/main.go
git commit -m "feat(app): write status.json after every sweep (all modes)

SweepAndRecord wraps RunOnce to time the sweep, build the status
record, and write it best-effort; wired into once, verify, and daemon.
RunOnce stays pure.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Docs + sample config

**Files:**
- Modify: `config.toml.example` (add a `status_path` line, if the file exists)
- Modify: `README.md` (document the status file, if a config section exists)

**Interfaces:**
- Consumes: `config.Config.StatusPath` (Task 1).
- Produces: nothing code-facing; documentation only.

- [ ] **Step 1: Locate the sample config and README config section**

Run: `ls config.toml.example 2>/dev/null; grep -n "status_path\|\[residency\]\|\[schedule\]" config.toml.example README.md 2>/dev/null`
Expected: shows whether a sample config exists and where the config keys are documented. If `config.toml.example` does not exist, skip its edit; still add a README note if a config section exists.

- [ ] **Step 2: Add `status_path` to the sample config**

If `config.toml.example` exists, add near the other top-level/optional keys:

```toml
# Where preloadd writes its machine-readable run summary (read by the
# settings page). Defaults to /var/local/preloadd/status.json if omitted.
status_path = "/var/local/preloadd/status.json"
```

- [ ] **Step 3: Note it in the README**

If the README documents configuration, add a short bullet under the config keys:

```markdown
- `status_path` - path for the JSON run summary (last run, per-tier/per-user
  counts) the settings page reads. Default `/var/local/preloadd/status.json`.
```

- [ ] **Step 4: Verify docs build/format**

Run: `make fmt`
Expected: no Go changes (docs-only); command succeeds.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: document status_path config key

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/specs/2026-06-30-phase2-settings-packaging-design.md` section 4):
- 4.1 schema (all fields, key conventions, UTC, no titles) -> Task 3 `Status` struct + Task 4 `buildStatus`.
- 4.2 `internal/status` atomic writer -> Task 3. `RunStats.ByUser` in preloader -> Task 2. Wiring into once/verify/daemon + non-fatal -> Task 4.
- 4.3 `status_path` config default -> Task 1.
- 4.4 tests: status writer (atomicity, dir creation, bad-path error, key shape) -> Task 3; per-user aggregation -> Task 2; mapping/error -> Task 4. All pure Go, no host dep.
- Sections 2/3/5/6 are later-PR / informative and intentionally out of this plan.

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code. Task 5 is conditional on file existence (explicit skip guidance), not a placeholder.

**3. Type consistency:** `Status`/`SchemaVersion`/`Write` used in Task 4 match Task 3 definitions. `RunStats.ByUser map[string]int` (Task 2) consumed in Task 4 `buildStatus`. `config.Config.StatusPath` (Task 1) consumed in Task 4 wiring. `SweepAndRecord` signature identical across `record.go`, `daemon.go`, and `main.go` call sites. `buildStatus` signature matches its test.
