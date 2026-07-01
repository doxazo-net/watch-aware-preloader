// Command preloadd is the watch-aware media preloader daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sydlexius/watch-aware-preloader/internal/app"
	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/sydlexius/watch-aware-preloader/internal/pagecache"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
	"github.com/sydlexius/watch-aware-preloader/internal/secrets"
)

var version = "dev"

// selectMode resolves the run mode from the three flag values.
// Priority:
//  1. -verify   -> "verify"  (one sweep + residency report, then exit)
//  2. -daemon   -> "daemon"  (resident loop; opt-in for long-running service use)
//  3. default / -once -> "once" (one sweep, then exit; cron re-invokes each interval)
//
// Combining -once and -daemon is an error: they express conflicting lifecycle intent.
// The DEFAULT (no flags) is "once" so that a bare `preloadd` invocation is safe to
// run under cron - it fetches fresh library state, preloads, and exits. The daemon
// loop is strictly opt-in via -daemon.
func selectMode(once, daemon, verify bool) (string, error) {
	if once && daemon {
		return "", errors.New("-once and -daemon are mutually exclusive")
	}
	switch {
	case verify:
		return "verify", nil
	case daemon:
		return "daemon", nil
	default: // covers both explicit -once and the bare invocation
		return "once", nil
	}
}

func main() {
	cfgPath := flag.String("config", "config.toml", "path to config file")
	verify := flag.Bool("verify", false, "run one sweep, then report cache residency and exit")
	once := flag.Bool("once", false, "run exactly one sweep then exit (cron model; default when no mode flag is given)")
	daemon := flag.Bool("daemon", false, "run the resident periodic loop (opt-in; use for long-running service installs)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("preloadd starting", "version", version)

	mode, err := selectMode(*once, *daemon, *verify)
	if err != nil {
		log.Error("invalid flags", "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	apiKey, err := secrets.APIKey(cfg.SecretPath)
	if err != nil {
		log.Error("loading API key failed", "err", err)
		os.Exit(1)
	}

	client, err := emby.New(cfg.Server.URL, apiKey, nil)
	if err != nil {
		log.Error("emby client init failed", "err", err)
		os.Exit(1)
	}

	rules := make([]pathmap.Rule, 0, len(cfg.PathMap))
	for _, r := range cfg.PathMap {
		rules = append(rules, pathmap.Rule{From: r.From, To: r.To})
	}
	preCfg := preloader.Config{
		TargetSeconds: cfg.Preload.TargetSeconds,
		MinHeadBytes:  cfg.Preload.MinHeadMB << 20,
		MaxHeadBytes:  cfg.Preload.MaxHeadMB << 20,
		TailBytes:     cfg.Preload.TailMB << 20,
	}
	pre := preloader.New(preCfg, pagecache.New(cfg.Residency.ProbeBytes, cfg.Residency.ProbeThreshold, log), pathmap.New(rules), preloader.DefaultFS(), log)

	d := app.NewDaemon(cfg, client, pre, log)

	switch mode {
	case "verify":
		budget := d.Budget()
		stats, verifyErr := app.SweepAndRecord(context.Background(), client, cfg.Users.Enabled, pre, budget, "verify", cfg.StatusPath, log)
		if verifyErr != nil {
			log.Error("verify sweep failed", "err", verifyErr)
			os.Exit(1)
		}
		mean, known := app.ReportResidency(pagecache.New(cfg.Residency.ProbeBytes, cfg.Residency.ProbeThreshold, log), stats.Warmed, log)
		if known {
			log.Info("verify complete", "mean_resident_pct", mean, "preloaded", stats.Preloaded, "skipped", stats.Skipped, "missing", stats.Missing)
		} else {
			log.Info("verify complete (residency unavailable on this platform - mincore is Linux-only)", "preloaded", stats.Preloaded, "skipped", stats.Skipped, "missing", stats.Missing)
		}

	case "once":
		stats, sweepErr := app.SweepAndRecord(context.Background(), client, cfg.Users.Enabled, pre, d.Budget(), "once", cfg.StatusPath, log)
		if sweepErr != nil {
			log.Error("sweep failed", "err", sweepErr)
			os.Exit(1)
		}
		log.Info("one-shot sweep done",
			"preloaded", stats.Preloaded,
			"skipped", stats.Skipped,
			"missing", stats.Missing)

	case "daemon":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		loopErr := d.Loop(ctx)
		stop() // release signal resources before exit
		if loopErr != nil && ctx.Err() == nil {
			log.Error("daemon loop exited with error", "err", loopErr)
			os.Exit(1)
		}
		log.Info("preloadd stopped")

	default:
		// Should be unreachable given selectMode's exhaustive switch.
		log.Error("internal error: unknown mode", "mode", mode)
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", mode)
		os.Exit(1)
	}
}
