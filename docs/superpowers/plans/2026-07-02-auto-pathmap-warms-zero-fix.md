# Auto Path-Map + Warms-Zero Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the sweep warm > 0 bytes on a standard Unraid setup by auto-deriving path rules, so an operator never has to hand-configure `path_map`.

**Architecture:** Two complementary rule sources merged at engine startup. (1) A host-agnostic *Unraid-UNC fallback* in `internal/pathmap` that rewrites `\\host\Share\rest` -> `/mnt/user/Share/rest` when no explicit rule matches - this alone fixes the live bug, where Emby reports item paths in UNC form. (2) *docker-inspect bind rules* (`/share/X` -> `/mnt/user/X`) for the container-path form, read via an injectable command runner. Manual `path_map` rules stay highest-precedence overrides. A read-only `preloadd detect-pathmaps` subcommand emits the effective rules as JSON for operator visibility and the future settings UI.

**Tech Stack:** Go 1.26+, stdlib only (`os/exec`, `encoding/json`, `log/slog`), no CGO, no third-party deps.

## Global Constraints

- **Go 1.26+**, `net/http` stdlib, `log/slog`; single static binary, no CGO, no runtime host deps.
- **No third-party imports** - stdlib only (`os/exec`, `encoding/json`).
- Auto-detection is **best-effort**: any docker/exec failure logs a warning and continues; it must NEVER abort or hang a sweep. All `exec.Command` calls use a bounded `context` timeout.
- **No secrets in output or logs.** `detect-pathmaps` uses docker only (no API key) and prints rules, never the key.
- **Privacy:** use only generic share names (`Movies`, `TV`, `Share`) in code, tests, and fixtures - never real personal share names.
- Lint BOTH platforms before push: local darwin + `GOOS=linux golangci-lint run ./...`.
- No emoji, no em-dashes in code/comments/commits.
- Target <= ~1000 hand-written LOC for the PR.

---

### Task 1: Host-agnostic Unraid-UNC fallback in the Mapper

**Files:**
- Modify: `internal/pathmap/pathmap.go`
- Test: `internal/pathmap/pathmap_test.go`

**Interfaces:**
- Consumes: existing `Mapper`, `New(rules []Rule) *Mapper`, `ToHost(serverPath string) (string, bool)`.
- Produces: `New(rules []Rule, opts ...Option) *Mapper` (variadic, backward-compatible); `Option`; `WithUnraidUNCFallback() Option`. When the fallback is enabled and no explicit rule matches a `\\host\Share\...` path, `ToHost` returns `/mnt/user/Share/...`, `true`.

- [ ] **Step 1: Write the failing test**

Add to `internal/pathmap/pathmap_test.go`:

```go
func TestUnraidUNCFallback(t *testing.T) {
	m := New(nil, WithUnraidUNCFallback())
	cases := []struct {
		in, want string
		ok       bool
	}{
		{`\\outatime\Movies\Film\a.mkv`, "/mnt/user/Movies/Film/a.mkv", true},
		{`\\OUTATIME\TV\Show\S01E01.mkv`, "/mnt/user/TV/Show/S01E01.mkv", true}, // host case-agnostic
		{`\\host\Share`, "/mnt/user/Share", true},                              // no trailing segment
		{`/mnt/user/Movies/a.mkv`, "", false},                                  // already host path, not UNC
		{`\\host`, "", false},                                                  // no share segment
	}
	for _, c := range cases {
		got, ok := m.ToHost(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ToHost(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestExplicitRuleBeatsFallback(t *testing.T) {
	m := New([]Rule{{From: `\\outatime\Movies`, To: "/mnt/disk1/Movies"}}, WithUnraidUNCFallback())
	got, ok := m.ToHost(`\\outatime\Movies\a.mkv`)
	if !ok || got != "/mnt/disk1/Movies/a.mkv" {
		t.Errorf("explicit rule should win: got (%q,%v)", got, ok)
	}
}

func TestFallbackDisabledByDefault(t *testing.T) {
	m := New(nil) // no option
	if got, ok := m.ToHost(`\\host\Share\a.mkv`); ok {
		t.Errorf("fallback must be opt-in; got (%q,%v)", got, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pathmap/ -run 'TestUnraidUNCFallback|TestExplicitRuleBeatsFallback|TestFallbackDisabledByDefault' -v`
Expected: FAIL - `undefined: WithUnraidUNCFallback`.

