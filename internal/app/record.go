package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/doxazo-net/watch-aware-preloader/internal/config"
	"github.com/doxazo-net/watch-aware-preloader/internal/preloader"
	"github.com/doxazo-net/watch-aware-preloader/internal/status"
)

// SweepAndRecord runs one sweep via RunOnce, times it, and writes the status
// file. It is the single sweep entry point for every run mode, so all modes
// emit status uniformly. opts.Mode is the run mode recorded in status.json and
// is one of "once", "verify", or "daemon" (written verbatim to the status file's
// mode field). The status write is best-effort: a failure is logged at WARN and
// never turns a successful warm into a failed run. RunOnce's stats and error are
// returned unchanged.
func SweepAndRecord(ctx context.Context, p Provider, pre *preloader.Preloader, opts SweepOptions, log *slog.Logger) (preloader.RunStats, error) {
	start := time.Now()
	stats, runErr := RunOnce(ctx, p, pre, opts, log)
	writeStatus(opts.StatusPath, opts.Mode, opts.Budget, time.Since(start), stats, runErr, log)
	return stats, runErr
}

// writeStatus builds and writes the status file, logging (never returning) a
// write failure. Shared by the sweep path and the pre-sweep failure path so both
// record through exactly one code path.
func writeStatus(path, mode string, budget int64, dur time.Duration, stats preloader.RunStats, runErr error, log *slog.Logger) {
	s := buildStatus(mode, budget, dur, stats, runErr)
	if err := status.Write(path, s); err != nil {
		log.Warn("writing status file failed", "path", path, "err", err)
	}
}

// SweepWithUsers is the recorded sweep entry point for every run mode: it
// enumerates the provider's users, resolves ranks from that same list, and runs
// one recorded sweep.
//
// The user list is fetched per sweep rather than cached, because rank resolution
// maps configured names to IDs and the server's user list can change between
// sweeps. Enumeration is INSIDE the recorded lifecycle on purpose: a user-list
// outage is a failed sweep and is written to status.json as one. Returning it
// bare would leave the status file advertising the last SUCCESSFUL run, so the
// settings page would show a healthy warm set while nothing had been warmed.
func SweepWithUsers(ctx context.Context, p Provider, pre *preloader.Preloader, cfg *config.Config, budget int64, mode string, log *slog.Logger) (preloader.RunStats, error) {
	start := time.Now()
	users, err := p.Users(ctx)
	if err != nil {
		err = fmt.Errorf("listing users: %w", err)
		writeStatus(cfg.StatusPath, mode, budget, time.Since(start), preloader.RunStats{}, err, log)
		return preloader.RunStats{}, err
	}
	opts := SweepOptionsFromConfig(cfg, users, budget, mode, log)
	return SweepAndRecord(ctx, p, pre, opts, log)
}

// buildStatus maps a RunStats plus run metadata into a status.Status. by_tier
// keys are tier names in snake_case (Tier.String() is kebab-case for log
// output; status.json uses snake_case like its other keys, so hyphens are
// converted to underscores here). by_user keys are raw UserIDs. last_run is
// stamped in UTC.
func buildStatus(mode string, budget int64, dur time.Duration, stats preloader.RunStats, runErr error) status.Status {
	byTier := make(map[string]int, len(stats.ByTier))
	for tier, n := range stats.ByTier {
		byTier[strings.ReplaceAll(tier.String(), "-", "_")] = n
	}
	byUser := make(map[string]int, len(stats.ByUser))
	for id, n := range stats.ByUser {
		byUser[id] = n
	}
	s := status.Status{
		SchemaVersion: status.SchemaVersion,
		LastRun:       time.Now().UTC().Format(time.RFC3339),
		Mode:          mode,
		DurationMs:    dur.Milliseconds(),
		OK:            runErr == nil,
		BudgetBytes:   budget,
		BytesWarmed:   stats.BytesWarmed,
		Preloaded:     stats.Preloaded,
		Skipped:       stats.Skipped,
		Missing:       stats.Missing,
		ByTier:        byTier,
		ByUser:        byUser,
	}
	if runErr != nil {
		s.Error = runErr.Error()
	}
	return s
}
