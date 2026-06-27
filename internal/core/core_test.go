package core

import "testing"

func TestTierString(t *testing.T) {
	cases := map[Tier]string{
		TierResume:        "resume",
		TierNextUp:        "next-up",
		TierRecentlyAdded: "recently-added",
		TierBingeAhead:    "binge-ahead",
		TierBestEffort:    "best-effort",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", int(tier), got, want)
		}
	}
}

func TestTierOrdering(t *testing.T) {
	// Lower value = higher priority; ordering must be stable for the scorer.
	if !(TierResume < TierNextUp && TierNextUp < TierRecentlyAdded &&
		TierRecentlyAdded < TierBingeAhead && TierBingeAhead < TierBestEffort) {
		t.Fatal("tier constants are not in ascending priority order")
	}
}
