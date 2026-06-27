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

// RunStats summarizes a preload pass.
type RunStats struct {
	Preloaded   int
	Skipped     int
	Missing     int
	BytesWarmed int64
	ByTier      map[core.Tier]int
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

		// Skip ranges already fully resident (costs no budget).
		if resident, known, rerr := p.cache.Resident(hostPath, offset, head); rerr == nil && known && resident >= head {
			stats.Skipped++
			stats.ByTier[t.Tier]++
			continue
		}

		cost := head + p.cfg.TailBytes
		if used+cost > budgetBytes {
			break // budget exhausted; remaining lower-priority targets dropped
		}

		if err := p.cache.Warm(hostPath, offset, head); err != nil {
			stats.Missing++
			p.log.Warn("warm failed", "path", hostPath, "err", err)
			continue
		}
		if p.cfg.TailBytes > 0 && size > p.cfg.TailBytes {
			_ = p.cache.Warm(hostPath, size-p.cfg.TailBytes, p.cfg.TailBytes) // best-effort: tail (e.g. MP4 moov) warm failure is non-fatal
		}
		used += cost
		stats.Preloaded++
		stats.BytesWarmed += cost
		stats.ByTier[t.Tier]++
		p.log.Info("preloaded", "name", t.Item.Name, "tier", t.Tier.String(),
			"user", t.Item.UserID, "offset", offset, "bytes", head)
	}
	return stats
}

func resumeOffsetBytes(it core.MediaItem) int64 {
	if it.ResumeOffset <= 0 || it.BitrateBps <= 0 {
		return 0
	}
	return int64(it.ResumeOffset.Seconds()) * it.BitrateBps / 8
}
