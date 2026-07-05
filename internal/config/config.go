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

// LibrariesConfig scopes preloading to specific media libraries.
type LibrariesConfig struct {
	Enabled []string `toml:"enabled"` // library IDs; empty => all libraries
}

// TierDial controls one preload signal tier. MaxItems caps how many items the
// tier contributes per user (0 = no cap).
type TierDial struct {
	Enabled  bool `toml:"enabled"`
	MaxItems int  `toml:"max_items"`
}

// TiersConfig holds the per-signal dials. The zero value (no [tiers] block)
// means every tier is enabled with no cap, preserving the pre-dials behavior;
// applyDefaults fills that in.
type TiersConfig struct {
	Resume        TierDial `toml:"resume"`
	NextUp        TierDial `toml:"next_up"`
	RecentlyAdded TierDial `toml:"recently_added"`
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
	// ProbeTimeout is a generous deadline for a single probe read. Unset/0 ->
	// 30s default. A negative value disables the guard (unbounded, pre-#17
	// behavior). A positive value must be >= 15s (above the array spin-up
	// window) so it never aborts a legitimate cold read.
	ProbeTimeout time.Duration `toml:"probe_timeout"`
}

// Config is the full preloadd configuration.
type Config struct {
	Server     ServerConfig    `toml:"server"`
	Users      UsersConfig     `toml:"users"`
	Libraries  LibrariesConfig `toml:"libraries"`
	Tiers      TiersConfig     `toml:"tiers"`
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
	c.applyDefaults(md.IsDefined("tiers"))
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

func (c *Config) applyDefaults(tiersDefined bool) {
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
		// Flat tail warmed for every non-resume tier, and for resume targets
		// whenever the container parser cannot locate the cue index (non-MKV or
		// parse failure). MKV resume targets warm the exact cue region instead
		// (see internal/container), so raising this default mainly affects the
		// non-resume and fallback paths.
		c.Preload.TailMB = 16
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
	if c.Residency.ProbeTimeout == 0 {
		c.Residency.ProbeTimeout = 30 * time.Second
	}
	if c.StatusPath == "" {
		c.StatusPath = "/var/local/preloadd/status.json"
	}
	if c.SecretPath == "" {
		c.SecretPath = "/boot/config/plugins/watch-aware-preloader/secrets.toml"
	}
	// No [tiers] block at all: enable every tier with no cap, matching the
	// pre-dials behavior. tiersDefined comes from the TOML metadata (not the
	// decoded value) so an operator who explicitly sets enabled=false for tiers
	// is honored rather than silently re-enabled.
	if !tiersDefined {
		c.Tiers = TiersConfig{
			Resume:        TierDial{Enabled: true},
			NextUp:        TierDial{Enabled: true},
			RecentlyAdded: TierDial{Enabled: true},
		}
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
	if c.Residency.ProbeTimeout > 0 && c.Residency.ProbeTimeout < 15*time.Second {
		return fmt.Errorf("residency.probe_timeout must be >= 15s (above the array spin-up window) or negative to disable, got %v", c.Residency.ProbeTimeout)
	}
	return nil
}
