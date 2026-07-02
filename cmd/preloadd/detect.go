package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"os"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/pathmap"
)

// runDetectSubcommand emits the effective path rules as JSON. Read-only and
// non-fatal on config errors so the UI can show docker-only rules pre-config.
func runDetectSubcommand(cfgPath string) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(cfgPath)
	var manual []config.PathRule
	if err != nil {
		log.Warn("config load failed; reporting docker-only rules", "err", err)
	} else {
		manual = cfg.PathMap
	}
	if err := runDetectPathmaps(context.Background(), manual, execRunner, os.Stdout); err != nil {
		log.Error("detect-pathmaps failed", "err", err)
		os.Exit(1)
	}
}

// configPathFromArgs resolves the -config value from the detect-pathmaps
// subcommand args (everything after "detect-pathmaps"), matching the -config
// flag the normal run modes accept. Defaults to "config.toml". Parsing is
// lenient (ContinueOnError, output discarded) so an unrecognized flag never
// aborts a read-only diagnostic invocation.
func configPathFromArgs(args []string) string {
	fs := flag.NewFlagSet("detect-pathmaps", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "config.toml", "path to config file")
	_ = fs.Parse(args)
	return *cfgPath
}

type ruleJSON struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source"`
}

// runDetectPathmaps writes the effective path rules (manual first, then
// docker-detected) as JSON. Read-only; no API key involved.
func runDetectPathmaps(ctx context.Context, manual []config.PathRule, run pathmap.Runner, w io.Writer) error {
	out := struct {
		Rules             []ruleJSON `json:"rules"`
		UnraidUNCFallback bool       `json:"unraid_unc_fallback"`
		DockerError       string     `json:"docker_error,omitempty"`
	}{UnraidUNCFallback: true, Rules: []ruleJSON{}}
	for _, r := range manual {
		out.Rules = append(out.Rules, ruleJSON{From: r.From, To: r.To, Source: "manual"})
	}
	dockerRules, err := pathmap.DetectDockerRules(ctx, run, mediaServerImages)
	if err != nil {
		// Surface a real detection failure (docker missing, socket unmounted,
		// permission denied, timeout) instead of making it indistinguishable
		// from "no container found" - this subcommand exists to diagnose exactly
		// that pipeline.
		out.DockerError = err.Error()
	} else {
		for _, r := range dockerRules {
			out.Rules = append(out.Rules, ruleJSON{From: r.From, To: r.To, Source: "docker"})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
