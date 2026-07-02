package pagecache

import (
	"errors"
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
	c := New(1<<20, 150*time.Millisecond, 30*time.Second, nil)
	if err := c.Warm(p, 0, 4096); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	// Warming past EOF must not error (clamp to file size).
	if err := c.Warm(p, int64(len(data)-100), 4096); err != nil {
		t.Fatalf("Warm near EOF: %v", err)
	}
}

func TestWarmMissingFile(t *testing.T) {
	c := New(1<<20, 150*time.Millisecond, 30*time.Second, nil)
	if err := c.Warm("/no/such/file", 0, 10); err == nil {
		t.Error("expected error warming a missing file")
	}
}

func TestProbeWithTimeoutFastProbe(t *testing.T) {
	want := 42 * time.Millisecond
	wantErr := errors.New("probe boom")
	got, err := probeWithTimeout(time.Second, func() (time.Duration, error) {
		return want, wantErr
	})
	if got != want {
		t.Errorf("duration: got %v, want %v", got, want)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err: got %v, want %v", err, wantErr)
	}
}

func TestProbeWithTimeoutTimesOut(t *testing.T) {
	release := make(chan struct{})
	// Ensure the blocked probe goroutine is released so it does not linger past
	// the test (the production leak is intentional; the test cleans up).
	defer close(release)

	start := time.Now()
	got, err := probeWithTimeout(20*time.Millisecond, func() (time.Duration, error) {
		<-release
		return 99 * time.Second, nil
	})
	elapsed := time.Since(start)
	if !errors.Is(err, errProbeTimeout) {
		t.Errorf("err: got %v, want errProbeTimeout", err)
	}
	if got != 0 {
		t.Errorf("duration on timeout: got %v, want 0", got)
	}
	if elapsed > time.Second {
		t.Errorf("timeout did not return promptly: took %v", elapsed)
	}
}

func TestProbeWithTimeoutDisabled(t *testing.T) {
	want := 7 * time.Millisecond
	called := false
	got, err := probeWithTimeout(0, func() (time.Duration, error) {
		called = true
		return want, nil
	})
	if !called {
		t.Fatal("probe was not called when timeout disabled")
	}
	if got != want || err != nil {
		t.Errorf("got (%v, %v), want (%v, nil)", got, err, want)
	}
}