- [ ] **Step 3: Write minimal implementation**

In `internal/pathmap/pathmap.go`, add the option type and field, extend `New`, and add the fallback branch to `ToHost`:

```go
// Option configures a Mapper.
type Option func(*Mapper)

// WithUnraidUNCFallback rewrites a UNC path (`\\host\Share\rest`) that matches no
// explicit rule to the Unraid share root `/mnt/user/Share/rest`, host-agnostically.
// This encodes the Unraid SMB convention (an exported share `\\host\Share` is the
// same data as `/mnt/user/Share`) and is safe here because this binary only ever
// runs on an Unraid host.
func WithUnraidUNCFallback() Option {
	return func(m *Mapper) { m.uncFallback = true }
}
```

Add `uncFallback bool` to the `Mapper` struct. Change `New` to:

```go
func New(rules []Rule, opts ...Option) *Mapper {
	cp := make([]Rule, len(rules))
	for i, r := range rules {
		cp[i] = Rule{From: normalizePrefix(r.From), To: normalizePrefix(r.To)}
	}
	sort.SliceStable(cp, func(i, j int) bool {
		return len(cp[i].From) > len(cp[j].From)
	})
	m := &Mapper{rules: cp}
	for _, o := range opts {
		o(m)
	}
	return m
}
```

At the END of `ToHost`, replace the final `return "", false` with:

```go
	if m.uncFallback {
		if host, ok := unraidUNCToHost(serverPath); ok {
			return host, true
		}
	}
	return "", false
}

// unraidUNCToHost maps `\\host\Share\rest` -> `/mnt/user/Share/rest`, dropping the
// host component entirely (SMB share names are case-insensitive; the host varies).
// Returns false for non-UNC paths or a UNC path with no share segment.
func unraidUNCToHost(p string) (string, bool) {
	if !strings.HasPrefix(p, `\\`) {
		return "", false
	}
	norm := strings.ReplaceAll(p, `\`, "/")   // //host/Share/rest
	rest := strings.TrimPrefix(norm, "//")     // host/Share/rest
	parts := strings.SplitN(rest, "/", 2)      // [host, Share/rest]
	if len(parts) < 2 || parts[1] == "" {
		return "", false
	}
	return "/mnt/user/" + parts[1], true
}
```

Note: the `ToHost` early return `if len(m.rules) == 0 { return serverPath, true }` must still run BEFORE the fallback only when the fallback is OFF. Update that guard so an empty-rules Mapper WITH the fallback still evaluates UNC inputs:

```go
	if len(m.rules) == 0 && !m.uncFallback {
		return serverPath, true
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pathmap/ -v`
Expected: PASS (new tests plus all existing pathmap tests).

- [ ] **Step 5: Commit**

```bash
git add internal/pathmap/pathmap.go internal/pathmap/pathmap_test.go
git commit -m "feat(pathmap): host-agnostic Unraid-UNC fallback for \\\\host\\Share paths"
```

---

### Task 2: Parse `docker inspect` mounts into path rules

**Files:**
- Create: `internal/pathmap/detect.go`
- Test: `internal/pathmap/detect_test.go`
- Create (fixture): `internal/pathmap/testdata/docker_inspect.json`

**Interfaces:**
- Consumes: `pathmap.Rule`.
- Produces: `type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)`; `func BindRulesFromInspect(inspectJSON []byte) ([]Rule, error)`. Returns one Rule `{From: Destination, To: Source}` per `bind` mount whose `Source` is under `/mnt/` (media shares), skipping `/config`, `/tmp`, and non-bind mounts.

- [ ] **Step 1: Create the fixture**

`internal/pathmap/testdata/docker_inspect.json` (generic shares only):

```json
[
  {
    "Name": "/emby",
    "Mounts": [
      {"Type": "bind", "Source": "/mnt/user/Movies", "Destination": "/share/Movies"},
      {"Type": "bind", "Source": "/mnt/user/TV", "Destination": "/share/TV"},
      {"Type": "bind", "Source": "/mnt/vms/appdata/emby", "Destination": "/config"},
      {"Type": "bind", "Source": "/tmp", "Destination": "/tmp"},
      {"Type": "volume", "Source": "somevol", "Destination": "/vol"}
    ]
  }
]
```

- [ ] **Step 2: Write the failing test**

`internal/pathmap/detect_test.go`:

