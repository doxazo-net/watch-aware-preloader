//go:build linux

package pagecache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const fuseSuperMagic = 0x65735546 // FUSE_SUPER_MAGIC

// fuseDetectCache memoizes per-directory FUSE detection. A directory's
// filesystem type is a stable host property, so the cache is process-wide
// (shared across every osCache, e.g. the preloader's and -verify's) rather
// than per-instance.
var (
	fuseDetectMu    sync.Mutex
	fuseDetectCache = map[string]bool{}
)

// probePath opens path, seeks to offset, and times a read of up to n bytes.
//
// The read intentionally has no deadline: its wall-clock duration IS the
// residency signal, and a cold read on a spun-down array disk legitimately
// blocks for the spin-up window (seconds), so any short timeout would abort
// real cold reads. A genuinely wedged fuse.shfs mount would therefore stall the
// sweep; that is an accepted limitation (a wedged user-share is a host-level
// failure, and Go cannot cancel a blocking read on a regular file). A possible
// generous bounded probe is tracked in issue #17.
func (c *osCache) probePath(path string, offset, n int64) (time.Duration, error) {
	f, err := os.Open(path) //nolint:gosec // operator-configured media path
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return timedRead(f, n, c.now)
}

// defaultStatfs returns the filesystem type (f_type) for path.
func defaultStatfs(path string) (uint32, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint32(st.Type), nil //nolint:gosec // G115: f_type is a small filesystem magic constant (e.g. FUSE_SUPER_MAGIC); truncation is not a concern
}

// isFUSE reports whether path lives on a FUSE filesystem, caching the answer per
// containing directory to avoid repeated statfs within a sweep. The statfs
// syscall runs outside the lock; a statfs error is not cached.
func (c *osCache) isFUSE(path string) (bool, error) {
	key := filepath.Dir(path)
	fuseDetectMu.Lock()
	if v, ok := fuseDetectCache[key]; ok {
		fuseDetectMu.Unlock()
		return v, nil
	}
	fuseDetectMu.Unlock()

	ft, err := c.statfs(path)
	if err != nil {
		return false, err
	}
	fuse := ft == fuseSuperMagic
	fuseDetectMu.Lock()
	fuseDetectCache[key] = fuse
	fuseDetectMu.Unlock()
	return fuse, nil
}

// residencyMethod reports the mechanism Resident uses for path on Linux.
func residencyMethod(c *osCache, path string) string {
	fuse, err := c.isFUSE(path)
	if err != nil {
		return "mincore" // statfs failed; mincore is the default attempt
	}
	if fuse {
		return "timing"
	}
	return "mincore"
}

// residentImpl dispatches to mincore on ordinary filesystems and to a
// read-timing probe on FUSE, where mincore reports all-zero residency.
func residentImpl(c *osCache, path string, offset, length int64) (int64, bool, error) {
	if length <= 0 {
		return 0, true, nil
	}
	fuse, err := c.isFUSE(path)
	if err != nil {
		// statfs failed; fall back to mincore (the default attempt, matching
		// residencyMethod's "mincore" label) rather than erroring out.
		return residentByMincore(path, offset, length)
	}
	if fuse {
		return c.residentByTiming(path, offset, length)
	}
	return residentByMincore(path, offset, length)
}

// residentByTiming probes a fixed sample and classifies the whole range as fully
// resident (length) or cold (0). A cold classification logs a latency
// diagnostic; the elapsed value is a proxy (spin-up+seek+transfer) and never
// feeds skip/verify control logic.
//
// Unlike residentByMincore, this does not stat/clamp the range to the file
// size; callers are expected to pre-clamp offsets. The probe also physically
// reads probeBytes from disk, warming that sample as a side effect -- so,
// unlike mincore, residency checking on FUSE is not read-only.
func (c *osCache) residentByTiming(path string, offset, length int64) (int64, bool, error) {
	if c.probeBytes <= 0 {
		return 0, false, fmt.Errorf("pagecache: probeBytes must be > 0, got %d", c.probeBytes)
	}
	n := c.probeBytes
	if n > length {
		n = length
	}
	elapsed, err := c.probePath(path, offset, n)
	if err != nil {
		return 0, false, err
	}
	if classifyCached(elapsed, c.threshold) {
		return length, true, nil
	}
	c.log.Debug("cold probe", "path", path, "offset", offset, "elapsed_ms", elapsed.Milliseconds())
	return 0, true, nil
}

// residentByMincore mmaps the requested range and asks mincore which pages are
// resident, returning the resident byte count.
func residentByMincore(path string, offset, length int64) (int64, bool, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator-configured media, opening it is this package's purpose
	if err != nil {
		return 0, false, err
	}
	defer f.Close() //nolint:errcheck // read-only; close error not actionable

	fi, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	if offset >= fi.Size() {
		return 0, true, nil
	}
	if offset+length > fi.Size() {
		length = fi.Size() - offset
	}

	pageSize := int64(os.Getpagesize())
	// mmap must start on a page boundary; align the offset down and grow length.
	alignedOff := offset - (offset % pageSize)
	mmapLen := length + (offset - alignedOff)

	data, err := unix.Mmap(int(f.Fd()), alignedOff, int(mmapLen),
		unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = unix.Munmap(data) }()

	pages := (mmapLen + pageSize - 1) / pageSize
	vec := make([]byte, pages)
	// unix.Mincore is not available as a Go wrapper; call the syscall directly.
	_, _, errno := unix.Syscall(unix.SYS_MINCORE,
		uintptr(unsafe.Pointer(&data[0])), //nolint:gosec // G103: audited mincore syscall; mincore requires the mapped region address
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&vec[0]))) //nolint:gosec // G103: audited mincore syscall; kernel writes residency bits into vec
	if errno != 0 {
		return 0, false, errno
	}

	// Count only the overlap of each resident page with the requested range.
	// When offset is not page-aligned, the first and last pages extend beyond
	// [offset, offset+length), so adding a full pageSize per page overcounts.
	requestEnd := offset + length
	var resident int64
	for i, v := range vec {
		if v&0x1 == 0 { // low bit clear => page not resident
			continue
		}
		pageStart := alignedOff + int64(i)*pageSize
		pageEnd := pageStart + pageSize
		if pageStart < offset {
			pageStart = offset
		}
		if pageEnd > requestEnd {
			pageEnd = requestEnd
		}
		if pageStart < pageEnd {
			resident += pageEnd - pageStart
		}
	}
	return resident, true, nil
}
