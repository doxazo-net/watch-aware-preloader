package pathmap

import "testing"

func TestToHostLongestPrefixWins(t *testing.T) {
	m := New([]Rule{
		{From: "/share", To: "/mnt/user"},
		{From: "/share/TV_Shows", To: "/mnt/disk1/TV_Shows"},
	})
	got, ok := m.ToHost("/share/TV_Shows/Slow Horses/s05e01.mkv")
	if !ok {
		t.Fatal("expected a match")
	}
	want := "/mnt/disk1/TV_Shows/Slow Horses/s05e01.mkv"
	if got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostNoMatch(t *testing.T) {
	m := New([]Rule{{From: "/share", To: "/mnt/user"}})
	if _, ok := m.ToHost("/data/movie.mkv"); ok {
		t.Error("expected no match for unmapped path")
	}
}

func TestToHostNoMidSegmentMatch(t *testing.T) {
	m := New([]Rule{{From: "/share", To: "/mnt/user"}})
	if got, ok := m.ToHost("/shareXYZ/movie.mkv"); ok {
		t.Errorf("expected no match for mid-segment prefix, got %q", got)
	}
}

func TestToHostNormalizesTrailingSlash(t *testing.T) {
	// A common config spelling "/share/" must still match "/share/movie.mkv"
	// without the boundary check turning into "/share//".
	m := New([]Rule{{From: "/share/", To: "/mnt/user/"}})
	got, ok := m.ToHost("/share/movie.mkv")
	if !ok {
		t.Fatal("expected a match for trailing-slash prefix")
	}
	if want := "/mnt/user/movie.mkv"; got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostEmptyRulesPassThrough(t *testing.T) {
	// With no rules, server path is assumed already host-correct.
	m := New(nil)
	got, ok := m.ToHost("/mnt/user/TV/x.mkv")
	if !ok || got != "/mnt/user/TV/x.mkv" {
		t.Errorf("empty mapper should pass through, got %q ok=%v", got, ok)
	}
}
