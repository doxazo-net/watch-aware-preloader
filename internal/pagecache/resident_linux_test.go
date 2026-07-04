//go:build linux

package pagecache

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests execute the real mmap + mincore residency path (residentByMincore)
// end-to-end, complementing the FUSE/timing coverage in resident_dispatch_test.go.
// They pin the residency mechanism by injecting statfs (newTestCache's fuse=false)
// so dispatch always routes to mincore regardless of which filesystem t.TempDir()
// happens to sit on; only then does the real mmap + mincore syscall run against a
// real temp file. They build and run on Linux only (mincore is Linux-only), which
// is the gap #6 closes: the residency path was previously validated by
// cross-compile only.

func TestResidentMincoreReportsUnwarmedRangeCold(t *testing.T) {
	// Negative control: a range never faulted into the cache reports zero resident,
	// proving the positive cases are not vacuously passing (e.g. a Resident that
	// always returned length). A sparse hole is used deliberately: its pages are
	// not brought into the page cache until accessed, whereas a file written via
	// os.WriteFile would already be resident and defeat the control.
	ps := int64(os.Getpagesize())
	p := filepath.Join(t.TempDir(), "sparse.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(16 * ps); err != nil { // 16 pages of hole, never written
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	c := newTestCache(t, false /*fuse*/, 0)
	resident, ok, err := c.Resident(p, 0, 8*ps)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if !ok {
		t.Fatal("Resident ok = false, want true (mincore path)")
	}
	if resident != 0 {
		t.Errorf("resident = %d, want 0 for an unwarmed sparse range", resident)
	}
}

func TestResidentMincoreReportsWarmedRange(t *testing.T) {
	ps := int64(os.Getpagesize())
	p := writeFile(t, t.TempDir(), int(16*ps))
	c := newTestCache(t, false /*fuse*/, 0)

	// Warm an aligned, multi-page range, then ask mincore how much is resident.
	length := 8 * ps
	if err := c.Warm(p, 0, length); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	resident, ok, err := c.Resident(p, 0, length)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if !ok {
		t.Fatal("Resident ok = false, want true on a non-FUSE filesystem (mincore path)")
	}
	if resident <= 0 || resident > length {
		t.Fatalf("resident = %d, want in (0, %d]", resident, length)
	}
	// Immediately after warming a small aligned range, eviction is not expected,
	// so the whole range should report resident.
	if resident != length {
		t.Errorf("resident = %d, want %d (full range resident right after warm)", resident, length)
	}
}

func TestResidentMincoreClampsUnalignedSubPageRange(t *testing.T) {
	// Regression guard for the per-page overlap clamp in residentByMincore: an
	// unaligned offset with a sub-page length forces the page-aligned mmap to span
	// more pages than the requested byte range. Each resident page is counted only
	// for its overlap with [offset, offset+length), so the total must never exceed
	// length. Drop that per-page clamping and this range would report a full
	// pageSize (or more), failing the assertion below.
	ps := int64(os.Getpagesize())
	p := writeFile(t, t.TempDir(), int(16*ps))
	c := newTestCache(t, false /*fuse*/, 0)

	offset := ps - 100 // straddles the boundary between the first two pages
	length := int64(200)
	if err := c.Warm(p, offset, length); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	resident, ok, err := c.Resident(p, offset, length)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if !ok {
		t.Fatal("Resident ok = false, want true (mincore path)")
	}
	if resident <= 0 || resident > length {
		t.Fatalf("resident = %d, want in (0, %d] (over-count means the clamp is gone)", resident, length)
	}
}

func TestResidentMincoreClampsUnalignedRangeAtEOF(t *testing.T) {
	// Near-EOF unaligned range: length runs past EOF, so residentByMincore first
	// truncates it to fi.Size()-offset, then the per-page overlap clamp keeps the
	// count within that truncated span. Covers the fi.Size() truncation path.
	ps := int64(os.Getpagesize())
	size := 16 * ps
	p := writeFile(t, t.TempDir(), int(size))
	c := newTestCache(t, false /*fuse*/, 0)

	offset := size - 100   // unaligned, in the final page
	length := int64(500)   // deliberately overruns EOF
	toEOF := size - offset // = 100; what the impl clamps to
	if err := c.Warm(p, offset, length); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	resident, ok, err := c.Resident(p, offset, length)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if !ok {
		t.Fatal("Resident ok = false, want true (mincore path)")
	}
	if resident <= 0 || resident > toEOF {
		t.Fatalf("resident = %d, want in (0, %d] (bytes to EOF)", resident, toEOF)
	}
}
