// Package core holds the domain types shared across the preloader units.
package core

import "time"

// Tier is the preload priority class. The integer values are NOT a priority
// order: priority is configuration data resolved at runtime (see
// config.TierOrder and scorer.RankOpts). Never compare two Tier values with
// < or > to mean "higher priority"; look up their position in the resolved
// order instead.
type Tier int

// Preload signal tiers. Declaration order is the DEFAULT global order only
// (applied by config.applyDefaults); it carries no meaning at comparison time.
const (
	TierResume        Tier = iota // recent incompletes, not currently playing
	TierNextUp                    // next episode of an active series
	TierRecentlyAdded             // recently added, unwatched
	TierBingeAhead                // episode after next-up (reserved; Phase 3)
	TierBestEffort                // filesystem-recency fill
)

// String returns the lowercase tier label used in structured logs.
func (t Tier) String() string {
	switch t {
	case TierResume:
		return "resume"
	case TierNextUp:
		return "next-up"
	case TierRecentlyAdded:
		return "recently-added"
	case TierBingeAhead:
		return "binge-ahead"
	case TierBestEffort:
		return "best-effort"
	default:
		return "unknown"
	}
}

// MediaItem is a normalized media file surfaced by the media server.
type MediaItem struct {
	ID           string
	Name         string
	ServerPath   string        // path as the media server reports it
	BitrateBps   int64         // average bits per second; 0 if unknown
	SizeBytes    int64         // file size in bytes
	Runtime      time.Duration // total playback duration
	ResumeOffset time.Duration // playback position for resume items; 0 otherwise
	UserID       string        // the user account that surfaced this item
}

// PreloadTarget is a scored, ordered item ready to preload.
type PreloadTarget struct {
	Item MediaItem
	Tier Tier
}
