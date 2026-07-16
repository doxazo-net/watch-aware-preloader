package app

import (
	"log/slog"

	"github.com/doxazo-net/watch-aware-preloader/internal/config"
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/doxazo-net/watch-aware-preloader/internal/scorer"
)

// resolveUserKey maps a configured user reference (an ID or a display name) to a
// user ID. IDs are GUIDs and names are human strings, so there is no collision.
func resolveUserKey(users []emby.User, key string) (string, bool) {
	for _, u := range users {
		if u.ID == key || u.Name == key {
			return u.ID, true
		}
	}
	return "", false
}

// tierPositions turns an order into a tier -> position lookup.
func tierPositions(o config.TierOrder) map[core.Tier]int {
	m := make(map[core.Tier]int, len(o))
	for i, t := range o {
		m[t] = i
	}
	return m
}

// ResolveRanks resolves the config cascade into ID-keyed rank data for the
// scorer. Enrollment order in cfg.Users.Enabled is the user rank. An empty
// enabled list selects every user at EQUAL rank (0), leaving the scorer's stable
// sort to preserve the provider's order.
//
// Overrides inherit by absence: a user with no override entry gets the global
// order. An override that binds to no known user is warned and ignored, so a
// stale entry for an un-enrolled user cannot brick a config.
func ResolveRanks(cfg *config.Config, users []emby.User, log *slog.Logger) scorer.RankOpts {
	global := tierPositions(cfg.Tiers.Order)

	// Resolve overrides to IDs once, warning on any that bind to nobody.
	overrides := make(map[string]map[core.Tier]int, len(cfg.Tiers.Override))
	for key, o := range cfg.Tiers.Override {
		id, ok := resolveUserKey(users, key)
		if !ok {
			log.Warn("ignoring tier override for unknown user", "user", key)
			continue
		}
		overrides[id] = tierPositions(o)
	}

	opts := scorer.RankOpts{
		TierRank: map[string]map[core.Tier]int{},
		UserRank: map[string]int{},
	}
	// assign shares the global map across users rather than copying it per user.
	// Safe only because nothing mutates a resolved order after construction; do
	// not introduce a writer without copying first.
	//
	// An already-assigned user keeps their first (best) rank: the enabled list can
	// name one user twice (say both a display name and an ID), and a re-assign
	// would otherwise overwrite that rank without growing the map, handing the
	// next user a colliding rank.
	assign := func(id string, rank int) {
		if _, seen := opts.UserRank[id]; seen {
			return
		}
		opts.UserRank[id] = rank
		if o, ok := overrides[id]; ok {
			opts.TierRank[id] = o
			return
		}
		opts.TierRank[id] = global
	}

	if len(cfg.Users.Enabled) == 0 {
		for _, u := range users {
			assign(u.ID, 0) // equal rank
		}
		return opts
	}
	for _, key := range cfg.Users.Enabled {
		id, ok := resolveUserKey(users, key)
		if !ok {
			log.Warn("ignoring enabled user not present on the server", "user", key)
			continue
		}
		assign(id, len(opts.UserRank))
	}
	return opts
}
