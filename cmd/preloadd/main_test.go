package main

import (
	"testing"
)

func TestSelectMode(t *testing.T) {
	tests := []struct {
		name     string
		once     bool
		daemon   bool
		verify   bool
		estimate bool
		wantMode string
		wantErr  bool
	}{
		{name: "default (no flags)", once: false, daemon: false, verify: false, estimate: false, wantMode: "once"},
		{name: "explicit -once", once: true, daemon: false, verify: false, estimate: false, wantMode: "once"},
		{name: "-daemon", once: false, daemon: true, verify: false, estimate: false, wantMode: "daemon"},
		{name: "-verify", once: false, daemon: false, verify: true, estimate: false, wantMode: "verify"},
		{name: "-verify wins over -once/-daemon conflict", once: true, daemon: true, verify: true, estimate: false, wantMode: "verify"},
		{name: "-verify with -daemon", once: false, daemon: true, verify: true, estimate: false, wantMode: "verify"},
		{name: "-once and -daemon conflict", once: true, daemon: true, verify: false, estimate: false, wantErr: true},
		{name: "-estimate", once: false, daemon: false, verify: false, estimate: true, wantMode: "estimate"},
		{name: "-verify beats -estimate", once: false, daemon: false, verify: true, estimate: true, wantMode: "verify"},
		{name: "-estimate beats -once/-daemon conflict", once: true, daemon: true, verify: false, estimate: true, wantMode: "estimate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectMode(tc.once, tc.daemon, tc.verify, tc.estimate)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantMode {
				t.Errorf("selectMode(%v,%v,%v,%v) = %q, want %q", tc.once, tc.daemon, tc.verify, tc.estimate, got, tc.wantMode)
			}
		})
	}
}
