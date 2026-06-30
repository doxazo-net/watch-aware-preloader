package pagecache

import (
	"testing"
	"time"
)

func TestClassifyCached(t *testing.T) {
	const threshold = 150 * time.Millisecond
	cases := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{"well under threshold (RAM)", 2 * time.Millisecond, true},
		{"just under threshold", 149 * time.Millisecond, true},
		{"exactly at threshold is cold", threshold, false},
		{"over threshold (cold disk)", 800 * time.Millisecond, false},
		{"zero elapsed is cached", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyCached(tc.elapsed, threshold); got != tc.want {
				t.Errorf("classifyCached(%v, %v) = %v, want %v", tc.elapsed, threshold, got, tc.want)
			}
		})
	}
}
