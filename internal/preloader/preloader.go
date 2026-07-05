// Package preloader warms the page cache for a ranked list of targets within a
// byte budget, sizing each read by playback duration.
package preloader

import (
	"context"
	"log/slog"
	"os"

	"github.com/doxazo-net/watch-aware-preloader/internal/container"
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/pagecache"
	"github.com/doxazo-net/watch-aware-preloader/internal/pathmap"
)

// Safety caps for the parsed resume regions, so a bogus SeekHead pointer or
// large front attachments can never warm an unbounded amount.
const (
	maxFrontBytes = 16 << 20 // cap the front-metadata window
	maxTailBytes  = 64 << 20 // cap the cue tail window
)

// Config controls duration-based sizing and the tail read.
type Config struct {
	TargetSeconds int
	MinHeadBytes  int64
	MaxHeadBytes  int64
	TailBytes     int64
}

// FS abstracts file metadata for testability.
type FS interface {
	Stat(path string) (size int64, err error)
}

type osFS struct{}

func (osFS) Stat(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// DefaultFS returns an FS backed by the real filesystem.
func DefaultFS() FS { return osFS{} }

// WarmedRange is a byte range that was warmed into the page cache during a run.
type WarmedRange struct {
	Path   string
	Offset int64
	Length int64
}

// RunStats summarizes a preload pass.
type RunStats struct {
	Preloaded   int
	Skipped     int
	Missing     int
	BytesWarmed int64
	ByTier      map[core.Tier]int
	ByUser      map[string]int
	Warmed      []WarmedRange
}

// Preloader executes preload passes.
type Preloader struct {
	cfg     Config
	cache   pagecache.Cache
	mapper  *pathmap.Mapper
	fs      FS
	log     *slog.Logger
	inspect func(path string, size int64) (container.Layout, bool)
}

// New builds a Preloader.
func New(cfg Config, cache pagecache.Cache, mapper *pathmap.Mapper, fs FS, log *slog.Logger) *Preloader {
	return &Preloader{cfg: cfg, cache: cache, mapper: mapper, fs: fs, log: log, inspect: container.Inspect}
}

// ToHost maps a server-reported path to its host path via the configured path
// rules, reporting whether it mapped. Exposed so the sweep can reuse the same
// normalization for the library-scope filter.
func (p *Preloader) ToHost(serverPath string) (string, bool) {
	return p.mapper.ToHost(serverPath)
}

// HeadBytes computes the duration-based head size for an item, clamped.
func HeadBytes(cfg Config, it core.MediaItem) int64 {
	bps := it.BitrateBps
	if bps <= 0 && it.Runtime > 0 {
		bps = int64(float64(it.SizeBytes) / it.Runtime.Seconds() * 8)
	}
	want := int64(cfg.TargetSeconds) * bps / 8
	if want < cfg.MinHeadBytes {
		want = cfg.MinHeadBytes
	}
	if want > cfg.MaxHeadBytes {
		want = cfg.MaxHeadBytes
	}
	return want
}

// Run warms targets in order until the budget is exhausted.
func (p *Preloader) Run(ctx context.Context, targets []core.PreloadTarget, budgetBytes int64) RunStats {
	stats := RunStats{ByTier: map[core.Tier]int{}, ByUser: map[string]int{}}
	var used int64
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		hostPath, ok := p.mapper.ToHost(t.Item.ServerPath)
		if !ok {
			stats.Missing++
			p.log.Warn("no path mapping", "server_path", t.Item.ServerPath)
			continue
		}
		size, err := p.fs.Stat(hostPath)
		if err != nil {
			stats.Missing++
			p.log.Warn("stat failed", "path", hostPath, "err", err)
			continue
		}

		pl := p.planWarm(t, hostPath, size)

		// Skip only when the front metadata, content window, and cue tail are
		// all resident; any cold region can force a disk spin-up on open/seek.
		if p.resident(hostPath, 0, pl.front) && p.resident(hostPath, pl.offset, pl.head) && p.resident(hostPath, pl.tailOffset, pl.tail) {
			stats.Skipped++
			stats.ByTier[t.Tier]++
			stats.ByUser[t.Item.UserID]++
			continue
		}

		// Charge only the unique bytes actually warmed.
		cost := pl.front + pl.head + pl.tail
		if used+cost > budgetBytes {
			break // budget exhausted; remaining lower-priority targets dropped
		}

		if pl.front > 0 {
			if err := p.cache.Warm(hostPath, 0, pl.front); err != nil {
				p.log.Warn("front-metadata warm failed", "path", hostPath, "err", err)
			}
		}
		if err := p.cache.Warm(hostPath, pl.offset, pl.head); err != nil {
			stats.Missing++
			p.log.Warn("warm failed", "path", hostPath, "err", err)
			continue
		}
		if pl.tail > 0 {
			if err := p.cache.Warm(hostPath, pl.tailOffset, pl.tail); err != nil {
				p.log.Warn("tail warm failed", "path", hostPath, "err", err)
			}
		}
		used += cost
		stats.Preloaded++
		stats.BytesWarmed += cost
		stats.ByTier[t.Tier]++
		stats.ByUser[t.Item.UserID]++
		if pl.front > 0 {
			stats.Warmed = append(stats.Warmed, WarmedRange{Path: hostPath, Offset: 0, Length: pl.front})
		}
		stats.Warmed = append(stats.Warmed, WarmedRange{Path: hostPath, Offset: pl.offset, Length: pl.head})
		if pl.tail > 0 {
			stats.Warmed = append(stats.Warmed, WarmedRange{Path: hostPath, Offset: pl.tailOffset, Length: pl.tail})
		}
		p.log.Info("preloaded", "name", t.Item.Name, "tier", t.Tier.String(),
			"user", t.Item.UserID, "offset", pl.offset, "bytes", pl.head)
	}
	return stats
}

