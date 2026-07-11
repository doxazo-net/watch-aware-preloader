// Package estimate defines the machine-readable warm-set projection preloadd
// writes in -estimate mode for the settings page to render a budget meter, plus
// an atomic file writer. Rows are anonymized (no item ID or title): the settings
// page re-aggregates them client-side, so they must never carry library item
// identity.
package estimate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is the estimate.json schema version. Bump on any incompatible
// change so the reader can fail loud rather than mis-render an old file.
const SchemaVersion = 1

// Row is one anonymized projected candidate. Keys are deliberately short and
// carry no identity: u=userID, t=tier label, l=libraryID ("" if unattributable),
// b=projected bytes, r=global rank index (0 = highest priority).
type Row struct {
	U string `json:"u"`
	T string `json:"t"`
	L string `json:"l"`
	B int64  `json:"b"`
	R int    `json:"r"`
}

// Meta carries the reference inputs the estimate was computed under.
type Meta struct {
	TargetSeconds    int  `json:"target_seconds"`
	RAMPercent       int  `json:"ram_percent"`
	ItemCount        int  `json:"item_count"`
	CeilingTruncated bool `json:"ceiling_truncated"`
}

// Estimate is the full projection written to estimate.json. generated_at is
// RFC3339 UTC; callers must not localize it here.
type Estimate struct {
	SchemaVersion      int    `json:"schema_version"`
	GeneratedAt        string `json:"generated_at"`
	BudgetBytes        int64  `json:"budget_bytes"`
	CeilingPerUserTier int    `json:"ceiling_per_user_tier"`
	Rows               []Row  `json:"rows"`
	Meta               Meta   `json:"meta"`
}

// Write atomically writes e as indented JSON to path (parent dir 0750, temp file
// + fsync + rename, 0600 mode), mirroring internal/status.Write so a concurrent
// reader never observes a partial file. Any failure is returned; callers treat
// it as non-fatal.
func Write(path string, e Estimate) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating estimate dir: %w", err)
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling estimate: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "estimate-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp estimate file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // harmless no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp estimate file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temp estimate file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp estimate file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming estimate file: %w", err)
	}
	return nil
}
