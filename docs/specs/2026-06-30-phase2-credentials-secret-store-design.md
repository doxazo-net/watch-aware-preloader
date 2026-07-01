# Phase 2 - Credentials Secret Store (Slice A) - Design

> Status: approved design of record. First slice of Phase 2 PR 2. Tracks issue #2;
> forward-compatible with issue #18 (per-user authentication). Date: 2026-06-30.
> Companion to `docs/specs/2026-06-30-phase2-settings-packaging-design.md` (section 5
> records the maintainer directive that credentials never live in `config.toml`).

## 1. Goal

Move the Emby API key out of `config.toml` into a separate, secret-only store, so
the Phase 2 settings UI writes non-secret settings to `config.toml` and credentials
to a store the engine reads. This is a Go-only foundation; the `.plg` packaging
(Slice B) and PHP settings page (Slice C) build on the credentials model finalized
here.

## 2. Scope

**In scope (this slice):**
1. New `internal/secrets` unit that loads the admin API key from a TOML secrets
   file, with an `EMBY_API_KEY` env-var override.
2. `config.toml` no longer carries `api_key`; the loader hard-errors if
   `server.api_key` is still present.
3. New `secret_path` config key (default on Unraid flash), overridable for tests.
4. `main` reads the key from the secret store and passes it to `emby.New`.
5. Deployment + docs updates (`outatime` key migration; README).

**Out of scope:** `.plg`, cron, PHP settings page, status panel (Slices B/C); the
per-user `AuthenticateByName` flow and UI sign-in (issue #18). The secrets file
*structure* reserves space for #18's per-user tokens but this slice reads only the
admin key.

## 3. Decisions (from brainstorming)

- **Store mechanism:** a separate TOML secrets file + `EMBY_API_KEY` env override.
  The engine reads it; the PHP settings page will write it in Slice C. A file (not
  env-only) is required because the settings UI must persist the credential; the
  env override serves dev/CI/headless.
- **Migration:** **hard error** if `api_key` is still present in `config.toml`
  (strictest enforcement of the "creds out of config.toml" directive).
- **Forward-compat (#18):** the secrets file reserves a `[users]` table keyed by
  the stable Emby `UserID` for future per-user tokens; not parsed for use yet.

## 4. `secrets.toml` structure

```toml
# secrets.toml - 0600, on Unraid at /boot/config/plugins/watch-aware-preloader/
# Credentials only. Never committed; never in config.toml.

[server]
api_key = "..."          # admin API key (read in this slice)

# Future (#18 - per-user authentication), NOT read yet:
# per-user access tokens keyed by the stable Emby UserID.
# [users]
# "3" = "eyJ..."
# "7" = "eyJ..."
```

- `[server].api_key` mirrors the old `config.toml` `[server]` table name - same key,
  different file.
- `[users]` keyed by `UserID` is reserved for #18; documented, not parsed for use in
  this slice (consistent with keying all user-scoped data on `UserID`, as
  `status.json`'s `by_user` already does).
- TOML per repo convention.

## 5. Config changes (`internal/config`)

- **Remove `APIKey` from `ServerConfig`** - `[server]` keeps only `type` and `url`.
  Drop the `api_key` required-field check from `Validate`.
- **Hard-error on a leftover key:** `Load` captures decode metadata
  (`md, err := toml.DecodeFile(path, &c)`) and inspects `md.Undecoded()`. If a
  `server.api_key` key is present, return:
  `fmt.Errorf("server.api_key must not be in config.toml; move it to the secrets file (%s) or the EMBY_API_KEY env var", c.SecretPath)`.
  Using `Undecoded()` (rather than a lingering struct field) keeps `api_key` fully
  out of the schema while still catching the stray key with a precise message.
  The check is scoped to `server.api_key` specifically, so unrelated unknown keys
  are not affected by this slice.
- **New `secret_path` key** (top-level, mirrors `status_path`): default
  `/boot/config/plugins/watch-aware-preloader/secrets.toml`, applied in
  `applyDefaults`, overridable for tests.

## 6. `internal/secrets` unit + wiring

New leaf package `internal/secrets` (stdlib + `BurntSushi/toml`):

```go
// Store is the on-disk secrets file. Users is reserved for issue #18 and is not
// consumed in this slice.
type Store struct {
    Server struct {
        APIKey string `toml:"api_key"`
    } `toml:"server"`
    Users map[string]string `toml:"users"`
}

// APIKey resolves the Emby admin API key. The EMBY_API_KEY env var wins when set
// (dev/CI/headless); otherwise the key is read from secretPath's [server].api_key.
// Returns an error when neither source yields a non-empty key, or when the file is
// required (no env var) but missing/unreadable.
func APIKey(secretPath string) (string, error)
```

Precedence: `EMBY_API_KEY` env > `secrets.toml` `[server].api_key`. If neither
yields a non-empty value:
`errors.New("no Emby API key found; set [server].api_key in <secret_path> or the EMBY_API_KEY env var")`.

Read-side does **not** enforce file permissions (that is the writer's job in Slice
C); it only reads. `os.Getenv` short-circuits before any file access, so dev/CI need
no secrets file.

**Wiring (`cmd/preloadd/main.go`):** after `config.Load`, call
`secrets.APIKey(cfg.SecretPath)`; on error, log and exit (same pattern as a bad
config load). Pass the returned key to `emby.New(cfg.Server.URL, key, nil)`. The
`emby` client and all downstream code are unchanged.

## 7. Testing

- `internal/secrets`: env-var override wins over file; file read returns
  `[server].api_key`; missing file with no env var -> error; empty key -> error;
  a file containing a `[users]` table parses without error (forward-compat). Uses
  `t.Setenv` and `t.TempDir`.
- `internal/config`: `Load` hard-errors when `server.api_key` is present; `Load`
  succeeds without it; `secret_path` default applied; `secret_path` override honored.
- No Unraid/host dependency; pure Go, runs in CI.

## 8. Deployment / migration (out-of-band, not code)

- On `outatime`: create `/boot/config/plugins/watch-aware-preloader/secrets.toml`
  (0600) with `[server].api_key`, and remove `api_key` from
  `/root/preloadd/config.toml`. Injected over ssh stdin; the key is never printed to
  any transcript or outward surface.
- The local dev `.env` (`EMBY_API_KEY=`) continues to work unchanged via the env
  override.
- README: document the secrets file, `secret_path`, and the env override; note that
  `config.toml` must not contain `api_key`.

## 9. Open items / forward references

- Per-user tokens (`[users]` table) are designed and parsed for use in issue #18.
- Slice B (`.plg` + cron) will seed `secrets.toml` (empty/placeholder) alongside the
  default `config.toml` and set 0600; Slice C (PHP settings page) writes the key
  into it. Both depend on the contract defined here.
