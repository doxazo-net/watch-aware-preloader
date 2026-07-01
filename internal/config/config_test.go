package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestLoadValid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.URL != "http://192.168.1.126:8096" {
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
	c.Preload.RAMPercent = 150
	if err := c.Validate(); err == nil {
		t.Error("expected error for ram_percent > 100")
	}
}

// validBase returns a Config that passes Validate, so each sub-test can flip a
// single field negative/zero and confirm it is the field under test that fails.
func validBase() *Config {
	c := &Config{}
	c.Server.Type = "emby"
	c.Server.URL = "http://h:8096"
	c.Preload.RAMPercent = 50
	c.Preload.TargetSeconds = 20
	c.Schedule.SweepSeconds = 60
	c.Schedule.SessionPollSeconds = 5
	c.Residency.ProbeBytes = 1 << 20
	c.Residency.ProbeThreshold = 150 * time.Millisecond
	return c
}

func TestValidateRejectsNegativePreloadSizes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Config)
	}{
		{"min_head_mb", func(c *Config) { c.Preload.MinHeadMB = -1 }},
		{"max_head_mb", func(c *Config) { c.Preload.MaxHeadMB = -1 }},
		{"tail_mb", func(c *Config) { c.Preload.TailMB = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validBase()
			tc.mut(c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected error for negative %s", tc.name)
			}
		})
	}
}

func TestValidateRejectsInvertedHeadBounds(t *testing.T) {
	c := validBase()
	c.Preload.MinHeadMB = 100
	c.Preload.MaxHeadMB = 50
	if err := c.Validate(); err == nil {
		t.Error("expected error for min_head_mb > max_head_mb (min would be silently meaningless)")
	}
}

func TestValidateRejectsNonPositiveIntervals(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Config)
	}{
		{"sweep_seconds", func(c *Config) { c.Schedule.SweepSeconds = -1 }},
		{"session_poll_seconds", func(c *Config) { c.Schedule.SessionPollSeconds = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validBase()
			tc.mut(c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected error for non-positive %s (panics time.NewTicker)", tc.name)
			}
		})
	}
}

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
		{"probe_bytes <= 0", func(c *Config) { c.Residency.ProbeBytes = -1 }},
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
