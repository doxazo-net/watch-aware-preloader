package main

import (
	"context"
	"log/slog"
	"os/exec"
	"time"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
)

// execRunner runs a real command with a bounded timeout. It is the production
// pathmap.Runner; tests inject a fake.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// #nosec G204 -- shell-free argv exec; command is the fixed "docker" binary, args are
	// fixed subcommands plus a container name from local docker output. No shell, no injection.
	return exec.CommandContext(ctx, name, args...).Output()
}

// buildMapper composes the path Mapper: manual rules first (highest precedence),
// then best-effort docker-inspect rules, with the Unraid-UNC fallback always on.
// Docker detection never blocks a sweep - failures are logged and skipped.
func buildMapper(ctx context.Context, manual []config.PathRule, run pathmap.Runner, log *slog.Logger) *pathmap.Mapper {
	rules := make([]pathmap.Rule, 0, len(manual))
	for _, r := range manual {
		rules = append(rules, pathmap.Rule{From: r.From, To: r.To})
	}
	dockerRules, err := pathmap.DetectDockerRules(ctx, run, []string{"emby", "jellyfin"})
	if err != nil {
		log.Warn("docker path-map auto-detect failed; using manual rules + UNC fallback", "err", err)
	} else if len(dockerRules) > 0 {
		log.Info("docker path-map auto-detect", "rules", len(dockerRules))
		rules = append(rules, dockerRules...)
	}
	return pathmap.New(rules, pathmap.WithUnraidUNCFallback())
}