// warmRanges holds the byte ranges to warm for a single target: the front
// metadata window (container header, read on open), the content/head window
// (sized by playback duration, at the resume offset for in-progress items),
// and the EOF/cue tail, clamped so the three never overlap.
type warmRanges struct {
	front            int64 // [0, front) front metadata; 0 = none
	offset, head     int64
	tailOffset, tail int64
}

// planWarm computes the front-metadata, content (head), and tail ranges for an
// item against its file size. For a seeking (resume) target it warms the exact
// cue index and front metadata when the container parser can locate them,
// falling back to the flat TailBytes tail otherwise. hostPath is the mapped
// on-host path used to inspect the container.
func (p *Preloader) planWarm(t core.PreloadTarget, hostPath string, size int64) warmRanges {
	head := HeadBytes(p.cfg, t.Item)
	offset := int64(0)
	seeking := t.Tier == core.TierResume
	if seeking {
		offset = resumeOffsetBytes(t.Item)
	}
	if offset >= size {
		offset = 0
	}
	if offset+head > size {
		head = size - offset
	}

	front, tailOffset, tail, parsed := p.inspectRanges(seeking, hostPath, size, offset)
	if !parsed && p.cfg.TailBytes > 0 {
		// Flat fallback tail: non-seeking tiers, or a parse failure.
		tailOffset, tail = flatTail(size, p.cfg.TailBytes)
	}
	// The tail must not overlap the content window (keeps the budget accurate).
	tailOffset, tail = clampTailToContent(tailOffset, tail, offset, head, size)
	return warmRanges{front: front, offset: offset, head: head, tailOffset: tailOffset, tail: tail}
}

// inspectRanges parses the container front (for a seeking/resume target) to
// locate the exact front-metadata and cue-tail ranges. parsed is false when
// the target isn't seeking, no inspector is configured, or the parse failed;
// callers then fall back to the flat tail.
func (p *Preloader) inspectRanges(seeking bool, hostPath string, size, offset int64) (front, tailOffset, tail int64, parsed bool) {
	if !seeking || p.inspect == nil {
		return 0, 0, 0, false
	}
	layout, ok := p.inspect(hostPath, size)
	if !ok {
		return 0, 0, 0, false
	}
	if layout.FrontEnd > 0 {
		front = layout.FrontEnd
		if front > maxFrontBytes {
			front = maxFrontBytes
		}
		if front > offset { // never overlap the content window
			front = offset
		}
	}
	// A trailing cue index needs its own tail warm; a front-placed cue index
	// is already covered by the front-metadata window.
	if layout.CueStart >= front && layout.CueStart < size {
		tailOffset = layout.CueStart
		if tailOffset < size-maxTailBytes {
			tailOffset = size - maxTailBytes
		}
		tail = size - tailOffset
	}
	return front, tailOffset, tail, true
}

// flatTail computes the fixed-size tail window from the end of the file.
func flatTail(size, tailBytes int64) (tailOffset, tail int64) {
	tailOffset = size - tailBytes
	if tailOffset < 0 {
		tailOffset = 0
	}
	tail = size - tailOffset
	return tailOffset, tail
}

// clampTailToContent pulls the tail forward so it never overlaps the content
// (head) window, keeping the budget accounting free of double-counted bytes.
func clampTailToContent(tailOffset, tail, offset, head, size int64) (int64, int64) {
	if tail > 0 && tailOffset < offset+head {
		tailOffset = offset + head
		tail = size - tailOffset
		if tail < 0 {
			tail = 0
		}
	}
	return tailOffset, tail
}

// resident reports whether [offset, length) is already fully page-cache resident.
// A zero-length range is trivially resident; unknown residency counts as not.
func (p *Preloader) resident(path string, offset, length int64) bool {
	if length == 0 {
		return true
	}
	r, known, err := p.cache.Resident(path, offset, length)
	return err == nil && known && r >= length
}

func resumeOffsetBytes(it core.MediaItem) int64 {
	if it.ResumeOffset <= 0 {
		return 0
	}
	// Mirror HeadBytes: derive bitrate from size/runtime when the API omits it,
	// so resume items still warm from the saved position instead of the head.
	bps := it.BitrateBps
	if bps <= 0 && it.Runtime > 0 {
		bps = int64(float64(it.SizeBytes) / it.Runtime.Seconds() * 8)
	}
	if bps <= 0 {
		return 0
	}
	return int64(it.ResumeOffset.Seconds()) * bps / 8
}
