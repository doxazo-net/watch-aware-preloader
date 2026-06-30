//go:build linux

package pagecache

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestCache builds an osCache with injected statfs + clock so the FUSE
// branch can be exercised without a real fuse.shfs mount or real disk latency.
func newTestCache(t *testing.T, fuse bool, readLatency time.Duration) *osCache {
	t.Helper()
	clk := time.Unix(0, 0)
	c := &osCache{
		probeBytes: 1 << 20,
		threshold:  150 * time.Millisecond,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:        func() time.Time { return clk },
		statfs: func(string) (uint32, error) {
			if fuse {
				return fuseSuperMagic, nil
			}
			return 0xEF53 /* ext2/3/4 */, nil
		},
	}
	// Advance the injected clock by readLatency on each probe read by wrapping now.
	// Simpler: precompute elapsed by making now jump once per Resident call.
	c.now = advanceOnceClock(&clk, readLatency)
	return c
}

// advanceOnceClock returns a now() that, the first time it is called after a
// reset, returns the base time, and on the next call returns base+latency, so a
// start/end pair around a probe measures exactly latency.
func advanceOnceClock(clk *time.Time, latency time.Duration) func() time.Time {
	calls := 0
	base := *clk
	return func() time.Time {
		calls++
		if calls == 1 {
			return base
		}
		return base.Add(latency)
	}
}

func writeFile(t *testing.T, dir string, size int) string {
	t.Helper()
	p := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResidentFUSEFastProbeReportsCached(t *testing.T) {
	p := writeFile(t, t.TempDir(), 4<<20)
	c := newTestCache(t, true /*fuse*/, 2*time.Millisecond /*fast => cached*/)
	got, known, err := c.Resident(p, 0, 1<<20)
	if err != nil || !known {
		t.Fatalf("Resident: err=%v known=%v", err, known)
	}
	if got != 1<<20 {
		t.Errorf("resident = %d, want %d (fast probe => fully cached)", got, 1<<20)
	}
}

func TestResidentFUSESlowProbeReportsCold(t *testing.T) {
	p := writeFile(t, t.TempDir(), 4<<20)
	c := newTestCache(t, true /*fuse*/, 800*time.Millisecond /*slow => cold*/)
	got, known, err := c.Resident(p, 0, 1<<20)
	if err != nil || !known {
		t.Fatalf("Resident: err=%v known=%v", err, known)
	}
	if got != 0 {
		t.Errorf("resident = %d, want 0 (slow probe => cold)", got)
	}
}

func TestResidentByTimingRejectsNonPositiveProbeBytes(t *testing.T) {
	p := writeFile(t, t.TempDir(), 4<<20)
	c := newTestCache(t, true /*fuse*/, 2*time.Millisecond)
	c.probeBytes = 0
	resident, known, err := c.Resident(p, 0, 1<<20)
	if err == nil {
		t.Fatalf("Resident: expected error for probeBytes<=0, got nil (resident=%d known=%v)", resident, known)
	}
	if known {
		t.Errorf("Resident: known = true, want false (probeBytes<=0 must not report a known residency)")
	}
	if resident == 1<<20 {
		t.Errorf("Resident: resident = %d, must not equal the full requested length (would be reported as falsely fully cached)", resident)
	}
}

func TestMethodReflectsFilesystem(t *testing.T) {
	// FUSE detection is cached per directory (process-wide), so the two cases
	// must live in distinct directories - a single directory has one filesystem.
	fusePath := writeFile(t, t.TempDir(), 1<<20)
	if m := newTestCache(t, true, time.Millisecond).Method(fusePath); m != "timing" {
		t.Errorf("Method on FUSE = %q, want timing", m)
	}
	nonFusePath := writeFile(t, t.TempDir(), 1<<20)
	if m := newTestCache(t, false, time.Millisecond).Method(nonFusePath); m != "mincore" {
		t.Errorf("Method on non-FUSE = %q, want mincore", m)
	}
}
