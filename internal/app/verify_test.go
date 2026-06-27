package app

import (
	"testing"
)

type residentCache struct {
	resident int64
	known    bool
}

func (c residentCache) Warm(string, int64, int64) error { return nil }
func (c residentCache) Resident(_ string, _ int64, length int64) (int64, bool, error) {
	if !c.known {
		return 0, false, nil
	}
	if c.resident > length {
		return length, true, nil
	}
	return c.resident, true, nil
}

func TestVerifyResidencyPercent(t *testing.T) {
	pct, known, err := VerifyResidency(residentCache{resident: 50, known: true}, "/x", 0, 100)
	if err != nil || !known {
		t.Fatalf("err=%v known=%v", err, known)
	}
	if pct != 50.0 {
		t.Errorf("pct = %v, want 50", pct)
	}
}

func TestVerifyResidencyUnknown(t *testing.T) {
	_, known, _ := VerifyResidency(residentCache{known: false}, "/x", 0, 100)
	if known {
		t.Error("expected known=false on platforms without mincore")
	}
}
