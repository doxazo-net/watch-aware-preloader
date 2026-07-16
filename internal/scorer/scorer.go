// Package scorer turns per-user watch signals into a ranked, deduped preload list.
package scorer

import (
	"sort"

	"github.com/doxazo-net/watch-aware-preloader/internal/core"
)

// Candidate is one item proposed for preloading at a given tier.
type Candidate struct {
	Item core.MediaItem
	Tier core.Tier
}

// RankOpts carries the resolved priority data. Both maps are keyed by user ID;
// app.ResolveRanks normalizes names to IDs before building them.
type RankOpts struct {
	// TierRank maps a user to their resolved tier positions (override or the
	// inherited global). A tier absent from a user's map is disabled for that
	// user. A user absent from TierRank contributes nothing.
	TierRank map[string]map[core.Tier]int
	// UserRank maps a user to their enrollment rank; lower is higher priority.
	// Equal values mean equal rank (the all-users default), and the stable sort
	// then preserves the provider's order.
	UserRank map[string]int
}

// slot returns the item's position in its own user's resolved order. The second
// result is false when the tier is disabled for that user, or the user has no
// resolved order at all.
func (o RankOpts) slot(c Candidate) (int, bool) {
	m, ok := o.TierRank[c.Item.UserID]
	if !ok {
		return 0, false
	}
	pos, ok := m[c.Tier]
	return pos, ok
}

// Rank filters out actively-playing items and tiers disabled for their user,
// dedupes by item ID, and orders the result slot-major: every user's first-choice
// signal precedes any second choice, with user rank breaking ties within a slot.
//
// Priority is NOT the core.Tier integer order; it is opts. Comparing tiers
// against a single global order instead of each item's own-user slot would make
// per-user overrides inert.
func Rank(candidates []Candidate, nowPlaying map[string]bool, opts RankOpts) []core.PreloadTarget {
	type scored struct {
		c    Candidate
		slot int
		rank int
	}

	best := make(map[string]scored, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if nowPlaying[c.Item.ID] {
			continue
		}
		s, ok := opts.slot(c)
		if !ok {
			continue // tier disabled for this user, or user not enrolled
		}
		cand := scored{c: c, slot: s, rank: opts.UserRank[c.Item.UserID]}

		existing, seen := best[c.Item.ID]
		if !seen {
			best[c.Item.ID] = cand
			order = append(order, c.Item.ID)
			continue
		}
		// A shared item keeps the better (user rank, slot) pair, and with it that
		// user's ID, so its fate does not depend on fetch order.
		if cand.rank < existing.rank || (cand.rank == existing.rank && cand.slot < existing.slot) {
			best[c.Item.ID] = cand
		}
	}

	targets := make([]core.PreloadTarget, 0, len(order))
	slots := make(map[string]scored, len(order))
	for _, id := range order {
		s := best[id]
		slots[id] = s
		targets = append(targets, core.PreloadTarget{Item: s.c.Item, Tier: s.c.Tier})
	}

	sort.SliceStable(targets, func(i, j int) bool {
		si, sj := slots[targets[i].Item.ID], slots[targets[j].Item.ID]
		if si.slot != sj.slot {
			return si.slot < sj.slot
		}
		if si.rank != sj.rank {
			return si.rank < sj.rank
		}
		// Within the resume tier, deeper resume positions go first.
		if targets[i].Tier == core.TierResume && targets[j].Tier == core.TierResume {
			return targets[i].Item.ResumeOffset > targets[j].Item.ResumeOffset
		}
		return false // stable: preserve input order
	})
	return targets
}
