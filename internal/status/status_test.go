package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sampleStatus() Status {
	return Status{
		SchemaVersion: SchemaVersion,
		LastRun:       "2026-07-01T04:12:33Z",
		Mode:          "once",
		DurationMs:    1840,
		OK:            true,
		BudgetBytes:   8 << 30,
		BytesWarmed:   2411724800,
		Preloaded:     33,
		Skipped:       4,
		Missing:       0,
		ByTier:        map[string]int{"resume": 2, "next_up": 5, "recently_added": 26},
		ByUser:        map[string]int{"3": 18, "7": 15},
	}
}

func TestWriteCreatesFileAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "status.json")
	if err := Write(path, sampleStatus()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	var got Status
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != SchemaVersion || got.Mode != "once" || got.Preloaded != 33 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.ByTier["recently_added"] != 26 || got.ByUser["7"] != 15 {
		t.Errorf("maps round-tripped wrong: %+v / %+v", got.ByTier, got.ByUser)
	}
	// The status file holds Emby UserIDs; it must stay owner-only (0600).
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

func TestWriteJSONKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := Write(path, sampleStatus()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"schema_version", "last_run", "mode", "duration_ms", "ok", "error",
		"budget_bytes", "bytes_warmed", "preloaded", "skipped", "missing",
		"by_tier", "by_user",
	} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing JSON key %q", k)
		}
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	if err := Write(path, sampleStatus()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "status.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir contents = %v, want exactly [status.json]", names)
	}
}

func TestWriteErrorsOnBadDir(t *testing.T) {
	// A path whose parent is an existing regular file cannot be MkdirAll'd.
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(file, "status.json") // parent is a file, not a dir
	if err := Write(path, sampleStatus()); err == nil {
		t.Error("expected error when parent path is a regular file")
	}
}
