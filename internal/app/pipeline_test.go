package app

import (
	"context"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/core"
	"github.com/sydlexius/watch-aware-preloader/internal/mediaserver/emby"
)

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
	cands, playing, err := CollectCandidates(context.Background(), p, nil, nil, nil)
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
	cands, _, err := CollectCandidates(context.Background(), p, nil, []string{"m"}, toHost)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Item.ID != "movie" {
		t.Fatalf("library scope should keep only the Movies item, got %+v", cands)
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
