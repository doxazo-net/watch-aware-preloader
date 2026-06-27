// Package preloader warms the page cache for a ranked list of targets within a
// byte budget, sizing each read by playback duration.
package preloader

import (
	"context"
	"log/slog"
	"os"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/pagecache"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
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
	Warmed      []WarmedRange
}

// Preloader executes preload passes.
type Preloader struct {
	cfg    Config
	cache  pagecache.Cache
	mapper *pathmap.Mapper
	fs     FS
	log    *slog.Logger
}

// New builds a Preloader.
func New(cfg Config, cache pagecache.Cache, mapper *pathmap.Mapper, fs FS, log *slog.Logger) *Preloader {
	return &Preloader{cfg: cfg, cache: cache, mapper: mapper, fs: fs, log: log}
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
	stats := RunStats{ByTier: map[core.Tier]int{}}
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

		pl := p.planWarm(t, size)

		// Skip only when both the head window and the EOF tail are resident;
		// a hot head with a cold tail must still warm the tail (container
		// metadata at EOF can otherwise force disk I/O on open).
		if p.resident(hostPath, pl.offset, pl.head) && p.resident(hostPath, pl.tailOffset, pl.tail) {
			stats.Skipped++
			stats.ByTier[t.Tier]++
			continue
		}

		// Charge only the unique bytes actually warmed (tail may be zero or
		// clamped near EOF), so the budget and BytesWarmed stay accurate.
		cost := pl.head + pl.tail
		if used+cost > budgetBytes {
			break // budget exhausted; remaining lower-priority targets dropped
		}

		if err := p.cache.Warm(hostPath, pl.offset, pl.head); err != nil {
			stats.Missing++
			p.log.Warn("warm failed", "path", hostPath, "err", err)
			continue
		}
		if pl.tail > 0 {
			_ = p.cache.Warm(hostPath, pl.tailOffset, pl.tail) // best-effort: tail (e.g. MP4 moov) warm failure is non-fatal
		}
		used += cost
		stats.Preloaded++
		stats.BytesWarmed += cost
		stats.ByTier[t.Tier]++
		stats.Warmed = append(stats.Warmed, WarmedRange{Path: hostPath, Offset: pl.offset, Length: pl.head})
		p.log.Info("preloaded", "name", t.Item.Name, "tier", t.Tier.String(),
			"user", t.Item.UserID, "offset", pl.offset, "bytes", pl.head)
	}
	return stats
}

// warmRanges holds the byte ranges to warm for a single target: the head window
// (sized by playback duration, at the resume offset for in-progress items) and
// the EOF tail, clamped so the two never overlap.
type warmRanges struct {
	offset, head     int64
	tailOffset, tail int64
}

// planWarm computes the head and tail ranges for an item against its file size.
func (p *Preloader) planWarm(t core.PreloadTarget, size int64) warmRanges {
	head := HeadBytes(p.cfg, t.Item)
	offset := int64(0)
	if t.Tier == core.TierResume {
		offset = resumeOffsetBytes(t.Item)
	}
	if offset >= size {
		offset = 0
	}
	if offset+head > size {
		head = size - offset
	}

	tailOffset, tail := int64(0), int64(0)
	if p.cfg.TailBytes > 0 {
		// Anchor the tail to EOF; for a file at or below TailBytes this starts at
		// 0 so the whole suffix is covered (a small file whose head stops short
		// of EOF would otherwise leave the end cold).
		tailOffset = size - p.cfg.TailBytes
		if tailOffset < 0 {
			tailOffset = 0
		}
		tail = size - tailOffset
		if tailOffset < offset+head { // tail would overlap the head window
			tailOffset = offset + head
			tail = size - tailOffset
			if tail < 0 {
				tail = 0
			}
		}
	}
	return warmRanges{offset: offset, head: head, tailOffset: tailOffset, tail: tail}
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
