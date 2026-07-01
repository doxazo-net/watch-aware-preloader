# Phase 2 Credentials Secret Store (Slice A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the Emby API key out of `config.toml` into a separate `secrets.toml` (with an `EMBY_API_KEY` env override), and hard-error if a key is still present in `config.toml`.

**Architecture:** A new leaf package `internal/secrets` resolves the API key from the env var or a TOML secrets file. `internal/config` drops `api_key` from its schema, adds a `secret_path` key, and hard-errors on a leftover `server.api_key` via decode metadata. `cmd/preloadd` reads the key from the secret store and passes it to the unchanged `emby` client.

**Tech Stack:** Go 1.26+, stdlib + `github.com/BurntSushi/toml` (already a dependency). No new deps.

## Global Constraints

- Go 1.26+, stdlib + existing deps only; no new third-party deps.
- Credentials must NEVER live in `config.toml`; the loader hard-errors if `server.api_key` is present.
- Secret precedence: `EMBY_API_KEY` env var wins; else `secrets.toml` `[server].api_key`.
- Default `secret_path`: `/boot/config/plugins/watch-aware-preloader/secrets.toml` (overridable for tests).
- The secrets file reserves a `[users]` table keyed by Emby `UserID` for issue #18; it is parsed (won't break) but not consumed in this slice.
- Never print any real API key to logs, transcripts, or outward surfaces.
- Commit messages end with: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Before any push: `GOOS=linux golangci-lint run ./...` (lint the Linux path).

---

### Task 1: `internal/secrets` package

**Files:**
- Create: `internal/secrets/secrets.go`
- Test: `internal/secrets/secrets_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces:
  - `secrets.Store` struct (TOML: `[server].api_key`, `[users]` map).
  - `secrets.APIKey(secretPath string) (string, error)` — returns `EMBY_API_KEY` if set; else reads `secretPath`'s `[server].api_key`; friendly error if neither yields a non-empty key or the file is missing.

- [ ] **Step 1: Write the failing tests**

Create `internal/secrets/secrets_test.go`:

```go
package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecrets(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secrets.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPIKeyEnvOverrideWins(t *testing.T) {
	t.Setenv("EMBY_API_KEY", "env-key")
	// Nonexistent path: env must win without touching the file.
	got, err := APIKey(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("APIKey: %v", err)
	}
	if got != "env-key" {
		t.Errorf("got %q, want env-key", got)
	}
}

func TestAPIKeyFromFile(t *testing.T) {
	t.Setenv("EMBY_API_KEY", "") // neutralize ambient override
	p := writeSecrets(t, "[server]\napi_key = \"file-key\"\n")
	got, err := APIKey(p)
	if err != nil {
		t.Fatalf("APIKey: %v", err)
	}
	if got != "file-key" {
		t.Errorf("got %q, want file-key", got)
	}
}

func TestAPIKeyMissingFileIsFriendlyError(t *testing.T) {
	t.Setenv("EMBY_API_KEY", "")
	_, err := APIKey(filepath.Join(t.TempDir(), "absent.toml"))
	if err == nil {
		t.Fatal("expected error for missing file with no env var")
	}
	if !strings.Contains(err.Error(), "no Emby API key found") {
		t.Errorf("error = %q, want it to mention 'no Emby API key found'", err)
	}
}

func TestAPIKeyEmptyKeyIsError(t *testing.T) {
	t.Setenv("EMBY_API_KEY", "")
	p := writeSecrets(t, "[server]\napi_key = \"\"\n")
	if _, err := APIKey(p); err == nil {
		t.Error("expected error for empty api_key")
	}
}

func TestAPIKeyUsersTableParses(t *testing.T) {
	t.Setenv("EMBY_API_KEY", "")
	p := writeSecrets(t, "[server]\napi_key = \"k\"\n\n[users]\n\"3\" = \"tok3\"\n\"7\" = \"tok7\"\n")
	got, err := APIKey(p)
	if err != nil {
		t.Fatalf("APIKey with [users] table: %v", err)
	}
	if got != "k" {
		t.Errorf("got %q, want k", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/secrets/ -v`
Expected: FAIL to build — `undefined: APIKey`.

- [ ] **Step 3: Write the implementation**

Create `internal/secrets/secrets.go`:

```go
// Package secrets loads preloadd credentials from a secret-only store (a TOML
// file or the EMBY_API_KEY env var), kept separate from config.toml so
// credentials never live in the main configuration file.
package secrets

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Store is the on-disk secrets file. Users is reserved for issue #18 (per-user
// access tokens keyed by Emby UserID) and is parsed but not consumed yet.
type Store struct {
	Server struct {
		APIKey string `toml:"api_key"`
	} `toml:"server"`
	Users map[string]string `toml:"users"`
}

// APIKey resolves the Emby admin API key. The EMBY_API_KEY env var wins when set
// (dev/CI/headless); otherwise the key is read from secretPath's [server].api_key.
// A missing file (with no env var) or an empty key yields a friendly error naming
// both sources.
func APIKey(secretPath string) (string, error) {
	if k := os.Getenv("EMBY_API_KEY"); k != "" {
		return k, nil
	}
	var s Store
	if _, err := toml.DecodeFile(secretPath, &s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", notFoundErr(secretPath)
		}
		return "", fmt.Errorf("reading secrets file %s: %w", secretPath, err)
	}
	if s.Server.APIKey == "" {
		return "", notFoundErr(secretPath)
	}
	return s.Server.APIKey, nil
}

func notFoundErr(secretPath string) error {
	return fmt.Errorf("no Emby API key found; set [server].api_key in %s or the EMBY_API_KEY env var", secretPath)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/secrets/ -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/
git commit -m "feat(secrets): load Emby API key from secrets.toml or EMBY_API_KEY

New leaf package: EMBY_API_KEY env wins, else [server].api_key from the
secrets file; friendly error naming both sources. [users] table reserved
for issue #18.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Config drops `api_key`, adds `secret_path`, hard-errors on leftover; rewire `main`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/preloadd/main.go`

**Interfaces:**
- Consumes: `secrets.APIKey(secretPath string) (string, error)` (Task 1).
- Produces: `config.Config.SecretPath string` (TOML `secret_path`, defaulted). `ServerConfig` no longer has `APIKey`. `config.Load` returns an error when `server.api_key` is present in the file.

> **Why config + main change together:** removing `APIKey` from `ServerConfig`
> breaks `main.go`'s `emby.New(..., cfg.Server.APIKey, ...)` at compile time, so
> both must land in one task to keep the build green.

- [ ] **Step 1: Write the failing tests**

In `internal/config/config_test.go`, first update the shared fixtures so they no
longer carry `api_key` (the new hard-error would otherwise fail every test that
uses them).

Edit the `sample` const — delete the `api_key` line (line 14):

```go
const sample = `
[server]
type = "emby"
url = "http://192.168.1.126:8096"

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
```

In `TestLoadValid`, drop the `APIKey` assertion — change the server check to:

```go
	if c.Server.URL != "http://192.168.1.126:8096" {
		t.Errorf("server parsed wrong: %+v", c.Server)
	}
```

In `TestValidateRejectsBadPercent`, delete the line `c.Server.APIKey = "k"`.

In `validBase()`, delete the line `c.Server.APIKey = "k"`.

In `TestResidencyDecodesDurationString`, delete the `api_key = "x"` line from its
inline `data` TOML (leave the `[server]` `type` and `url` lines).

Now add the new tests:

```go
func TestLoadRejectsAPIKeyInConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	body := "[server]\ntype = \"emby\"\nurl = \"http://h:8096\"\napi_key = \"leaked\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error: api_key must not be in config.toml")
	}
	if !strings.Contains(err.Error(), "server.api_key must not be in config.toml") {
		t.Errorf("error = %q, want it to mention server.api_key must not be in config.toml", err)
	}
}

func TestSecretPathDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.SecretPath != "/boot/config/plugins/watch-aware-preloader/secrets.toml" {
		t.Errorf("SecretPath default = %q", c.SecretPath)
	}
}

func TestSecretPathOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	body := "secret_path = \"/tmp/x/secrets.toml\"\n" + sample
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.SecretPath != "/tmp/x/secrets.toml" {
		t.Errorf("SecretPath = %q, want /tmp/x/secrets.toml", c.SecretPath)
	}
}
```

Add `"strings"` to the `config_test.go` imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestLoadRejectsAPIKeyInConfig|TestSecretPath' -v`
Expected: FAIL — `c.SecretPath undefined` and no hard-error yet.

- [ ] **Step 3: Edit `internal/config/config.go`**

Remove `APIKey` from `ServerConfig`:

```go
// ServerConfig holds the media-server connection parameters.
type ServerConfig struct {
	Type string `toml:"type"` // "emby" (Phase 1)
	URL  string `toml:"url"`
}
```

Add `SecretPath` to `Config` (after `StatusPath`):

```go
	StatusPath string          `toml:"status_path"` // where the engine writes status.json
	SecretPath string          `toml:"secret_path"` // where the engine reads the secrets file
```

In `applyDefaults()`, add (after the `StatusPath` default):

```go
	if c.SecretPath == "" {
		c.SecretPath = "/boot/config/plugins/watch-aware-preloader/secrets.toml"
	}
```

In `Validate()`, delete the `api_key` required block:

```go
	if c.Server.APIKey == "" {
		return fmt.Errorf("server.api_key is required")
	}
```

Rewrite `Load` to capture metadata and hard-error on a leftover `server.api_key`
(note `applyDefaults` runs before the check so `c.SecretPath` is set for the
message):

```go
// Load decodes a TOML config file, applies defaults, and validates it.
func Load(path string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}
	c.applyDefaults()
	for _, key := range md.Undecoded() {
		if key.String() == "server.api_key" {
			return nil, fmt.Errorf("server.api_key must not be in config.toml; move it to the secrets file (%s) or the EMBY_API_KEY env var", c.SecretPath)
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}
```

- [ ] **Step 4: Rewire `cmd/preloadd/main.go`**

