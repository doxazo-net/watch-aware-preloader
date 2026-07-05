package preloader

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/doxazo-net/watch-aware-preloader/internal/container"
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/pathmap"
)

// fakeCache records Warm calls and reports nothing resident.
type fakeCache struct {
	warmed   []warmCall
	resident int64
	warmErr  error // returned by Warm when set
}
type warmCall struct {
	path           string
	offset, length int64
}

func (f *fakeCache) Warm(path string, offset, length int64) error {
	f.warmed = append(f.warmed, warmCall{path, offset, length})
	return f.warmErr
}
func (f *fakeCache) Resident(_ string, _, length int64) (int64, bool, error) {
	if f.resident < 0 {
		return 0, false, nil // residency unknown
	}
	return f.resident, true, nil
}

type fakeFS map[string]int64 // path -> size

func (m fakeFS) Stat(path string) (int64, error) {
	sz, ok := m[path]
	if !ok {
		return 0, io.EOF // stand-in for "not found"
	}
	return sz, nil
}

func testCfg() Config {
	return Config{TargetSeconds: 20, MinHeadBytes: 8 << 20, MaxHeadBytes: 250 << 20, TailBytes: 1 << 20}
}

func TestHeadBytesDurationBased(t *testing.T) {
	// 25 Mbps over 20s = 25e6/8*20 = 62.5 MB, within clamp.
	it := core.MediaItem{BitrateBps: 25_000_000}
	got := HeadBytes(testCfg(), it)
	want := int64(20) * 25_000_000 / 8
	if got != want {
		t.Errorf("HeadBytes = %d, want %d", got, want)
	}
}

func TestHeadBytesClampsLow(t *testing.T) {
	it := core.MediaItem{BitrateBps: 1_000_000} // 20s = 2.5MB < 8MB floor
	if got := HeadBytes(testCfg(), it); got != 8<<20 {
		t.Errorf("HeadBytes = %d, want floor 8MiB", got)
	}
}

func TestHeadBytesFallbackToSizeOverRuntime(t *testing.T) {
	// 600 MiB over 20 min => ~4.2 Mbps => 20s head ~10 MiB, above the 8 MiB floor.
	// A fallback that silently clamps to MinHeadBytes would return exactly 8 MiB and fail this check.
	cfg := testCfg()
	it := core.MediaItem{SizeBytes: 600 << 20, Runtime: 20 * time.Minute}
	got := HeadBytes(cfg, it)
	if got <= cfg.MinHeadBytes {
		t.Fatalf("HeadBytes = %d, want strictly > MinHeadBytes (%d); fallback may be clamping to floor", got, cfg.MinHeadBytes)
	}
}

func TestRunSkipsMissingAndBudgets(t *testing.T) {
	cache := &fakeCache{resident: -1} // unknown residency => always warm
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))

	targets := []core.PreloadTarget{
		{Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 25_000_000}, Tier: core.TierNextUp},
		{Item: core.MediaItem{ID: "missing", ServerPath: "/mnt/user/TV/none.mkv", BitrateBps: 25_000_000}, Tier: core.TierNextUp},
	}
	// Budget only fits one head + tail.
	budget := HeadBytes(testCfg(), targets[0].Item) + testCfg().TailBytes + 1
	stats := p.Run(context.Background(), targets, budget)

	if stats.Preloaded != 1 {
		t.Errorf("Preloaded = %d, want 1", stats.Preloaded)
	}
	if stats.Missing != 1 {
		t.Errorf("Missing = %d, want 1", stats.Missing)
	}
	if len(cache.warmed) == 0 || cache.warmed[0].path != "/mnt/user/TV/a.mkv" {
		t.Errorf("expected warm of a.mkv, got %+v", cache.warmed)
	}
}