```go
package pathmap

import (
	"os"
	"reflect"
	"testing"
)

func TestBindRulesFromInspect(t *testing.T) {
	data, err := os.ReadFile("testdata/docker_inspect.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := BindRulesFromInspect(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{From: "/share/Movies", To: "/mnt/user/Movies"},
		{From: "/share/TV", To: "/mnt/user/TV"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBindRulesFromInspectEmpty(t *testing.T) {
	got, err := BindRulesFromInspect([]byte(`[{"Mounts":[]}]`))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v err %v, want empty", got, err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/pathmap/ -run TestBindRulesFromInspect -v`
Expected: FAIL - `undefined: BindRulesFromInspect`.

- [ ] **Step 4: Write minimal implementation**

`internal/pathmap/detect.go`:

```go
package pathmap

import (
	"context"
	"encoding/json"
	"strings"
)

// Runner executes an external command and returns its stdout. Injected so
// docker calls are testable without a docker daemon.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type inspectContainer struct {
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

// BindRulesFromInspect turns `docker inspect` JSON into path rules: one
// {From: container Destination, To: host Source} per bind mount rooted at
// /mnt/ (the Unraid array/user shares). /config, /tmp, and non-bind mounts
// are ignored - they are not media libraries.
func BindRulesFromInspect(inspectJSON []byte) ([]Rule, error) {
	var containers []inspectContainer
	if err := json.Unmarshal(inspectJSON, &containers); err != nil {
		return nil, err
	}
	var rules []Rule
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type != "bind" || !strings.HasPrefix(m.Source, "/mnt/") {
				continue
			}
			rules = append(rules, Rule{From: m.Destination, To: m.Source})
		}
	}
	return rules, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/pathmap/ -run TestBindRulesFromInspect -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/pathmap/detect.go internal/pathmap/detect_test.go internal/pathmap/testdata/docker_inspect.json
git commit -m "feat(pathmap): parse docker inspect bind mounts into path rules"
```

---

### Task 3: Find the media container and produce docker rules end-to-end

**Files:**
- Modify: `internal/pathmap/detect.go`
- Test: `internal/pathmap/detect_test.go`

**Interfaces:**
- Consumes: `Runner`, `BindRulesFromInspect`.
- Produces: `func DetectDockerRules(ctx context.Context, run Runner, imageSubstrings []string) ([]Rule, error)`. Runs `docker ps` to find the first container whose image contains any of `imageSubstrings` (default caller passes `["emby", "jellyfin"]`), then `docker inspect <name>` and returns `BindRulesFromInspect`. Returns `(nil, nil)` when no matching container is found (not an error - best-effort).

- [ ] **Step 1: Write the failing test**

Add to `internal/pathmap/detect_test.go`:

```go
func TestDetectDockerRules(t *testing.T) {
	inspect, _ := os.ReadFile("testdata/docker_inspect.json")
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch args[0] {
		case "ps":
			return []byte("emby emby/embyserver:beta\nsonarr linuxserver/sonarr\n"), nil
		case "inspect":
			if args[len(args)-1] != "emby" {
				t.Fatalf("inspected wrong container: %v", args)
			}
			return inspect, nil
		}
		return nil, nil
	}
	got, err := DetectDockerRules(context.Background(), run, []string{"emby", "jellyfin"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{From: "/share/Movies", To: "/mnt/user/Movies"},
		{From: "/share/TV", To: "/mnt/user/TV"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDetectDockerRulesNoContainer(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("sonarr linuxserver/sonarr\n"), nil
	}
	got, err := DetectDockerRules(context.Background(), run, []string{"emby", "jellyfin"})
	if err != nil || got != nil {
		t.Errorf("got %+v err %v, want nil,nil", got, err)
	}
}
```

Add `"context"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pathmap/ -run TestDetectDockerRules -v`
Expected: FAIL - `undefined: DetectDockerRules`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/pathmap/detect.go`:

```go
// DetectDockerRules finds the first running container whose image name contains
// one of imageSubstrings, inspects it, and returns its bind-mount rules. A
// missing container yields (nil, nil): auto-detection is best-effort.
func DetectDockerRules(ctx context.Context, run Runner, imageSubstrings []string) ([]Rule, error) {
	out, err := run(ctx, "docker", "ps", "--format", "{{.Names}} {{.Image}}")
	if err != nil {
		return nil, err
	}
	name := matchContainer(string(out), imageSubstrings)
	if name == "" {
		return nil, nil
	}
	inspect, err := run(ctx, "docker", "inspect", name)
	if err != nil {
		return nil, err
	}
	return BindRulesFromInspect(inspect)
}

