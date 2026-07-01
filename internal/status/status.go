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
// directory (0750) if needed, writes to a uniquely-named temp file in the same
// directory, fsyncs it, then renames it over path so a concurrent reader never
// observes a partial file. The fsync-before-rename ensures the temp file's data
// is durable before the rename publishes it, so a crash between write and rename
// cannot leave a truncated or empty status.json (the target's own filesystem
// still governs whether the rename itself survives a crash). The file keeps
// os.CreateTemp's 0600 mode. Any failure is returned; the caller treats it as
// non-fatal.
func Write(path string, s Status) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
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
	defer func() { _ = os.Remove(tmpName) }() // harmless no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp status file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temp status file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp status file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming status file: %w", err)
	}
	return nil
}