func TestRunResumeUsesOffset(t *testing.T) {
	cache := &fakeCache{resident: -1}
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, ResumeOffset: 10 * time.Minute},
		Tier: core.TierResume,
	}}
	p.Run(context.Background(), targets, 1<<40)
	// offset = 600s * 8e6/8 = 600 * 1e6 = 600_000_000 bytes
	if cache.warmed[0].offset != 600_000_000 {
		t.Errorf("resume offset = %d, want 600000000", cache.warmed[0].offset)
	}
}

func TestRunResumeOffsetBitrateFallback(t *testing.T) {
	cache := &fakeCache{resident: -1}
	fs := fakeFS{"/mnt/user/TV/a.mkv": 600 << 20}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// No BitrateBps: bitrate must be derived from SizeBytes/Runtime, else the
	// resume item wrongly warms from the file head (offset 0).
	it := core.MediaItem{
		ID: "a", ServerPath: "/mnt/user/TV/a.mkv",
		SizeBytes: 600 << 20, Runtime: 20 * time.Minute, ResumeOffset: 10 * time.Minute,
	}
	p.Run(context.Background(), []core.PreloadTarget{{Item: it, Tier: core.TierResume}}, 1<<40)
	if len(cache.warmed) == 0 {
		t.Fatal("expected a warm call")
	}
	// bps = 600MiB/1200s*8; offset = 600s * bps/8 = 300MiB.
	if want := int64(300 << 20); cache.warmed[0].offset != want {
		t.Errorf("resume offset = %d, want %d (bitrate fallback)", cache.warmed[0].offset, want)
	}
}

func TestRunTailOverlapNotDoubleCharged(t *testing.T) {
	cache := &fakeCache{resident: -1} // always warm
	const size = 5 << 20
	fs := fakeFS{"/m/a.mkv": size}
	// Head clamps to 4MiB; tail (2MiB) would start at 3MiB, overlapping the head
	// window [0,4MiB), so it must clamp to [4MiB,5MiB) = 1MiB, not a full 2MiB.
	cfg := Config{TargetSeconds: 20, MinHeadBytes: 1 << 20, MaxHeadBytes: 4 << 20, TailBytes: 2 << 20}
	p := New(cfg, cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/m/a.mkv", BitrateBps: 1_000_000_000},
		Tier: core.TierNextUp,
	}}
	stats := p.Run(context.Background(), targets, 1<<40)
	if want := int64(5 << 20); stats.BytesWarmed != want {
		t.Errorf("BytesWarmed = %d, want %d (overlapping tail must not be double-charged)", stats.BytesWarmed, want)
	}
	if len(cache.warmed) != 2 {
		t.Fatalf("want 2 warm calls (head+tail), got %d: %+v", len(cache.warmed), cache.warmed)
	}
	if cache.warmed[1].offset != 4<<20 || cache.warmed[1].length != 1<<20 {
		t.Errorf("tail warm = offset %d len %d, want offset %d len %d",
			cache.warmed[1].offset, cache.warmed[1].length, 4<<20, 1<<20)
	}
}

func TestRunSmallFileWarmsTail(t *testing.T) {
	cache := &fakeCache{resident: -1} // always warm
	const size = 3 << 20
	fs := fakeFS{"/m/a.mkv": size}
	// File (3MiB) is below TailBytes (4MiB) and the head clamps to 2MiB, stopping
	// before EOF; the [2MiB,3MiB) suffix must still be warmed (regression: the old
	// `size > TailBytes` guard left it cold).
	cfg := Config{TargetSeconds: 20, MinHeadBytes: 1 << 20, MaxHeadBytes: 2 << 20, TailBytes: 4 << 20}
	p := New(cfg, cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/m/a.mkv", BitrateBps: 1_000_000_000},
		Tier: core.TierNextUp,
	}}
	stats := p.Run(context.Background(), targets, 1<<40)
	if len(cache.warmed) != 2 {
		t.Fatalf("want 2 warm calls (head+tail), got %d: %+v", len(cache.warmed), cache.warmed)
	}
	if cache.warmed[1].offset != 2<<20 || cache.warmed[1].length != 1<<20 {
		t.Errorf("tail warm = offset %d len %d, want offset %d len %d",
			cache.warmed[1].offset, cache.warmed[1].length, 2<<20, 1<<20)
	}
	if want := int64(3 << 20); stats.BytesWarmed != want {
		t.Errorf("BytesWarmed = %d, want %d", stats.BytesWarmed, want)
	}
}

