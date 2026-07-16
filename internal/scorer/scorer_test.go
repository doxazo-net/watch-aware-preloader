package scorer

import (
	"reflect"
	"testing"
	"time"

	"github.com/doxazo-net/watch-aware-preloader/internal/core"
)

func item(id string, off time.Duration) core.MediaItem {
	return core.MediaItem{ID: id, ResumeOffset: off}
}

// userItem builds an item attributed to a user; resume depth is irrelevant to
// these cases, so it stays zero.
func userItem(id, user string) core.MediaItem {
	return core.MediaItem{ID: id, UserID: user}
}

// uo pairs a user with their resolved tier order.
type uo = struct {
	user  string
	order []core.Tier
}

var defaultOrder = []core.Tier{core.TierResume, core.TierNextUp, core.TierRecentlyAdded}

// opts builds RankOpts for users given each user's order, in rank order.
func opts(orders ...uo) RankOpts {
	o := RankOpts{
		TierRank: map[string]map[core.Tier]int{},
		UserRank: map[string]int{},
	}
	for i, u := range orders {
		o.UserRank[u.user] = i
		m := map[core.Tier]int{}
		for pos, t := range u.order {
			m[t] = pos
		}
		o.TierRank[u.user] = m
	}
	return o
}

func targetIDs(ts []core.PreloadTarget) []string {
	ids := make([]string, 0, len(ts))
	for _, t := range ts {
		ids = append(ids, t.Item.ID)
	}
	return ids
}

// anon is the RankOpts for the pre-existing cases, whose items carry no user.
// The empty string is a valid map key, so those items are enrolled on the
// default order at a single rank.
func anon() RankOpts { return opts(uo{"", defaultOrder}) }

func TestRankExcludesNowPlaying(t *testing.T) {
	cands := []Candidate{
		{Item: item("a", 0), Tier: core.TierNextUp},
		{Item: item("b", 0), Tier: core.TierResume},
	}
	got := Rank(cands, map[string]bool{"b": true}, anon())
	if len(got) != 1 || got[0].Item.ID != "a" {
		t.Fatalf("expected only 'a', got %+v", got)
	}
}

func TestRankDedupesKeepingHighestPriority(t *testing.T) {
	cands := []Candidate{
		{Item: item("a", 0), Tier: core.TierRecentlyAdded},
		{Item: item("a", 0), Tier: core.TierResume}, // higher priority wins
	}
	got := Rank(cands, nil, anon())
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped target, got %d", len(got))
	}
	if got[0].Tier != core.TierResume {
		t.Fatalf("expected TierResume to win dedupe, got %v", got[0].Tier)
	}

	// KEEP-EXISTING branch: the higher-priority tier arrives FIRST, the lower
	// second. The seen entry must be retained, not overwritten by the later one.
	firstWins := Rank([]Candidate{
		{Item: item("a", 0), Tier: core.TierResume},
		{Item: item("a", 0), Tier: core.TierRecentlyAdded},
	}, nil, anon())
	if len(firstWins) != 1 {
		t.Fatalf("expected 1 deduped target, got %d", len(firstWins))
	}
	if firstWins[0].Tier != core.TierResume {
		t.Fatalf("expected TierResume to survive when it arrives first, got %v", firstWins[0].Tier)
	}
}

