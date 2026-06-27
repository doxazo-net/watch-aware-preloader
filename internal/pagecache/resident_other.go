//go:build !linux

package pagecache

// residentImpl cannot determine residency off Linux; callers warm unconditionally.
func residentImpl(_ string, _, _ int64) (int64, bool, error) {
	return 0, false, nil
}
