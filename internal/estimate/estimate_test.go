package estimate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "estimate.json")
	e := Estimate{
		SchemaVersion:      SchemaVersion,
		GeneratedAt:        "2026-07-10T21:05:00Z",
		BudgetBytes:        16 << 30,
		CeilingPerUserTier: 200,
		Rows: []Row{
			{U: "3", T: "resume", L: "L1", B: 41155072, R: 0},
			{U: "7", T: "next-up", L: "", B: 38000000, R: 1},
		},
		Meta: Meta{TargetSeconds: 20, RAMPercent: 50, ItemCount: 2, CeilingTruncated: false},
	}
	if err := Write(path, e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got Estimate
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != SchemaVersion || got.BudgetBytes != e.BudgetBytes {
		t.Errorf("header round-trip wrong: %+v", got)
	}
	if len(got.Rows) != 2 || got.Rows[0].U != "3" || got.Rows[0].B != 41155072 || got.Rows[1].L != "" {
		t.Errorf("rows round-tripped wrong: %+v", got.Rows)
	}
	// Anonymized keys must be short (u/t/l/b/r), and no id/title keys present.
	if !contains(string(b), `"u": "3"`) || contains(string(b), `"id"`) || contains(string(b), `"name"`) {
		t.Errorf("json keys not anonymized as expected:\n%s", b)
	}
}

func TestWriteAtomicPerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "estimate.json") // parent dir must be created
	if err := Write(path, Estimate{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", fi.Mode().Perm())
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
