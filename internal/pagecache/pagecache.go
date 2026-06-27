// Package pagecache warms and inspects the OS page cache for media files.
package pagecache

import (
	"errors"
	"io"
	"os"
)

// Cache warms byte ranges into the page cache and (on supported platforms)
// reports how much of a range is already resident.
type Cache interface {
	// Warm reads [offset, offset+length) so the kernel caches those pages.
	// Ranges past EOF are clamped. Returns an error only on open/read failure.
	Warm(path string, offset, length int64) error
	// Resident reports how many bytes of [offset, offset+length) are already in
	// the page cache. ok is false when residency cannot be determined on this
	// platform (callers should then warm unconditionally).
	Resident(path string, offset, length int64) (resident int64, ok bool, err error)
}

// New returns the platform Cache implementation.
func New() Cache { return &osCache{} }

type osCache struct{}

func (c *osCache) Warm(path string, offset, length int64) error {
	if length <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only; close error not actionable

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 1<<20) // 1 MiB chunks
	remaining := length
	for remaining > 0 {
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		read, err := f.Read(buf[:n])
		remaining -= int64(read)
		if errors.Is(err, io.EOF) {
			return nil // clamped at EOF
		}
		if err != nil {
			return err
		}
		if read == 0 {
			return nil
		}
	}
	return nil
}

func (c *osCache) Resident(path string, offset, length int64) (int64, bool, error) {
	return residentImpl(path, offset, length)
}
