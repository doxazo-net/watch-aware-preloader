package pagecache

import (
	"errors"
	"io"
	"time"
)

// timedRead reads up to n bytes from r and returns the wall-clock duration of
// the read, measured with the injected now clock. It stops at n bytes or EOF;
// a short read at EOF is not an error (a cached partial read is still fast).
// A non-EOF read error returns (0, err).
func timedRead(r io.Reader, n int64, now func() time.Time) (time.Duration, error) {
	start := now()
	const chunk = 64 << 10 // 64 KiB
	// Size the buffer to the smaller of n and the chunk cap so a small probe
	// (e.g. a tiny configured probe_bytes) doesn't allocate a full 64 KiB.
	bufSize := int64(chunk)
	if n < bufSize {
		bufSize = n
	}
	buf := make([]byte, bufSize)
	var read int64
	for read < n {
		want := n - read
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		m, err := r.Read(buf[:want])
		read += int64(m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		if m == 0 {
			break
		}
	}
	return now().Sub(start), nil
}
