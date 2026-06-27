// Command preloadd is the watch-aware media preloader daemon.
package main

import (
	"context"
	"flag"
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
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "config.toml", "path to config file")
	verify := flag.Bool("verify", false, "run one sweep, then report cache residency and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("preloadd starting", "version", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}

	client, err := emby.New(cfg.Server.URL, cfg.Server.APIKey, nil)
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
	pre := preloader.New(preCfg, pagecache.New(), pathmap.New(rules), preloader.DefaultFS(), log)

	d := app.NewDaemon(cfg, client, pre, log)

	if *verify {
		budget := d.Budget()
		stats, err := app.RunOnce(context.Background(), client, cfg.Users.Enabled, pre, budget, log)
		if err != nil {
			log.Error("verify sweep failed", "err", err)
			os.Exit(1)
		}
		log.Info("verify sweep done", "preloaded", stats.Preloaded, "skipped", stats.Skipped)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	loopErr := d.Loop(ctx)
	stop() // release signal resources before exit
	if loopErr != nil && ctx.Err() == nil {
		log.Error("daemon loop exited with error", "err", loopErr)
		os.Exit(1)
	}
	log.Info("preloadd stopped")
}