func TestRunResumeOffsetOnlyForResumeTier(t *testing.T) {
	cache := &fakeCache{resident: -1}
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Same item as TestRunResumeUsesOffset but Tier=TierNextUp; offset must NOT be applied.
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, ResumeOffset: 10 * time.Minute},
		Tier: core.TierNextUp,
	}}
	p.Run(context.Background(), targets, 1<<40)
	if len(cache.warmed) == 0 {
		t.Fatal("expected at least one Warm call")
	}
	if cache.warmed[0].offset != 0 {
		t.Errorf("warm offset = %d, want 0 (resume offset must not apply to non-resume tier)", cache.warmed[0].offset)
	}
}

func TestRunWarmedRangesPopulated(t *testing.T) {
	cache := &fakeCache{resident: -1}
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	cfg := testCfg()
	p := New(cfg, cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	item := core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 25_000_000}
	targets := []core.PreloadTarget{
		{Item: item, Tier: core.TierNextUp},
	}
	stats := p.Run(context.Background(), targets, 1<<40)

	if len(stats.Warmed) != 1 {
		t.Fatalf("Warmed = %v, want 1 entry", stats.Warmed)
	}
	want := WarmedRange{
		Path:   "/mnt/user/TV/a.mkv",
		Offset: 0,
		Length: HeadBytes(cfg, item),
	}
	if stats.Warmed[0] != want {
		t.Errorf("Warmed[0] = %+v, want %+v", stats.Warmed[0], want)
	}
}

func TestRunWarmErrorNotCountedPreloaded(t *testing.T) {
	cache := &fakeCache{resident: -1, warmErr: io.ErrUnexpectedEOF}
	fs := fakeFS{"/mnt/user/TV/a.mkv": 5 << 30}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 25_000_000},
		Tier: core.TierNextUp,
	}}
	stats := p.Run(context.Background(), targets, 1<<40)
	if stats.Preloaded != 0 {
		t.Errorf("Preloaded = %d, want 0 when Warm returns an error", stats.Preloaded)
	}
}

func TestRunResumeWarmsFrontAndExactCueTail(t *testing.T) {
	cache := &fakeCache{resident: -1} // always warm
	const size = int64(20 << 30)
	fs := fakeFS{"/mnt/user/4K/a.mkv": size}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Inject a layout: front metadata ends at 200 KiB; cue index starts 8 MiB
	// before EOF (a long-film cue index the flat 1 MiB tail would miss).
	const frontEnd = int64(200 << 10)
	cueStart := size - (8 << 20)
	p.inspect = func(_ string, _ int64) (container.Layout, bool) {
		return container.Layout{FrontEnd: frontEnd, CueStart: cueStart}, true
	}
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/4K/a.mkv", BitrateBps: 80_000_000, ResumeOffset: 30 * time.Minute},
		Tier: core.TierResume,
	}}
	p.Run(context.Background(), targets, 1<<40)

	// Expect three warm calls: front [0,200KiB), head at the resume offset, tail [cueStart,EOF).
	if len(cache.warmed) != 3 {
		t.Fatalf("want 3 warm calls (front+head+tail), got %d: %+v", len(cache.warmed), cache.warmed)
	}
	front := cache.warmed[0]
	if front.offset != 0 || front.length != frontEnd {
		t.Errorf("front warm = offset %d len %d, want offset 0 len %d", front.offset, front.length, frontEnd)
	}
	tail := cache.warmed[2]
	if tail.offset != cueStart || tail.length != size-cueStart {
		t.Errorf("cue tail = offset %d len %d, want offset %d len %d", tail.offset, tail.length, cueStart, size-cueStart)
	}
}

