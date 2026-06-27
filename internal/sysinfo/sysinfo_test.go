package sysinfo

import (
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
}
