package app

import (
	"github.com/doxazo-net/watch-aware-preloader/internal/core"
	"github.com/doxazo-net/watch-aware-preloader/internal/preloader"
)

// estimateResumeFrontBytes is a fixed, deliberately-high allowance for the
// front-metadata window a resume/seeking target warms. The real front window is
// the parsed container FrontEnd (<= 16 MiB), which needs disk I/O to measure; the
// projection uses this constant upper bound instead so the meter errs high (warns
// before the budget is actually blown) without touching the array.
const estimateResumeFrontBytes = 16 << 20

// projectBytes estimates the bytes a single target would warm, without any disk
// I/O. It is the same two-stage sizing a real sweep uses (preloader.HeadBytes is
// bitrate-based when BitrateBps is known and geometry-based - SizeBytes/Runtime -
// otherwise) plus the flat tail window, plus a fixed front allowance for resume
// targets. It intentionally over-estimates slightly relative to a real sweep.
func projectBytes(cfg preloader.Config, it core.MediaItem, tier core.Tier) int64 {
	b := preloader.HeadBytes(cfg, it) + cfg.TailBytes
	if tier == core.TierResume {
		b += estimateResumeFrontBytes
	}
	return b
}
