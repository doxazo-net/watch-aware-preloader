// Package app assembles the media-server client, scorer, and preloader into a
// runnable pipeline.
package app

import (
	"context"
	"log/slog"

	"github.com/doxazo-net/watch-aware-preloader/internal/config"
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/libscope"
	"github.com/doxazo-net/watch-aware-preloader/internal/mediaserver/emby"
	"github.com/doxazo-net/watch-aware-preloader/internal/scorer"
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

// capItems returns at most limit items (in the server's order, which is
// relevance-ordered per tier). limit <= 0 means no cap.
func capItems(items []core.MediaItem, limit int) []core.MediaItem {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

// ResolveUserIDs maps configured user IDs or names to IDs. An entry matches a user
// by u.ID or u.Name (IDs are GUIDs and names are human strings, so no collision).
// An empty enabled list selects all users.
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
		if want[u.ID] || want[u.Name] {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

// CollectCandidates fetches the enabled signal tiers for each enabled user plus
// the global now-playing set. The tiers dials skip a disabled tier entirely (no
// fetch) and cap each tier's per-user contribution to MaxItems (0 = no cap).
// When enabledLibraries is non-empty, candidates are filtered to items that fall
// under one of those libraries; toHost (the preloader's path mapper) normalizes
// item paths and library locations to a common host-path namespace for the
// comparison. An empty enabledLibraries leaves candidates unfiltered.
func CollectCandidates(ctx context.Context, p Provider, enabled, enabledLibraries []string, tiers config.TiersConfig, toHost libscope.ToHost, log *slog.Logger) ([]scorer.Candidate, map[string]bool, error) {
	users, err := p.Users(ctx)
	if err != nil {
		return nil, nil, err
	}
	playing, err := p.NowPlayingIDs(ctx)
	if err != nil {
		return nil, nil, err
	}

	var cands []scorer.Candidate
	add := func(items []core.MediaItem, dial config.TierDial, tier core.Tier) {
		for _, it := range capItems(items, dial.MaxItems) {
			cands = append(cands, scorer.Candidate{Item: it, Tier: tier})
		}
	}
	for _, id := range ResolveUserIDs(users, enabled) {
		if tiers.Resume.Enabled {
			resume, err := p.Resume(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			add(resume, tiers.Resume, core.TierResume)
		}
		if tiers.NextUp.Enabled {
			nextUp, err := p.NextUp(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			add(nextUp, tiers.NextUp, core.TierNextUp)
		}
		if tiers.RecentlyAdded.Enabled {
			latest, err := p.RecentlyAdded(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			add(latest, tiers.RecentlyAdded, core.TierRecentlyAdded)
		}
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
