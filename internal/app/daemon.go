package app

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/doxazo-net/watch-aware-preloader/internal/config"
	"github.com/doxazo-net/watch-aware-preloader/internal/preloader"
	"github.com/doxazo-net/watch-aware-preloader/internal/scorer"
	"github.com/doxazo-net/watch-aware-preloader/internal/sysinfo"
)

// RunOnce performs one full pipeline pass: collect, rank, preload. When
// opts.EnabledLibraries is non-empty, candidates are scoped to those libraries;
// opts.Tiers controls which signal tiers contribute and their per-user caps.
// opts.Mode and opts.StatusPath are unused here (only SweepAndRecord reads them).
func RunOnce(ctx context.Context, p Provider, pre *preloader.Preloader, opts SweepOptions, log *slog.Logger) (preloader.RunStats, error) {
	// Rank resolution needs the provider's user list to map configured names to
	// IDs. SweepOptions carries the cascade's inputs rather than the resolved
	// ranks, so rebuild the config view here.
	users, err := p.Users(ctx)
	if err != nil {
		return preloader.RunStats{}, err
	}
	rankCfg := &config.Config{Tiers: opts.Tiers}
	rankCfg.Users.Enabled = opts.Enabled
	ranks := ResolveRanks(rankCfg, users, log)

	cands, playing, err := CollectCandidates(ctx, p, opts.Enabled, opts.EnabledLibraries, opts.Tiers, pre.ToHost, log)
	if err != nil {
		return preloader.RunStats{}, err
	}
	targets := scorer.Rank(cands, playing, ranks)
	stats := pre.Run(ctx, targets, opts.Budget)
	log.Info("sweep complete",
		"targets", len(targets), "preloaded", stats.Preloaded,
		"skipped", stats.Skipped, "missing", stats.Missing,
		"bytes_warmed", stats.BytesWarmed, "by_tier", stats.ByTier)
	return stats, nil
}

// Daemon runs the periodic sweep and the session-triggered sweep.
type Daemon struct {
	cfg *config.Config
	p   Provider
	pre *preloader.Preloader
	log *slog.Logger
}

// NewDaemon wires the runtime loop.
func NewDaemon(cfg *config.Config, p Provider, pre *preloader.Preloader, log *slog.Logger) *Daemon {
	return &Daemon{cfg: cfg, p: p, pre: pre, log: log}
}

// Budget returns the current preload byte budget.
func (d *Daemon) Budget() int64 { return d.budget() }

func (d *Daemon) budget() int64 {
	avail, err := sysinfo.AvailableBytes()
	if err != nil {
		d.log.Warn("reading available RAM failed; using 0 budget", "err", err)
		return 0
	}
	return sysinfo.BudgetBytes(avail, d.cfg.Preload.RAMPercent)
}

func (d *Daemon) sweep(ctx context.Context) {
	opts := SweepOptionsFromConfig(d.cfg, d.budget(), "daemon")
	if _, err := SweepAndRecord(ctx, d.p, d.pre, opts, d.log); err != nil {
		d.log.Error("sweep failed", "err", err)
	}
}

// Loop runs until ctx is canceled. A full sweep fires on the sweep interval;
// a fast session poll fires an extra sweep whenever the now-playing set changes
// (giving event-like latency without a websocket).
func (d *Daemon) Loop(ctx context.Context) error {
	d.sweep(ctx) // warm immediately on start

	sweepTick := time.NewTicker(time.Duration(d.cfg.Schedule.SweepSeconds) * time.Second)
	defer sweepTick.Stop()
	pollTick := time.NewTicker(time.Duration(d.cfg.Schedule.SessionPollSeconds) * time.Second)
	defer pollTick.Stop()

	var lastPlaying string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sweepTick.C:
			d.sweep(ctx)
		case <-pollTick.C:
			ids, err := d.p.NowPlayingIDs(ctx)
			if err != nil {
				d.log.Warn("session poll failed", "err", err)
				continue
			}
			if sig := playingSignature(ids); sig != lastPlaying {
				lastPlaying = sig
				d.log.Info("playback state changed; triggering sweep")
				d.sweep(ctx)
			}
		}
	}
}

func playingSignature(ids map[string]bool) string {
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	// Deterministic signature independent of map order.
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
