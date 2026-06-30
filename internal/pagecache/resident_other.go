//go:build !linux

package pagecache

// residentImpl cannot determine residency off Linux; callers warm unconditionally.
func residentImpl(_ *osCache, _ string, _, _ int64) (int64, bool, error) {
	return 0, false, nil
}

// defaultStatfs is unused off Linux but keeps New() platform-neutral.
func defaultStatfs(_ string) (uint32, error) { return 0, nil }

// residencyMethod reports that residency is unavailable off Linux.
func residencyMethod(_ *osCache, _ string) string { return "unavailable" }
