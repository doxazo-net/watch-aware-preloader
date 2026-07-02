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

// mediaServerImages lists the substrings matched against `docker ps` image names
// to auto-detect the media-server container. Shared between the startup wiring
// (buildMapper) and the detect-pathmaps diagnostic subcommand so the two never
// drift on which images count.
var mediaServerImages = []string{"emby", "jellyfin"}

// buildMapper composes the path Mapper: manual rules are appended before
// docker-inspect rules, then all are resolved by longest matching prefix (so a
// manual rule wins over a docker rule of equal specificity via the stable sort);
// the Unraid-UNC fallback is always on. Docker detection is best-effort and never
// blocks a sweep - failures are logged and skipped.
func buildMapper(ctx context.Context, manual []config.PathRule, run pathmap.Runner, log *slog.Logger) *pathmap.Mapper {
	rules := make([]pathmap.Rule, 0, len(manual))
	for _, r := range manual {
		rules = append(rules, pathmap.Rule{From: r.From, To: r.To})
	}
	dockerRules, err := pathmap.DetectDockerRules(ctx, run, mediaServerImages)
	if err != nil {
		log.Warn("docker path-map auto-detect failed; using manual rules + UNC fallback", "err", err)
	} else if len(dockerRules) > 0 {
		log.Info("docker path-map auto-detect", "rules", len(dockerRules))
		rules = append(rules, dockerRules...)
	} else {
		log.Info("docker path-map auto-detect found no applicable rules; using manual rules + UNC fallback")
	}
	return pathmap.New(rules, pathmap.WithUnraidUNCFallback())
}
