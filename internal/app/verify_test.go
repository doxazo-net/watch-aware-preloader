package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/sydlexius/watch-aware-preloader/internal/preloader"
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

func TestReportResidency(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("known", func(t *testing.T) {
		cache := residentCache{resident: 80, known: true}
		warmed := []preloader.WarmedRange{
			{Path: "/a", Offset: 0, Length: 100},
			{Path: "/b", Offset: 0, Length: 100},
		}
		mean, anyKnown := ReportResidency(cache, warmed, log)
		if !anyKnown {
			t.Fatal("expected anyKnown=true")
		}
		if mean != 80.0 {
			t.Errorf("mean = %v, want 80.0", mean)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		cache := residentCache{known: false}
		warmed := []preloader.WarmedRange{
			{Path: "/a", Offset: 0, Length: 100},
		}
		_, anyKnown := ReportResidency(cache, warmed, log)
		if anyKnown {
			t.Error("expected anyKnown=false on platforms without mincore")
		}
	})
}
