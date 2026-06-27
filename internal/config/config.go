// Package config loads and validates the preloadd TOML configuration.
package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// ServerConfig holds the media-server connection parameters.
type ServerConfig struct {
	Type   string `toml:"type"` // "emby" (Phase 1)
	URL    string `toml:"url"`
	APIKey string `toml:"api_key"`
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
	if c.Preload.MinHeadMB < 0 || c.Preload.MaxHeadMB < 0 || c.Preload.TailMB < 0 {
		return fmt.Errorf("preload min_head_mb, max_head_mb, and tail_mb must be >= 0")
	}
	if c.Schedule.SweepSeconds < 1 {
		return fmt.Errorf("schedule.sweep_seconds must be >= 1")
	}
	if c.Schedule.SessionPollSeconds < 1 {
		return fmt.Errorf("schedule.session_poll_seconds must be >= 1")
	}
	return nil
}