Add the import `"github.com/sydlexius/watch-aware-preloader/internal/secrets"` to the
import block. Replace the `emby.New` wiring (currently
`client, err := emby.New(cfg.Server.URL, cfg.Server.APIKey, nil)`):

```go
	apiKey, err := secrets.APIKey(cfg.SecretPath)
	if err != nil {
		log.Error("loading API key failed", "err", err)
		os.Exit(1)
	}

	client, err := emby.New(cfg.Server.URL, apiKey, nil)
	if err != nil {
		log.Error("emby client init failed", "err", err)
		os.Exit(1)
	}
```

- [ ] **Step 5: Run the full build and test suite**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages (the new config tests, the updated fixtures, and the rest of the suite; `cmd/preloadd` builds with the new wiring).

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/preloadd/main.go
git commit -m "feat(config): move api_key out of config.toml to the secret store

Drop api_key from ServerConfig, add secret_path (default on Unraid flash),
hard-error via decode metadata if server.api_key is still present, and wire
main to read the key from internal/secrets.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Docs + example files

**Files:**
- Modify: `config.example.toml`
- Create: `secrets.example.toml`
- Modify: `README.md`

**Interfaces:**
- Consumes: `secret_path` (Task 2), the `secrets.toml` format (Task 1).
- Produces: nothing code-facing; documentation only.

- [ ] **Step 1: Update `config.example.toml`**

Remove the `api_key` line from the `[server]` table and add a pointer. The
`[server]` block becomes:

```toml
[server]
type = "emby"
url = "http://192.168.1.126:8096"
# The API key is NOT stored here. Put it in the secrets file (see
# secrets.example.toml) or the EMBY_API_KEY env var. A stray api_key in this
# file is a hard error at startup.
```

Add a commented pointer for the optional override near the top (after the header
comment, before `[server]`):

```toml
# Optional: override where the engine reads the secrets file.
# Defaults to /boot/config/plugins/watch-aware-preloader/secrets.toml.
# secret_path = "/boot/config/plugins/watch-aware-preloader/secrets.toml"
```

- [ ] **Step 2: Create `secrets.example.toml`**

```toml
# secrets.example.toml - copy to secrets.toml and set mode 0600.
# Credentials ONLY. Never commit secrets.toml; never put these in config.toml.
# On Unraid the default location is
# /boot/config/plugins/watch-aware-preloader/secrets.toml
# The EMBY_API_KEY env var, if set, overrides [server].api_key below.

[server]
api_key = "PUT-YOUR-EMBY-API-KEY-HERE"

# Future (issue #18 - per-user authentication), NOT read yet:
# per-user access tokens keyed by the Emby UserID.
# [users]
# "3" = "PER-USER-ACCESS-TOKEN"
```

- [ ] **Step 3: Update `README.md`**

If the README documents configuration or setup, add a short subsection (place it
near any existing config/setup content; otherwise under a `## Configuration`
heading):

```markdown
### Credentials

The Emby API key is a secret and is kept out of `config.toml`. Provide it either:

- in a secrets file (default `/boot/config/plugins/watch-aware-preloader/secrets.toml`,
  mode `0600`) under `[server].api_key` - see `secrets.example.toml`; or
- via the `EMBY_API_KEY` environment variable (which overrides the file).

`config.toml` must not contain `api_key`; the engine refuses to start if it does.
The secrets-file location can be overridden with the `secret_path` key in
`config.toml`.
```

- [ ] **Step 4: Verify formatting**

Run: `make fmt`
Expected: no Go changes (docs-only); command succeeds.

- [ ] **Step 5: Commit**

```bash
git add config.example.toml secrets.example.toml README.md
git commit -m "docs: document the secrets file and remove api_key from the example

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/specs/2026-06-30-phase2-credentials-secret-store-design.md`):
- Section 2.1 `internal/secrets` -> Task 1. 2.2 `config.toml` no api_key + hard-error -> Task 2. 2.3 `secret_path` key -> Task 2. 2.4 `main` reads from store -> Task 2 Step 4. 2.5 deployment/docs -> Task 3 (the `outatime` migration itself is out-of-band per spec section 8, not a code task).
- Section 4 secrets structure -> Task 1 `Store` + Task 3 `secrets.example.toml`.
- Section 5 config changes -> Task 2. Section 6 unit + wiring -> Tasks 1 & 2. Section 7 testing -> Tasks 1 & 2 tests.
- Forward-compat `[users]` table -> Task 1 `Store.Users` + `TestAPIKeyUsersTableParses` + example file.

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code. `README.md` step is conditional on existing structure but gives the exact heading/content to add. `secrets.example.toml` uses an obvious placeholder token, not a real key.

**3. Type consistency:** `secrets.APIKey(secretPath string) (string, error)` defined in Task 1, consumed in Task 2 Step 4. `config.Config.SecretPath` defined in Task 2, used by Task 2's `main` wiring and referenced by Task 3 docs. `ServerConfig` loses `APIKey` in Task 2; the only consumer (`main.go`) is rewired in the same task. `sample` fixture edit in Task 2 is applied before the new tests rely on it.
