package pagecache

import "time"

// classifyCached reports whether a probe read that took elapsed indicates the
// range was already page-cache resident. A read served from RAM returns far
// faster than the threshold; a read that touched a physical (possibly
// spun-down) disk returns slower. The threshold detects this categorical
// difference, so it need not scale with probe size.
func classifyCached(elapsed, threshold time.Duration) bool {
	return elapsed < threshold
}
