package app

import "github.com/doxazo-net/watch-aware-preloader/internal/config"

// SweepOptions bundles the per-sweep configuration knobs that thread through the
// sweep call chain (SweepAndRecord -> RunOnce). Grouping them keeps those
// signatures stable as the settings UI adds more selection dials.
//
// Field consumption by layer: RunOnce reads Enabled, EnabledLibraries, Tiers,
// and Budget; SweepAndRecord additionally reads Mode and StatusPath for the
// status write. The lower-level CollectCandidates keeps explicit params and is
// fed the collection fields by RunOnce.
type SweepOptions struct {
	Enabled          []string           // enabled user names, empty = all users (ResolveUserIDs maps names to IDs)
	EnabledLibraries []string           // enabled library IDs, empty = all libraries (library-scope filter)
	Tiers            config.TiersConfig // per-tier enable + per-user caps
	Budget           int64              // preload byte budget for this sweep
	Mode             string             // status.json run mode: "once", "verify", or "daemon"
	StatusPath       string             // path the status file is written to
}

// SweepOptionsFromConfig builds the sweep options from the resolved config, with
// the per-run budget and mode supplied by the caller (both vary per invocation:
// budget is sampled from available RAM at sweep time, mode from the run path).
func SweepOptionsFromConfig(cfg *config.Config, budget int64, mode string) SweepOptions {
	return SweepOptions{
		Enabled:          cfg.Users.Enabled,
		EnabledLibraries: cfg.Libraries.Enabled,
		Tiers:            cfg.Tiers,
		Budget:           budget,
		Mode:             mode,
		StatusPath:       cfg.StatusPath,
	}
}
