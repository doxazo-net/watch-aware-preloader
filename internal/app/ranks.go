package app

import (
	"log/slog"
	"sort"

	"github.com/doxazo-net/watch-aware-preloader/internal/config"
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/doxazo-net/watch-aware-preloader/internal/scorer"
)

// userRef is how a configured user reference resolved against the server list.
type userRef int

const (
	refUnknown   userRef = iota // matched no user
	refExactID                  // matched a user ID
	refName                     // matched exactly one display name
	refAmbiguous                // matched more than one display name
)

// resolveUserKey maps a configured user reference (an ID or a display name) to a
// user ID.
//
// An exact ID match wins outright and is checked against every user before any
// name match is accepted: IDs are unique and server-assigned, so a key that is
// one user's ID is never taken as another user's name.
//
// A display name shared by more than one user is AMBIGUOUS and resolves to
// nothing. Returning the first match would bind the operator's intent to the
// server's arbitrary list order, silently warming one user's media under
// another's name; refusing makes the operator disambiguate with an ID.
func resolveUserKey(users []emby.User, key string) (string, userRef) {
	var named []string
	for _, u := range users {
		if u.ID == key {
			return u.ID, refExactID
		}
		if u.Name == key {
			named = append(named, u.ID)
		}
	}
	switch len(named) {
	case 0:
		return "", refUnknown
	case 1:
		return named[0], refName
	default:
		return "", refAmbiguous
	}
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
	//
	// Two DIFFERENT override keys can name the SAME user (say "Alice" and her ID),
	// and cfg.Tiers.Override is a map, so applying them in iteration order would
	// let Go's randomized ordering pick the winner - the same config yielding a
	// different warm set per run. Resolve in sorted key order, then apply
	// name-resolved entries before ID-resolved ones so the exact ID always wins.
	keys := make([]string, 0, len(cfg.Tiers.Override))
	for key := range cfg.Tiers.Override {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	type resolvedOverride struct {
		id    string
		order map[core.Tier]int
	}
	var byName, byID []resolvedOverride
	for _, key := range keys {
		id, ref := resolveUserKey(users, key)
		switch ref {
		case refExactID:
			byID = append(byID, resolvedOverride{id, tierPositions(cfg.Tiers.Override[key])})
		case refName:
			byName = append(byName, resolvedOverride{id, tierPositions(cfg.Tiers.Override[key])})
		case refAmbiguous:
			log.Warn("ignoring tier override for ambiguous user name", "user", key)
		case refUnknown:
			log.Warn("ignoring tier override for unknown user", "user", key)
		}
	}
	overrides := make(map[string]map[core.Tier]int, len(cfg.Tiers.Override))
	for _, r := range byName {
		overrides[r.id] = r.order
	}
	for _, r := range byID {
		overrides[r.id] = r.order
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
	//
	// An empty resolved order (the user warms nothing) is reachable only when the
	// operator asked for it explicitly (`order = []`, an empty override, or every
	// legacy dial false), so it passes without comment. This runs once per sweep;
	// warning here would nag about a deliberate choice on every pass.
	assign := func(id string, rank int) {
		if _, seen := opts.UserRank[id]; seen {
			return
		}
		opts.UserRank[id] = rank
		order, overridden := overrides[id]
		if !overridden {
			order = global
		}
		opts.TierRank[id] = order
	}

	if len(cfg.Users.Enabled) == 0 {
		for _, u := range users {
			assign(u.ID, 0) // equal rank
		}
		return opts
	}
	for _, key := range cfg.Users.Enabled {
		id, ref := resolveUserKey(users, key)
		switch ref {
		case refExactID, refName:
			assign(id, len(opts.UserRank))
		case refAmbiguous:
			log.Warn("ignoring enabled user whose display name matches several users", "user", key)
		case refUnknown:
			log.Warn("ignoring enabled user not present on the server", "user", key)
		}
	}
	return opts
}
