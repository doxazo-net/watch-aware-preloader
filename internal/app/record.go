package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
	"github.com/sydlexius/watch-aware-preloader/internal/status"
)

// SweepAndRecord runs one sweep via RunOnce, times it, and writes the status
// file. It is the single sweep entry point for every run mode, so all modes
// emit status uniformly. mode is the run mode recorded in status.json and is one
// of "once", "verify", or "daemon" (it is written verbatim to the status file's
// mode field). The status write is best-effort: a failure is logged at WARN and
// never turns a successful warm into a failed run. RunOnce's stats and error are
// returned unchanged.
func SweepAndRecord(ctx context.Context, p Provider, enabled, enabledLibraries []string, tiers config.TiersConfig, pre *preloader.Preloader, budget int64, mode, statusPath string, log *slog.Logger) (preloader.RunStats, error) {
	start := time.Now()
	stats, runErr := RunOnce(ctx, p, enabled, enabledLibraries, tiers, pre, budget, log)
	s := buildStatus(mode, budget, time.Since(start), stats, runErr)
	if err := status.Write(statusPath, s); err != nil {
		log.Warn("writing status file failed", "path", statusPath, "err", err)
	}
	return stats, runErr
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