// matchContainer returns the container name of the first "name image" line whose
// image contains any of subs (case-insensitive), or "".
func matchContainer(psOutput string, subs []string) string {
	for _, line := range strings.Split(strings.TrimSpace(psOutput), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		image := strings.ToLower(fields[1])
		for _, s := range subs {
			if strings.Contains(image, strings.ToLower(s)) {
				return fields[0]
			}
		}
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pathmap/ -v`
Expected: PASS (all pathmap tests).

- [ ] **Step 5: Commit**

```bash
git add internal/pathmap/detect.go internal/pathmap/detect_test.go
git commit -m "feat(pathmap): detect media container and derive docker path rules"
```

---

### Task 4: Wire auto-detection into engine startup (the warms-zero fix)

**Files:**
- Modify: `cmd/preloadd/main.go`
- Create: `cmd/preloadd/pathmap.go`
- Test: `cmd/preloadd/pathmap_test.go`

**Interfaces:**
- Consumes: `config.PathRule`, `pathmap.Rule`, `pathmap.DetectDockerRules`, `pathmap.New`, `pathmap.WithUnraidUNCFallback`, `pathmap.Runner`.
- Produces: `func buildMapper(ctx context.Context, manual []config.PathRule, run pathmap.Runner, log *slog.Logger) *pathmap.Mapper`. Merges manual rules (first, highest precedence) with best-effort docker rules, and always enables the Unraid-UNC fallback. Docker failure logs a warning and is skipped.

- [ ] **Step 1: Write the failing test**

`cmd/preloadd/pathmap_test.go`:

```go
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBuildMapperUsesUNCFallback(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, nil // no docker
	}
	m := buildMapper(context.Background(), nil, run, quietLog())
	got, ok := m.ToHost(`\\host\Movies\a.mkv`)
	if !ok || got != "/mnt/user/Movies/a.mkv" {
		t.Errorf("got (%q,%v), want (/mnt/user/Movies/a.mkv,true)", got, ok)
	}
}

func TestBuildMapperManualWins(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) { return nil, nil }
	manual := []config.PathRule{{From: `\\host\Movies`, To: "/mnt/disk1/Movies"}}
	m := buildMapper(context.Background(), manual, run, quietLog())
	got, ok := m.ToHost(`\\host\Movies\a.mkv`)
	if !ok || got != "/mnt/disk1/Movies/a.mkv" {
		t.Errorf("manual rule should win: got (%q,%v)", got, ok)
	}
}

