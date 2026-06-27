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

// validBase returns a Config that passes Validate, so each sub-test can flip a
// single field negative/zero and confirm it is the field under test that fails.
func validBase() *Config {
	c := &Config{}
	c.Server.Type = "emby"
	c.Server.URL = "http://h:8096"
	c.Server.APIKey = "k"
	c.Preload.RAMPercent = 50
	c.Preload.TargetSeconds = 20
	c.Schedule.SweepSeconds = 60
	c.Schedule.SessionPollSeconds = 5
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
