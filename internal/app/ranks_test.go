package app

import (
	"reflect"
	"testing"

	"github.com/doxazo-net/watch-aware-preloader/internal/config"
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/mediaserver/emby"
)

var testUsers = []emby.User{
	{ID: "id-a", Name: "Alice"},
	{ID: "id-b", Name: "Bob"},
	{ID: "id-c", Name: "Cara"},
}

func TestResolveRanksUsesConfigOrderNotServerOrder(t *testing.T) {
	cfg := &config.Config{}
	cfg.Users.Enabled = []string{"Cara", "Alice"} // deliberately not server order
	cfg.Tiers.Order = config.DefaultTierOrder()

	got := ResolveRanks(cfg, testUsers, discardLog())

	if got.UserRank["id-c"] != 0 || got.UserRank["id-a"] != 1 {
		t.Fatalf("UserRank = %v, want cara=0 alice=1", got.UserRank)
	}
	if _, ok := got.TierRank["id-b"]; ok {
		t.Fatal("bob is not enrolled and must contribute nothing")
	}
}

func TestResolveRanksEmptyEnabledIsAllUsersEqualRank(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tiers.Order = config.DefaultTierOrder()

	got := ResolveRanks(cfg, testUsers, discardLog())

	for _, id := range []string{"id-a", "id-b", "id-c"} {
		if got.UserRank[id] != 0 {
			t.Fatalf("UserRank[%s] = %d, want 0 (equal rank)", id, got.UserRank[id])
		}
		if len(got.TierRank[id]) != 3 {
			t.Fatalf("TierRank[%s] = %v, want all three tiers", id, got.TierRank[id])
		}
	}
}

func TestResolveRanksOverrideBindsByNameOrID(t *testing.T) {
	cfg := &config.Config{}
	cfg.Users.Enabled = []string{"id-a", "Bob"}
	cfg.Tiers.Order = config.DefaultTierOrder()
	cfg.Tiers.Override = map[string]config.TierOrder{
		"Alice": {core.TierNextUp},                  // by display name
		"id-b":  {core.TierResume, core.TierNextUp}, // by ID
	}

	got := ResolveRanks(cfg, testUsers, discardLog())

	if want := (map[core.Tier]int{core.TierNextUp: 0}); !reflect.DeepEqual(got.TierRank["id-a"], want) {
		t.Fatalf("TierRank[id-a] = %v, want %v", got.TierRank["id-a"], want)
	}
	if want := (map[core.Tier]int{core.TierResume: 0, core.TierNextUp: 1}); !reflect.DeepEqual(got.TierRank["id-b"], want) {
		t.Fatalf("TierRank[id-b] = %v, want %v", got.TierRank["id-b"], want)
	}
}

func TestResolveRanksInheritsByAbsence(t *testing.T) {
	cfg := &config.Config{}
	cfg.Users.Enabled = []string{"Alice", "Bob"}
	cfg.Tiers.Order = config.TierOrder{core.TierResume, core.TierNextUp}
	cfg.Tiers.Override = map[string]config.TierOrder{"Bob": {core.TierNextUp}}

	got := ResolveRanks(cfg, testUsers, discardLog())

	want := map[core.Tier]int{core.TierResume: 0, core.TierNextUp: 1}
	if !reflect.DeepEqual(got.TierRank["id-a"], want) {
		t.Fatalf("alice TierRank = %v, want the global %v", got.TierRank["id-a"], want)
	}
}

func TestResolveRanksDuplicateUserKeepsFirstRank(t *testing.T) {
	// Alice listed twice, by name and by ID. She must keep her first (best) rank,
	// and the duplicate must not perturb the users listed after her.
	cfg := &config.Config{}
	cfg.Users.Enabled = []string{"Alice", "id-a", "Bob"}
	cfg.Tiers.Order = config.DefaultTierOrder()

	got := ResolveRanks(cfg, testUsers, discardLog())

	if got.UserRank["id-a"] != 0 {
		t.Fatalf("UserRank[id-a] = %d, want 0 (first rank retained)", got.UserRank["id-a"])
	}
	if got.UserRank["id-b"] != 1 {
		t.Fatalf("UserRank[id-b] = %d, want 1 (duplicate must not collide)", got.UserRank["id-b"])
	}
	if len(got.UserRank) != 2 {
		t.Fatalf("UserRank = %v, want exactly alice and bob", got.UserRank)
	}
}

func TestResolveRanksUnknownOverrideIgnored(t *testing.T) {
	cfg := &config.Config{}
	cfg.Users.Enabled = []string{"Alice"}
	cfg.Tiers.Order = config.DefaultTierOrder()
	cfg.Tiers.Override = map[string]config.TierOrder{"Ghost": {core.TierResume}}

	got := ResolveRanks(cfg, testUsers, discardLog()) // must not panic

	if len(got.TierRank) != 1 {
		t.Fatalf("TierRank = %v, want only alice", got.TierRank)
	}
}
