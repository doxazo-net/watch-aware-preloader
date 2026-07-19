// Package scorer turns per-user watch signals into a ranked, deduped preload list.
package scorer

import (
	"sort"
	"time"

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
	//
	// READ-ONLY: entries may be SHARED, one positions map aliased by every user
	// that inherited the global order (see app.ResolveRanks). Mutating a
	// resolved order in place would silently alter other users; copy first.
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

// resumeKey returns the tie-break pair for an item within an equal slot+rank
// group: class 0 for a resume and 1 otherwise, plus the resume depth to order
// resumes among themselves. Depth is forced to zero off the resume tier so
// non-resumes tie with each other and keep their input order. The class test is
// tier IDENTITY, not the core.Tier integer order, which carries no priority.
func resumeKey(t core.PreloadTarget) (int, time.Duration) {
	if t.Tier != core.TierResume {
		return 1, 0
	}
	return 0, t.Item.ResumeOffset
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
		// A shared item keeps the better (slot, user rank) pair, and with it that
		// user's ID, so its fate does not depend on fetch order.
		//
		// Slot leads rank here to match the slot-major comparator below. Comparing
		// rank first would let a high-rank user's LATE slot evict a low-rank user's
		// EARLY slot, and the retained candidate would then sort behind the one it
		// displaced - delaying or budget-excluding an item that a user picked first.
		if cand.slot < existing.slot || (cand.slot == existing.slot && cand.rank < existing.rank) {
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
		// Third key, as a transitive PAIR: resumes ahead of non-resumes, then
		// deeper resume positions first. Comparing depth only when BOTH sides are
		// resumes would make a resume and a non-resume compare equal while two
		// resumes did not, which is intransitive and yields order-dependent
		// output from a stable sort rather than an error.
		ki, di := resumeKey(targets[i])
		kj, dj := resumeKey(targets[j])
		if ki != kj {
			return ki < kj
		}
		if di != dj {
			return di > dj
		}
		return false // stable: preserve input order
	})
	return targets
}
