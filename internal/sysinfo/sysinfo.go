// Package sysinfo reads host memory information for the preload budget.
package sysinfo

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ParseMemAvailable extracts MemAvailable (in bytes) from /proc/meminfo content.
func ParseMemAvailable(r io.Reader) (int64, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line) // ["MemAvailable:", "117000000", "kB"]
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed MemAvailable line: %q", line)
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsing MemAvailable value: %w", err)
		}
		return kb * 1024, nil
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("MemAvailable not found in meminfo")
}

// AvailableBytes reads /proc/meminfo and returns MemAvailable in bytes.
func AvailableBytes() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read-only file; close error not actionable
	return ParseMemAvailable(f)
}

// BudgetBytes returns pct percent of available, clamped to a non-negative value.
func BudgetBytes(available int64, pct int) int64 {
	if pct <= 0 || available <= 0 {
		return 0
	}
	// Divide-before-multiply to avoid overflowing int64 on large available values;
	// the second term recovers the remainder so there is no precision loss.
	return (available/100)*int64(pct) + (available%100)*int64(pct)/100
}
