package scorer

import (
	"testing"
	"time"

	"github.com/doxazo-net/watch-aware-preloader/internal/core"
)

func item(id string, off time.Duration) core.MediaItem {
	return core.MediaItem{ID: id, ResumeOffset: off}
}

func TestRankExcludesNowPlaying(t *testing.T) {
	cands := []Candidate{
		{Item: item("a", 0), Tier: core.TierNextUp},
		{Item: item("b", 0), Tier: core.TierResume},
	}
	got := Rank(cands, map[string]bool{"b": true})
	if len(got) != 1 || got[0].Item.ID != "a" {
		t.Fatalf("expected only 'a', got %+v", got)
	}
}

func TestRankDedupesKeepingHighestPriority(t *testing.T) {
	cands := []Candidate{
		{Item: item("a", 0), Tier: core.TierRecentlyAdded},
		{Item: item("a", 0), Tier: core.TierResume}, // higher priority wins
	}
	got := Rank(cands, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped target, got %d", len(got))
	}
	if got[0].Tier != core.TierResume {
		t.Fatalf("expected TierResume to win dedupe, got %v", got[0].Tier)
	}
}

func TestRankOrdersByTierThenResumeOffset(t *testing.T) {
	cands := []Candidate{
		{Item: item("added", 0), Tier: core.TierRecentlyAdded},
		{Item: item("r-small", 1*time.Minute), Tier: core.TierResume},
		{Item: item("r-big", 30*time.Minute), Tier: core.TierResume},
		{Item: item("next", 0), Tier: core.TierNextUp},
	}
	got := Rank(cands, nil)
	wantOrder := []string{"r-big", "r-small", "next", "added"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d targets, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].Item.ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].Item.ID, id)
		}
	}
}
