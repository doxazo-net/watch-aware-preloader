package app

import (
	"testing"
	"time"

	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/preloader"
)

func estCfg() preloader.Config {
	return preloader.Config{TargetSeconds: 20, MinHeadBytes: 8 << 20, MaxHeadBytes: 250 << 20, TailBytes: 16 << 20}
}

func TestProjectBytesAddsTailAndResumeFront(t *testing.T) {
	cfg := estCfg()
	// Bitrate known: HeadBytes = TargetSeconds * bps/8, clamped. 8 Mbps * 20s / 8 = 20 MB.
	it := core.MediaItem{BitrateBps: 8_000_000}
	head := preloader.HeadBytes(cfg, it)

	nextUp := projectBytes(cfg, it, core.TierNextUp)
	if nextUp != head+cfg.TailBytes {
		t.Errorf("next-up projected = %d, want head+tail = %d", nextUp, head+cfg.TailBytes)
	}
	resume := projectBytes(cfg, it, core.TierResume)
	if resume != head+cfg.TailBytes+estimateResumeFrontBytes {
		t.Errorf("resume projected = %d, want head+tail+front = %d", resume, head+cfg.TailBytes+estimateResumeFrontBytes)
	}
	if resume <= nextUp {
		t.Errorf("resume (%d) should exceed next-up (%d) by the front allowance", resume, nextUp)
	}
}

func TestProjectBytesGeometryFallback(t *testing.T) {
	cfg := estCfg()
	// No bitrate: HeadBytes derives bps from SizeBytes/Runtime. 1 GiB over 1000s.
	it := core.MediaItem{SizeBytes: 1 << 30, Runtime: 1000 * time.Second}
	if got := projectBytes(cfg, it, core.TierNextUp); got != preloader.HeadBytes(cfg, it)+cfg.TailBytes {
		t.Errorf("geometry projected = %d, want head+tail", got)
	}
	if preloader.HeadBytes(cfg, it) <= 0 {
		t.Fatal("expected a positive geometry-derived head; sizing fallback not exercised")
	}
}