func TestRunResumeFallsBackToFlatTailOnParseFailure(t *testing.T) {
	cache := &fakeCache{resident: -1}
	const size = int64(5 << 30)
	fs := fakeFS{"/mnt/user/TV/a.mkv": size}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.inspect = func(_ string, _ int64) (container.Layout, bool) {
		return container.Layout{}, false // parse failure -> flat tail, no front
	}
	targets := []core.PreloadTarget{{
		Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, ResumeOffset: 10 * time.Minute},
		Tier: core.TierResume,
	}}
	p.Run(context.Background(), targets, 1<<40)
	// No front warm; head first, flat 1 MiB tail second.
	if len(cache.warmed) != 2 {
		t.Fatalf("want 2 warm calls (head+flat tail), got %d: %+v", len(cache.warmed), cache.warmed)
	}
	if cache.warmed[1].length != testCfg().TailBytes {
		t.Errorf("fallback tail len = %d, want flat %d", cache.warmed[1].length, testCfg().TailBytes)
	}
}

func TestRunByUserCounts(t *testing.T) {
	cache := &fakeCache{resident: -1} // nothing resident -> everything preloads
	fs := fakeFS{
		"/mnt/user/TV/a.mkv": 5 << 30,
		"/mnt/user/TV/b.mkv": 5 << 30,
		"/mnt/user/TV/c.mkv": 5 << 30,
	}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{
		{Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, UserID: "3"}, Tier: core.TierResume},
		{Item: core.MediaItem{ID: "b", ServerPath: "/mnt/user/TV/b.mkv", BitrateBps: 8_000_000, UserID: "3"}, Tier: core.TierNextUp},
		{Item: core.MediaItem{ID: "c", ServerPath: "/mnt/user/TV/c.mkv", BitrateBps: 8_000_000, UserID: "7"}, Tier: core.TierNextUp},
	}
	stats := p.Run(context.Background(), targets, 1<<40)

	if stats.Preloaded != 3 {
		t.Fatalf("Preloaded = %d, want 3", stats.Preloaded)
	}
	if got := stats.ByUser["3"]; got != 2 {
		t.Errorf("ByUser[3] = %d, want 2", got)
	}
	if got := stats.ByUser["7"]; got != 1 {
		t.Errorf("ByUser[7] = %d, want 1", got)
	}
}

func TestRunByUserCountsSkipResident(t *testing.T) {
	cache := &fakeCache{resident: 1 << 40} // everything fully resident -> skip branch
	fs := fakeFS{
		"/mnt/user/TV/a.mkv": 5 << 30,
		"/mnt/user/TV/b.mkv": 5 << 30,
		"/mnt/user/TV/c.mkv": 5 << 30,
	}
	p := New(testCfg(), cache, pathmap.New(nil), fs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	targets := []core.PreloadTarget{
		{Item: core.MediaItem{ID: "a", ServerPath: "/mnt/user/TV/a.mkv", BitrateBps: 8_000_000, UserID: "3"}, Tier: core.TierResume},
		{Item: core.MediaItem{ID: "b", ServerPath: "/mnt/user/TV/b.mkv", BitrateBps: 8_000_000, UserID: "3"}, Tier: core.TierNextUp},
		{Item: core.MediaItem{ID: "c", ServerPath: "/mnt/user/TV/c.mkv", BitrateBps: 8_000_000, UserID: "7"}, Tier: core.TierNextUp},
	}
	stats := p.Run(context.Background(), targets, 1<<40)

	if stats.Skipped != len(targets) {
		t.Fatalf("Skipped = %d, want %d", stats.Skipped, len(targets))
	}
	if stats.Preloaded != 0 {
		t.Fatalf("Preloaded = %d, want 0", stats.Preloaded)
	}
	if got := stats.ByUser["3"]; got != 2 {
		t.Errorf("ByUser[3] = %d, want 2", got)
	}
	if got := stats.ByUser["7"]; got != 1 {
		t.Errorf("ByUser[7] = %d, want 1", got)
	}
}
