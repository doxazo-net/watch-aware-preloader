package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/config"
	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// allTiers returns a TiersConfig with every tier enabled and no cap (the
// applyDefaults result), for tests that call the pipeline directly.
func allTiers() config.TiersConfig {
	return config.TiersConfig{
		Resume:        config.TierDial{Enabled: true},
		NextUp:        config.TierDial{Enabled: true},
		RecentlyAdded: config.TierDial{Enabled: true},
	}
}

type stubProvider struct {
	users     []emby.User
	libraries []emby.Library
	resume    map[string][]core.MediaItem
	nextUp    map[string][]core.MediaItem
	latest    map[string][]core.MediaItem
	playing   map[string]bool
}

func (s *stubProvider) Users(context.Context) ([]emby.User, error) { return s.users, nil }
func (s *stubProvider) Libraries(context.Context) ([]emby.Library, error) {
	return s.libraries, nil
}
func (s *stubProvider) Resume(_ context.Context, id string) ([]core.MediaItem, error) {
	return s.resume[id], nil
}
func (s *stubProvider) NextUp(_ context.Context, id string) ([]core.MediaItem, error) {
	return s.nextUp[id], nil
}
func (s *stubProvider) RecentlyAdded(_ context.Context, id string) ([]core.MediaItem, error) {
	return s.latest[id], nil
}
func (s *stubProvider) NowPlayingIDs(context.Context) (map[string]bool, error) {
	return s.playing, nil
}

func TestResolveUserIDsAllWhenEmpty(t *testing.T) {
	users := []emby.User{{ID: "1", Name: "jesse"}, {ID: "2", Name: "rachel"}}
	got := ResolveUserIDs(users, nil)
	if len(got) != 2 {
		t.Fatalf("expected all users, got %v", got)
	}
}

func TestResolveUserIDsFiltersByName(t *testing.T) {
	users := []emby.User{{ID: "1", Name: "jesse"}, {ID: "2", Name: "rachel"}}
	got := ResolveUserIDs(users, []string{"rachel"})
	if len(got) != 1 || got[0] != "2" {
		t.Fatalf("expected [2], got %v", got)
	}
}

func TestCollectCandidatesTiersAndPlaying(t *testing.T) {
	p := &stubProvider{
		users:   []emby.User{{ID: "1", Name: "jesse"}},
		resume:  map[string][]core.MediaItem{"1": {{ID: "r1"}}},
		nextUp:  map[string][]core.MediaItem{"1": {{ID: "n1"}}},
		latest:  map[string][]core.MediaItem{"1": {{ID: "l1"}}},
		playing: map[string]bool{"x": true},
	}
	cands, playing, err := CollectCandidates(context.Background(), p, nil, nil, allTiers(), nil, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(cands))
	}
	tierByID := map[string]core.Tier{}
	for _, c := range cands {
		tierByID[c.Item.ID] = c.Tier
	}
	if tierByID["r1"] != core.TierResume || tierByID["n1"] != core.TierNextUp || tierByID["l1"] != core.TierRecentlyAdded {
		t.Errorf("tiers assigned wrong: %v", tierByID)
	}
	if !playing["x"] {
		t.Error("now-playing set not returned")
	}
}

func TestCollectCandidatesLibraryScope(t *testing.T) {
	// Two candidates: one in the Movies library, one in Music. Scoping to
	// Movies (id "m") must drop the Music item.
	p := &stubProvider{
		users: []emby.User{{ID: "1", Name: "jesse"}},
		libraries: []emby.Library{
			{ID: "m", Name: "Movies", Locations: []string{"/share/Movies"}},
			{ID: "u", Name: "Music", Locations: []string{"/share/Music"}},
		},
		latest: map[string][]core.MediaItem{"1": {
			{ID: "movie", ServerPath: `\\host\Movies\a.mkv`},
			{ID: "song", ServerPath: `\\host\Music\b.flac`},
		}},
	}
	// toHost mirrors the mapper for this topology: UNC and /share both -> /mnt/user.
	toHost := func(path string) (string, bool) {
		if len(path) > 2 && path[:2] == `\\` {
			rest := path[2:]
			for i := 0; i < len(rest); i++ {
				if rest[i] == '\\' {
					return "/mnt/user/" + replaceBackslash(rest[i+1:]), true
				}
			}
			return "", false
		}
		if len(path) >= 7 && path[:7] == "/share/" {
			return "/mnt/user/" + path[7:], true
		}
		return "", false
	}
	cands, _, err := CollectCandidates(context.Background(), p, nil, []string{"m"}, allTiers(), toHost, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Item.ID != "movie" {
		t.Fatalf("library scope should keep only the Movies item, got %+v", cands)
	}
}

func TestCollectCandidatesTierDials(t *testing.T) {
	p := &stubProvider{
		users:  []emby.User{{ID: "1", Name: "jesse"}},
		resume: map[string][]core.MediaItem{"1": {{ID: "r1"}}},
		nextUp: map[string][]core.MediaItem{"1": {{ID: "n1"}}},
		latest: map[string][]core.MediaItem{"1": {{ID: "l1"}, {ID: "l2"}, {ID: "l3"}}},
	}
	// Disable next-up entirely; cap recently-added to 2; keep resume on.
	tiers := config.TiersConfig{
		Resume:        config.TierDial{Enabled: true},
		NextUp:        config.TierDial{Enabled: false},
		RecentlyAdded: config.TierDial{Enabled: true, MaxItems: 2},
	}
	cands, _, err := CollectCandidates(context.Background(), p, nil, nil, tiers, nil, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]core.Tier{}
	for _, c := range cands {
		ids[c.Item.ID] = c.Tier
	}
	if _, ok := ids["n1"]; ok {
		t.Error("disabled next-up tier should contribute no candidates")
	}
	if _, ok := ids["r1"]; !ok {
		t.Error("enabled resume tier should contribute")
	}
	// recently-added capped at 2: l1,l2 kept, l3 dropped.
	if _, ok := ids["l3"]; ok {
		t.Error("recently-added cap of 2 should drop the third item")
	}
	if _, ok := ids["l1"]; !ok {
		t.Error("recently-added cap of 2 should keep the first item")
	}
	if len(cands) != 3 { // r1 + l1 + l2
		t.Errorf("expected 3 candidates (resume 1 + recently-added 2), got %d", len(cands))
	}
}

func replaceBackslash(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] == '\\' {
			b[i] = '/'
		}
	}
	return string(b)
}