func TestBuildMapperDockerFailureIsSoft(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	m := buildMapper(context.Background(), nil, run, quietLog())
	// still functional via UNC fallback despite docker error
	if _, ok := m.ToHost(`\\host\TV\x.mkv`); !ok {
		t.Error("mapper should still work when docker detection errors")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/preloadd/ -run TestBuildMapper -v`
Expected: FAIL - `undefined: buildMapper`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/preloadd/pathmap.go`:

```go
package main

import (
	"context"
	"log/slog"
	"os/exec"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
)

// execRunner runs a real command with a bounded timeout. It is the production
// pathmap.Runner; tests inject a fake.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// buildMapper composes the path Mapper: manual rules first (highest precedence),
// then best-effort docker-inspect rules, with the Unraid-UNC fallback always on.
// Docker detection never blocks a sweep - failures are logged and skipped.
func buildMapper(ctx context.Context, manual []config.PathRule, run pathmap.Runner, log *slog.Logger) *pathmap.Mapper {
	rules := make([]pathmap.Rule, 0, len(manual))
	for _, r := range manual {
		rules = append(rules, pathmap.Rule{From: r.From, To: r.To})
	}
	dockerRules, err := pathmap.DetectDockerRules(ctx, run, []string{"emby", "jellyfin"})
	if err != nil {
		log.Warn("docker path-map auto-detect failed; using manual rules + UNC fallback", "err", err)
	} else if len(dockerRules) > 0 {
		log.Info("docker path-map auto-detect", "rules", len(dockerRules))
		rules = append(rules, dockerRules...)
	}
	return pathmap.New(rules, pathmap.WithUnraidUNCFallback())
}
```

In `cmd/preloadd/main.go`, replace lines 83-86 (the manual `rules` loop) and the `pathmap.New(rules)` argument on line 93. Delete:

```go
	rules := make([]pathmap.Rule, 0, len(cfg.PathMap))
	for _, r := range cfg.PathMap {
		rules = append(rules, pathmap.Rule{From: r.From, To: r.To})
	}
```

and change the `preloader.New(...)` call to build the mapper via the helper:

```go
	mapper := buildMapper(context.Background(), cfg.PathMap, execRunner, log)
	pre := preloader.New(preCfg, pagecache.New(cfg.Residency.ProbeBytes, cfg.Residency.ProbeThreshold, cfg.Residency.ProbeTimeout, log), mapper, preloader.DefaultFS(), log)
```

The `pathmap` import stays (used in `pathmap.go`); remove it from `main.go`'s imports if `main.go` no longer references the package directly (the compiler will flag an unused import - drop it then).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/preloadd/ -v && go build ./...`
Expected: PASS and a clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/preloadd/pathmap.go cmd/preloadd/pathmap_test.go cmd/preloadd/main.go
git commit -m "fix(preloadd): auto-derive path maps so the sweep warms cache (docker + UNC fallback)"
```

---

### Task 5: `detect-pathmaps` subcommand (operator visibility, UI-ready)

**Files:**
- Modify: `cmd/preloadd/main.go`
- Create: `cmd/preloadd/detect.go`
- Test: `cmd/preloadd/detect_test.go`

**Interfaces:**
- Consumes: `pathmap.DetectDockerRules`, `pathmap.Runner`, `config.PathRule`.
- Produces: `func runDetectPathmaps(ctx context.Context, manual []config.PathRule, run pathmap.Runner, w io.Writer) error` - writes JSON `{"rules":[{"from","to","source"}],"unraid_unc_fallback":true}` where `source` is `manual` or `docker`. Dispatched when `os.Args[1] == "detect-pathmaps"`, before flag parsing.

- [ ] **Step 1: Write the failing test**

`cmd/preloadd/detect_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
)

func TestRunDetectPathmapsJSON(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "ps" {
			return []byte("emby emby/embyserver\n"), nil
		}
		return []byte(`[{"Mounts":[{"Type":"bind","Source":"/mnt/user/Movies","Destination":"/share/Movies"}]}]`), nil
	}
	manual := []config.PathRule{{From: "/x", To: "/mnt/user/x"}}
	var buf bytes.Buffer
	if err := runDetectPathmaps(context.Background(), manual, run, &buf); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Rules []struct{ From, To, Source string } `json:"rules"`
		UNC   bool                                `json:"unraid_unc_fallback"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if !out.UNC {
		t.Error("expected unraid_unc_fallback=true")
	}
	if len(out.Rules) != 2 || out.Rules[0].Source != "manual" || out.Rules[1].Source != "docker" {
		t.Errorf("unexpected rules: %+v", out.Rules)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/preloadd/ -run TestRunDetectPathmaps -v`
Expected: FAIL - `undefined: runDetectPathmaps`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/preloadd/detect.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
)

type ruleJSON struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source"`
}

