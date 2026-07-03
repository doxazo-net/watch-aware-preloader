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

// Rank filters out actively-playing items, dedupes by item ID (keeping the
// highest-priority tier), and orders the result by tier then resume depth.
func Rank(candidates []Candidate, nowPlaying map[string]bool) []core.PreloadTarget {
	best := make(map[string]Candidate, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if nowPlaying[c.Item.ID] {
			continue
		}
		existing, seen := best[c.Item.ID]
		if !seen {
			best[c.Item.ID] = c
			order = append(order, c.Item.ID)
			continue
		}
		if c.Tier < existing.Tier {
			best[c.Item.ID] = c
		}
	}

	targets := make([]core.PreloadTarget, 0, len(order))
	for _, id := range order {
		c := best[id]
		targets = append(targets, core.PreloadTarget{Item: c.Item, Tier: c.Tier})
	}

	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Tier != targets[j].Tier {
			return targets[i].Tier < targets[j].Tier
		}
		// Within the resume tier, deeper resume positions go first.
		if targets[i].Tier == core.TierResume {
			return targets[i].Item.ResumeOffset > targets[j].Item.ResumeOffset
		}
		return false // stable: preserve input order
	})
	return targets
}
