// Package config loads and validates the preloadd TOML configuration.
package config

import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// ServerConfig holds the media-server connection parameters.
type ServerConfig struct {
	Type string `toml:"type"` // "emby" (Phase 1)
	URL  string `toml:"url"`
}

// UsersConfig specifies which users to preload for.
type UsersConfig struct {
	Enabled []string `toml:"enabled"` // user names; empty => all users
}

// PreloadConfig controls the preload budget and read-ahead sizes.
type PreloadConfig struct {
	RAMPercent    int   `toml:"ram_percent"`
	TargetSeconds int   `toml:"target_seconds"`
	MinHeadMB     int64 `toml:"min_head_mb"`
	MaxHeadMB     int64 `toml:"max_head_mb"`
	TailMB        int64 `toml:"tail_mb"`
}

// PathRule maps a media-server path prefix to the equivalent host path.
type PathRule struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

// ScheduleConfig controls polling and sweep intervals.
type ScheduleConfig struct {
	SweepSeconds       int `toml:"sweep_seconds"`
	SessionPollSeconds int `toml:"session_poll_seconds"`
}

// ResidencyConfig controls the read-timing probe used to detect page-cache
// residency on filesystems where mincore cannot (e.g. Unraid fuse.shfs).
type ResidencyConfig struct {
	ProbeBytes     int64         `toml:"probe_bytes"`     // fixed probe sample size
	ProbeThreshold time.Duration `toml:"probe_threshold"` // cached iff a probe read returns faster than this
}

// Config is the full preloadd configuration.
type Config struct {
	Server     ServerConfig    `toml:"server"`
	Users      UsersConfig     `toml:"users"`
	Preload    PreloadConfig   `toml:"preload"`
	PathMap    []PathRule      `toml:"path_map"`
	Schedule   ScheduleConfig  `toml:"schedule"`
	Residency  ResidencyConfig `toml:"residency"`
	StatusPath string          `toml:"status_path"` // where the engine writes status.json
	SecretPath string          `toml:"secret_path"` // where the engine reads the secrets file
}

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
	if c.Residency.ProbeBytes == 0 {
		c.Residency.ProbeBytes = 1 << 20
	}
	if c.Residency.ProbeThreshold == 0 {
		c.Residency.ProbeThreshold = 150 * time.Millisecond
	}
	if c.StatusPath == "" {
		c.StatusPath = "/var/local/preloadd/status.json"
	}
	if c.SecretPath == "" {
		c.SecretPath = "/boot/config/plugins/watch-aware-preloader/secrets.toml"
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
	if c.Preload.RAMPercent < 1 || c.Preload.RAMPercent > 100 {
		return fmt.Errorf("preload.ram_percent must be 1-100, got %d", c.Preload.RAMPercent)
	}
	if c.Preload.TargetSeconds < 1 {
		return fmt.Errorf("preload.target_seconds must be >= 1")
	}
	if c.Preload.MinHeadMB < 0 || c.Preload.MaxHeadMB < 0 || c.Preload.TailMB < 0 {
		return fmt.Errorf("preload min_head_mb, max_head_mb, and tail_mb must be >= 0")
	}
	if c.Preload.MaxHeadMB > 0 && c.Preload.MinHeadMB > c.Preload.MaxHeadMB {
		return fmt.Errorf("preload.min_head_mb (%d) must be <= preload.max_head_mb (%d)",
			c.Preload.MinHeadMB, c.Preload.MaxHeadMB)
	}
	if c.Schedule.SweepSeconds < 1 {
		return fmt.Errorf("schedule.sweep_seconds must be >= 1")
	}
	if c.Schedule.SessionPollSeconds < 1 {
		return fmt.Errorf("schedule.session_poll_seconds must be >= 1")
	}
	if c.Residency.ProbeBytes <= 0 {
		return fmt.Errorf("residency.probe_bytes must be > 0, got %d", c.Residency.ProbeBytes)
	}
	if c.Residency.ProbeThreshold <= 0 {
		return fmt.Errorf("residency.probe_threshold must be > 0, got %v", c.Residency.ProbeThreshold)
	}
	return nil
}