func TestRankOrdersByTierThenResumeOffset(t *testing.T) {
	cands := []Candidate{
		{Item: item("added", 0), Tier: core.TierRecentlyAdded},
		{Item: item("r-small", 1*time.Minute), Tier: core.TierResume},
		{Item: item("r-big", 30*time.Minute), Tier: core.TierResume},
		{Item: item("next", 0), Tier: core.TierNextUp},
	}
	got := Rank(cands, nil, anon())
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

func TestRankOverrideIsNotInert(t *testing.T) {
	// Bob promoted next-up, so his next-up is his slot-0 item and competes in
	// slot 0, where alice wins the tie on user rank. Bob's next-up therefore
	// lands second: ahead of alice's next-up and ahead of every slot-1 item,
	// including bob's own now-demoted resume.
	//
	// The defect this guards: comparing tiers against one global order would
	// sort bob's next-up behind every resume, making the override inert.
	cands := []Candidate{
		{Item: userItem("a-next", "alice"), Tier: core.TierNextUp},
		{Item: userItem("b-next", "bob"), Tier: core.TierNextUp},
		{Item: userItem("a-res", "alice"), Tier: core.TierResume},
		{Item: userItem("b-res", "bob"), Tier: core.TierResume},
	}
	got := Rank(cands, nil, opts(
		uo{"alice", defaultOrder},
		uo{"bob", []core.Tier{core.TierNextUp, core.TierResume}},
	))
	want := []string{"a-res", "b-next", "a-next", "b-res"}
	if ids := targetIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestRankUserRankBreaksSlotTie(t *testing.T) {
	cands := []Candidate{
		{Item: userItem("c-res", "cara"), Tier: core.TierResume},
		{Item: userItem("a-res", "alice"), Tier: core.TierResume},
		{Item: userItem("b-res", "bob"), Tier: core.TierResume},
	}
	got := Rank(cands, nil, opts(
		uo{"alice", defaultOrder}, uo{"bob", defaultOrder}, uo{"cara", defaultOrder},
	))
	want := []string{"a-res", "b-res", "c-res"}
	if ids := targetIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestRankEmptyOrderWarmsNothing(t *testing.T) {
	cands := []Candidate{
		{Item: userItem("b-res", "bob"), Tier: core.TierResume},
		{Item: userItem("a-res", "alice"), Tier: core.TierResume},
	}
	o := opts(uo{"alice", defaultOrder}, uo{"bob", defaultOrder})
	o.TierRank["bob"] = map[core.Tier]int{} // bob disabled everything
	got := Rank(cands, nil, o)
	if ids := targetIDs(got); !reflect.DeepEqual(ids, []string{"a-res"}) {
		t.Fatalf("order = %v, want [a-res]", ids)
	}
}

func TestRankUnenrolledUserContributesNothing(t *testing.T) {
	// A user absent from TierRank has no resolved order, so none of their items
	// are eligible.
	cands := []Candidate{
		{Item: userItem("a-res", "alice"), Tier: core.TierResume},
		{Item: userItem("z-res", "zed"), Tier: core.TierResume},
	}
	got := Rank(cands, nil, opts(uo{"alice", defaultOrder}))
	if ids := targetIDs(got); !reflect.DeepEqual(ids, []string{"a-res"}) {
		t.Fatalf("order = %v, want [a-res]", ids)
	}
}

func TestRankOmittedTierIsDropped(t *testing.T) {
	cands := []Candidate{
		{Item: userItem("a-rec", "alice"), Tier: core.TierRecentlyAdded},
		{Item: userItem("a-res", "alice"), Tier: core.TierResume},
	}
	got := Rank(cands, nil, opts(
		uo{"alice", []core.Tier{core.TierResume}},
	))
	if ids := targetIDs(got); !reflect.DeepEqual(ids, []string{"a-res"}) {
		t.Fatalf("order = %v, want [a-res]", ids)
	}
}

func TestRankSharedItemInheritsBestRank(t *testing.T) {
	// The same item surfaced by both users. Cara is ranked last but fetched
	// first; the deduped target must carry alice's ID and rank, so it sorts
	// ahead of bob's resume rather than behind it.
	cands := []Candidate{
		{Item: userItem("shared", "cara"), Tier: core.TierResume},
		{Item: userItem("b-res", "bob"), Tier: core.TierResume},
		{Item: userItem("shared", "alice"), Tier: core.TierResume},
	}
	got := Rank(cands, nil, opts(
		uo{"alice", defaultOrder}, uo{"bob", defaultOrder}, uo{"cara", defaultOrder},
	))
	want := []string{"shared", "b-res"}
	if ids := targetIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	if got[0].Item.UserID != "alice" {
		t.Fatalf("shared item UserID = %q, want alice (best rank wins)", got[0].Item.UserID)
	}
}

func TestRankDedupKeepsBetterSlotNotLowerEnum(t *testing.T) {
	// Same item at two tiers for one user who promoted next-up. The kept tier
	// must be next-up (his slot 0), NOT resume (the lower enum value).
	cands := []Candidate{
		{Item: userItem("x", "bob"), Tier: core.TierResume},
		{Item: userItem("x", "bob"), Tier: core.TierNextUp},
	}
	got := Rank(cands, nil, opts(
		uo{"bob", []core.Tier{core.TierNextUp, core.TierResume}},
	))
	if len(got) != 1 || got[0].Tier != core.TierNextUp {
		t.Fatalf("got %+v, want single next-up target", got)
	}
}