// runDetectPathmaps writes the effective path rules (manual first, then
// docker-detected) as JSON. Read-only; no API key involved.
func runDetectPathmaps(ctx context.Context, manual []config.PathRule, run pathmap.Runner, w io.Writer) error {
	out := struct {
		Rules             []ruleJSON `json:"rules"`
		UnraidUNCFallback bool       `json:"unraid_unc_fallback"`
	}{UnraidUNCFallback: true}
	for _, r := range manual {
		out.Rules = append(out.Rules, ruleJSON{From: r.From, To: r.To, Source: "manual"})
	}
	dockerRules, err := pathmap.DetectDockerRules(ctx, run, []string{"emby", "jellyfin"})
	if err == nil {
		for _, r := range dockerRules {
			out.Rules = append(out.Rules, ruleJSON{From: r.From, To: r.To, Source: "docker"})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
```

In `cmd/preloadd/main.go`, add subcommand dispatch as the FIRST statement in `main()` (before flag definitions), so `preloadd detect-pathmaps` short-circuits the daemon flag flow:

```go
	if len(os.Args) > 1 && os.Args[1] == "detect-pathmaps" {
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		// Honor the subcommand's own -config flag (default config.toml) so
		// `preloadd detect-pathmaps -config <path>` matches the run modes.
		cfg, err := config.Load(configPathFromArgs(os.Args[2:]))
		var manual []config.PathRule
		if err != nil {
			log.Warn("config load failed; reporting docker-only rules", "err", err)
		} else {
			manual = cfg.PathMap
		}
		if err := runDetectPathmaps(context.Background(), manual, execRunner, os.Stdout); err != nil {
			log.Error("detect-pathmaps failed", "err", err)
			os.Exit(1)
		}
		return
	}
```

Note: `configPathFromArgs` parses the subcommand's own `-config` flag (defaulting to
`config.toml`); `rc.preloadd` invokes this from the plugin working dir where
`config.toml` exists. Keep it read-only and non-fatal on config errors so the UI can
still show docker rules pre-configuration.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/preloadd/ -v && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 5: Verify the subcommand runs**

Run: `go run ./cmd/preloadd detect-pathmaps` (in a dir without config.toml)
Expected: JSON with `"unraid_unc_fallback": true` and an empty or docker-only `rules` array; exit 0.

- [ ] **Step 6: Commit**

```bash
git add cmd/preloadd/detect.go cmd/preloadd/detect_test.go cmd/preloadd/main.go
git commit -m "feat(preloadd): detect-pathmaps subcommand emits effective rules as JSON"
```

---

### Task 6: Full gate + live acceptance on outatime

**Files:** none (verification only).

- [ ] **Step 1: Full local gate**

Run:
```bash
go test -count=1 ./...
gofmt -l . && go vet ./...
GOOS=linux golangci-lint run ./...
```
Expected: all pass, no output from `gofmt -l`.

- [ ] **Step 2: Live acceptance (maintainer-gated - do not run autonomously)**

This is the rendered-evidence sign-off. Requires a prerelease cut + install on outatime (release is maintainer-gated). Acceptance criteria:
- `preloadd detect-pathmaps` on the box lists `/share/<Share> -> /mnt/user/<Share>` docker rules and `unraid_unc_fallback: true`.
- A real one-shot sweep logs `preloaded > 0` and `bytes_warmed > 0` (previously `missing=N bytes_warmed=0`).
- `internal/status` `status.json` shows a non-zero warmed set.

Record the sweep log line (redact any real media titles) in the PR as the acceptance evidence.

- [ ] **Step 3: Open the PR**

Follow the repo PR gate (`/orchestrate:prep-pr` -> PR). Link: `Closes #31`. Note in the body that #32 (pickers) and the tier dials are follow-up PRs per the design spec. Do NOT push or open the PR without explicit maintainer go-ahead.

---

## Self-Review

**Spec coverage (design section 6 = this PR's scope):**
- 6.1 docker inspect rules -> Tasks 2, 3.
- 6.2 Unraid share-name/UNC convention -> Task 1 (implemented as a host-agnostic UNC fallback - simpler and more robust than per-share rules; container-path `/share/X` form is covered by the docker rules in Task 3).
- 6.3 resolution order (manual -> docker -> convention) -> Task 4 (`buildMapper`: manual rules first, docker appended, UNC fallback last-resort in `ToHost`).
- `detect-pathmaps` subcommand (design section 3) -> Task 5.
- Best-effort / bounded-timeout / no-hang constraint -> Task 4 (`execRunner` 10s timeout; soft docker failure) + Task 4 test `TestBuildMapperDockerFailureIsSoft`.
- Warms-0 fix acceptance -> Task 6 live criteria.
- Out of scope here (later PRs): user/library pickers (#32), tier dials, list-users/list-libraries subcommands, budget meter (#39). Correct per the sequenced delivery plan.

**Placeholder scan:** none - every code and test step has literal content.

**Type consistency:** `pathmap.Rule{From,To}`, `pathmap.Runner`, `pathmap.New(rules, ...Option)`, `WithUnraidUNCFallback()`, `DetectDockerRules(ctx, run, subs)`, `BindRulesFromInspect(json)`, `buildMapper(ctx, manual, run, log)`, `runDetectPathmaps(ctx, manual, run, w)` are used consistently across tasks; `config.PathRule{From,To}` matches `internal/config/config.go`.

**Known assumption (documented risk from spec section 10):** the UNC fallback assumes an Unraid host where `\\host\Share` == `/mnt/user/Share`. Valid because this is an Unraid-only plugin. Manual `path_map` rules override it for any non-standard layout.
