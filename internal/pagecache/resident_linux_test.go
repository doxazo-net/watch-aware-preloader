//go:build linux

package pagecache

import (
	"os"
	"testing"
)

// These tests execute the real mmap + mincore residency path (residentByMincore)
// end-to-end through the public Cache surface, complementing the injected-statfs
// FUSE/timing coverage in resident_dispatch_test.go. t.TempDir() lives on an
// ordinary (non-FUSE) filesystem, so New()'s real statfs routes Resident to
// mincore. They only build and run on Linux (mincore is Linux-only), which is the
// gap #6 closes: the residency path was previously validated by cross-compile only.

// newMincoreCache returns a real Cache. probeBytes/threshold/probeTimeout only
// affect the FUSE timing branch, which these tests never take, so their values
// are immaterial here.
func newMincoreCache() Cache { return New(1<<20, 0, 0, nil) }

func TestResidentMincoreReportsWarmedRange(t *testing.T) {
	ps := int64(os.Getpagesize())
	p := writeFile(t, t.TempDir(), int(16*ps))
	c := newMincoreCache()

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
	c := newMincoreCache()

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
	c := newMincoreCache()

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
