// Package app assembles the media-server client, scorer, and preloader into a
// runnable pipeline.
package app

import (
	"context"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/sydlexius/watch-aware-preloader/internal/scorer"
)

// Provider is the subset of the Emby client the pipeline needs.
type Provider interface {
	Users(ctx context.Context) ([]emby.User, error)
	Resume(ctx context.Context, userID string) ([]core.MediaItem, error)
	NextUp(ctx context.Context, userID string) ([]core.MediaItem, error)
	RecentlyAdded(ctx context.Context, userID string) ([]core.MediaItem, error)
	NowPlayingIDs(ctx context.Context) (map[string]bool, error)
}

// ResolveUserIDs maps configured user names to IDs. An empty enabled list
// selects all users.
func ResolveUserIDs(users []emby.User, enabled []string) []string {
	if len(enabled) == 0 {
		ids := make([]string, 0, len(users))
		for _, u := range users {
			ids = append(ids, u.ID)
		}
		return ids
	}
	want := map[string]bool{}
	for _, n := range enabled {
		want[n] = true
	}
	var ids []string
	for _, u := range users {
		if want[u.Name] {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

// CollectCandidates fetches tiers 1-3 for each enabled user plus the global
// now-playing set.
func CollectCandidates(ctx context.Context, p Provider, enabled []string) ([]scorer.Candidate, map[string]bool, error) {
	users, err := p.Users(ctx)
	if err != nil {
		return nil, nil, err
	}
	playing, err := p.NowPlayingIDs(ctx)
	if err != nil {
		return nil, nil, err
	}

	var cands []scorer.Candidate
	add := func(items []core.MediaItem, tier core.Tier) {
		for _, it := range items {
			cands = append(cands, scorer.Candidate{Item: it, Tier: tier})
		}
	}
	for _, id := range ResolveUserIDs(users, enabled) {
		resume, err := p.Resume(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		add(resume, core.TierResume)

		nextUp, err := p.NextUp(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		add(nextUp, core.TierNextUp)

		latest, err := p.RecentlyAdded(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		add(latest, core.TierRecentlyAdded)
	}
	return cands, playing, nil
}
