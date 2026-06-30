package pagecache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWarmReadsRange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	data := make([]byte, 1<<20) // 1 MiB
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(1<<20, 150*time.Millisecond, nil)
	if err := c.Warm(p, 0, 4096); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	// Warming past EOF must not error (clamp to file size).
	if err := c.Warm(p, int64(len(data)-100), 4096); err != nil {
		t.Fatalf("Warm near EOF: %v", err)
	}
}

func TestWarmMissingFile(t *testing.T) {
	c := New(1<<20, 150*time.Millisecond, nil)
	if err := c.Warm("/no/such/file", 0, 10); err == nil {
		t.Error("expected error warming a missing file")
	}
}
