//go:build linux

package pagecache

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// residentImpl mmaps the requested range and asks mincore which pages are
// resident, returning the resident byte count.
func residentImpl(path string, offset, length int64) (int64, bool, error) {
	if length <= 0 {
		return 0, true, nil
	}
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
