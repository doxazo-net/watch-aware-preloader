package pagecache

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// advancingReader advances *clk by perRead on every Read call, simulating I/O
// latency without touching a real disk.
type advancingReader struct {
	data    []byte
	pos     int
	clk     *time.Time
	perRead time.Duration
	err     error // returned (after data exhausted) when set
}

func (r *advancingReader) Read(p []byte) (int, error) {
	*r.clk = r.clk.Add(r.perRead)
	if r.err != nil && r.pos >= len(r.data) {
		return 0, r.err
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestTimedReadMeasuresElapsed(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	r := &advancingReader{data: bytes.Repeat([]byte{0xAB}, 4096), clk: &clk, perRead: 10 * time.Millisecond}

	elapsed, err := timedRead(r, 4096, now)
	if err != nil {
		t.Fatalf("timedRead: %v", err)
	}
	if elapsed != 10*time.Millisecond {
		t.Errorf("elapsed = %v, want 10ms (one Read of the whole buffer)", elapsed)
	}
}

func TestTimedReadStopsAtN(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	// 1 MiB available, but only probe 4096 bytes: one Read suffices.
	r := &advancingReader{data: bytes.Repeat([]byte{1}, 1<<20), clk: &clk, perRead: 5 * time.Millisecond}

	elapsed, err := timedRead(r, 4096, now)
	if err != nil {
		t.Fatalf("timedRead: %v", err)
	}
	if elapsed != 5*time.Millisecond {
		t.Errorf("elapsed = %v, want 5ms (stopped at n=4096 after one Read)", elapsed)
	}
}

func TestTimedReadShortReadAtEOF(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	// Ask for 4096 but only 100 bytes exist; must classify on what was read.
	r := &advancingReader{data: bytes.Repeat([]byte{1}, 100), clk: &clk, perRead: 3 * time.Millisecond}

	elapsed, err := timedRead(r, 4096, now)
	if err != nil {
		t.Fatalf("timedRead at EOF should not error, got %v", err)
	}
	if elapsed < 3*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 3ms", elapsed)
	}
}

func TestTimedReadZeroN(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	// n == 0: probe nothing. The read loop must not run, so no bytes are
	// consumed and elapsed is zero (the clock never advances).
	r := &advancingReader{data: bytes.Repeat([]byte{1}, 4096), clk: &clk, perRead: 7 * time.Millisecond}

	elapsed, err := timedRead(r, 0, now)
	if err != nil {
		t.Fatalf("timedRead(n=0): %v", err)
	}
	if elapsed != 0 {
		t.Errorf("elapsed = %v, want 0 (no read performed)", elapsed)
	}
	if r.pos != 0 {
		t.Errorf("reader advanced by %d bytes, want 0 (n=0 must read nothing)", r.pos)
	}
}

func TestTimedReadPropagatesError(t *testing.T) {
	clk := time.Unix(0, 0)
	now := func() time.Time { return clk }
	wantErr := errors.New("boom")
	r := &advancingReader{data: nil, clk: &clk, perRead: time.Millisecond, err: wantErr}

	if _, err := timedRead(r, 4096, now); !errors.Is(err, wantErr) {
		t.Errorf("timedRead error = %v, want %v", err, wantErr)
	}
}
