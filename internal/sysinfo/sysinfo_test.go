package sysinfo

import (
	"math"
	"strings"
	"testing"
)

const sampleMeminfo = `MemTotal:       197565123 kB
MemFree:         6215254 kB
MemAvailable:  117000000 kB
Buffers:          123456 kB
`

func TestParseMemAvailable(t *testing.T) {
	got, err := ParseMemAvailable(strings.NewReader(sampleMeminfo))
	if err != nil {
		t.Fatal(err)
	}
	want := int64(117000000) * 1024 // kB -> bytes
	if got != want {
		t.Errorf("ParseMemAvailable = %d, want %d", got, want)
	}
}

func TestParseMemAvailableMissing(t *testing.T) {
	_, err := ParseMemAvailable(strings.NewReader("MemTotal: 100 kB\n"))
	if err == nil {
		t.Error("expected error when MemAvailable absent")
	}
}

func TestBudgetBytes(t *testing.T) {
	if got := BudgetBytes(1000, 50); got != 500 {
		t.Errorf("BudgetBytes(1000,50) = %d, want 500", got)
	}
	if got := BudgetBytes(1000, -5); got != 0 {
		t.Errorf("negative pct should clamp to 0, got %d", got)
	}
	if got := BudgetBytes(0, 50); got != 0 {
		t.Errorf("zero available should clamp to 0, got %d", got)
	}
	if got := BudgetBytes(-1, 50); got != 0 {
		t.Errorf("negative available should clamp to 0, got %d", got)
	}
	// Large-input case: a naive available*pct would overflow int64. Assert the
	// EXACT divide-before-multiply result (positive, so the > 0 intent is kept),
	// so a future precision regression is caught, not just a wrap-to-nonpositive.
	// For available=MaxInt64 (9223372036854775807), pct=10:
	//   (MaxInt64/100)*10 + (MaxInt64%100)*10/100
	//   = 92233720368547758*10 + 7*10/100
	//   = 922337203685477580 + 0 = 922337203685477580.
	if got := BudgetBytes(math.MaxInt64, 10); got != 922337203685477580 {
		t.Errorf("BudgetBytes(MaxInt64,10) = %d, want 922337203685477580", got)
	}
}
