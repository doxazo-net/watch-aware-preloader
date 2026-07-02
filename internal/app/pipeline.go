// Package app assembles the media-server client, scorer, and preloader into a
// runnable pipeline.
package app

import (
	"context"
	"log/slog"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/libscope"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/sydlexius/watch-aware-preloader/internal/scorer"
)

// Provider is the subset of the Emby client the pipeline needs.
type Provider interface {
	Users(ctx context.Context) ([]emby.User, error)
	Libraries(ctx context.Context) ([]emby.Library, error)
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
// now-playing set. When enabledLibraries is non-empty, candidates are filtered
// to items that fall under one of those libraries; toHost (the preloader's path
// mapper) normalizes item paths and library locations to a common host-path
// namespace for the comparison. An empty enabledLibraries leaves candidates
// unfiltered (all libraries).
func CollectCandidates(ctx context.Context, p Provider, enabled, enabledLibraries []string, toHost libscope.ToHost, log *slog.Logger) ([]scorer.Candidate, map[string]bool, error) {
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

	if len(enabledLibraries) > 0 {
		libs, err := p.Libraries(ctx)
		if err != nil {
			return nil, nil, err
		}
		cands = filterByLibraries(cands, libs, enabledLibraries, toHost, log)
	}
	return cands, playing, nil
}

// filterByLibraries keeps only candidates whose item falls under one of the
// enabled libraries, using toHost to normalize item paths and library locations
// to a common host-path namespace. When the requested scope cannot be applied
// (a bad library ID, or paths that do not map), it logs a warning and warms all
// libraries rather than failing silently.
func filterByLibraries(cands []scorer.Candidate, libs []emby.Library, enabledLibraries []string, toHost libscope.ToHost, log *slog.Logger) []scorer.Candidate {
	scopeLibs := make([]libscope.Library, len(libs))
	for i, l := range libs {
		scopeLibs[i] = libscope.Library{ID: l.ID, Locations: l.Locations}
	}
	scope, fellBack := libscope.New(scopeLibs, enabledLibraries, toHost)
	if fellBack && log != nil {
		log.Warn("library scope requested but no selected library resolved to a host path; warming all libraries",
			"enabled_libraries", enabledLibraries)
	}
	filtered := make([]scorer.Candidate, 0, len(cands))
	for _, c := range cands {
		if scope.Allowed(c.Item.ServerPath) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
