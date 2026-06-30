// Package pagecache warms and inspects the OS page cache for media files.
package pagecache

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"time"
)

// Cache warms byte ranges into the page cache and (on supported platforms)
// reports how much of a range is already resident.
type Cache interface {
	// Warm reads [offset, offset+length) so the kernel caches those pages.
	// Ranges past EOF are clamped. Returns an error only on open/read failure.
	Warm(path string, offset, length int64) error
	// Resident reports how many bytes of [offset, offset+length) are already in
	// the page cache. ok is false when residency cannot be determined on this
	// platform (callers should then warm unconditionally). On FUSE filesystems
	// the check is determined by a timed probe read, so it is not
	// side-effect-free: it warms the probed sample.
	Resident(path string, offset, length int64) (resident int64, ok bool, err error)
}

// Methoder optionally reports which residency mechanism a Cache uses for a path.
type Methoder interface {
	Method(path string) string
}

// New returns the platform Cache implementation. probeBytes and threshold tune
// the read-timing residency probe used on filesystems where mincore is blind
// (e.g. fuse.shfs); log receives the cold-probe latency diagnostic.
func New(probeBytes int64, threshold time.Duration, log *slog.Logger) Cache {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &osCache{
		probeBytes: probeBytes,
		threshold:  threshold,
		log:        log,
		now:        time.Now,
		statfs:     defaultStatfs,
	}
}

type osCache struct {
	probeBytes int64
	threshold  time.Duration
	log        *slog.Logger
	now        func() time.Time
	statfs     func(path string) (uint32, error)
}

func (c *osCache) Warm(path string, offset, length int64) error {
	if length <= 0 {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // path is operator-configured media, opening it is this package's purpose
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
	return residentImpl(c, path, offset, length)
}

// Method reports the residency mechanism for path: "mincore", "timing" (FUSE),
// or "unavailable" (no residency support on this platform).
func (c *osCache) Method(path string) string { return residencyMethod(c, path) }
