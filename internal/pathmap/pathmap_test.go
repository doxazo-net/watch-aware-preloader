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

func TestToHostMapsUNCPath(t *testing.T) {
	// SMB-added Emby/Jellyfin libraries report Windows UNC paths. A rule anchored
	// on the UNC host/share must rewrite them to the POSIX host root.
	m := New([]Rule{{From: `\\host`, To: "/mnt/user"}})
	got, ok := m.ToHost(`\\host\Movies\Example (2024)\example.mkv`)
	if !ok {
		t.Fatal("expected UNC path to match")
	}
	if want := "/mnt/user/Movies/Example (2024)/example.mkv"; got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostUNCNoMidSegmentMatch(t *testing.T) {
	// The segment-boundary guard must still hold after backslash normalization.
	m := New([]Rule{{From: `\\host`, To: "/mnt/user"}})
	if got, ok := m.ToHost(`\\hostXYZ\Movies\example.mkv`); ok {
		t.Errorf("expected no match for mid-segment UNC host, got %q", got)
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

func TestToHostUNCExactMatch(t *testing.T) {
	// A server path equal to the UNC rule prefix (no trailing segment) must match.
	m := New([]Rule{{From: `\\host`, To: "/mnt/user"}})
	got, ok := m.ToHost(`\\host`)
	if !ok {
		t.Fatal("expected exact UNC prefix to match")
	}
	if want := "/mnt/user"; got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostUNCMultipleRulesLongestWins(t *testing.T) {
	// When both \\host and \\host\Share match, the longer prefix must win.
	m := New([]Rule{
		{From: `\\host`, To: "/mnt/user"},
		{From: `\\host\Movies`, To: "/mnt/disk1/Movies"},
	})
	got, ok := m.ToHost(`\\host\Movies\Film (2024)\film.mkv`)
	if !ok {
		t.Fatal("expected match")
	}
	if want := "/mnt/disk1/Movies/Film (2024)/film.mkv"; got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostEmptyRulesUNCPassthrough(t *testing.T) {
	// With no rules the original path (including backslashes) is returned unchanged
	// because the normalization step is skipped.
	m := New(nil)
	unc := `\\host\Movies\film.mkv`
	got, ok := m.ToHost(unc)
	if !ok || got != unc {
		t.Errorf("empty mapper should pass UNC through unchanged, got %q ok=%v", got, ok)
	}
}

func TestToHostRuleWithBackslashToNormalized(t *testing.T) {
	// A To value spelled with backslashes (unusual but possible on Windows) is
	// normalized to forward slashes by New(), so the rewritten host path uses /.
	m := New([]Rule{{From: `\\host\Share`, To: `C:\mnt\user`}})
	got, ok := m.ToHost(`\\host\Share\video.mkv`)
	if !ok {
		t.Fatal("expected match")
	}
	if want := "C:/mnt/user/video.mkv"; got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}

func TestToHostUNCMixedSlashesInServerPath(t *testing.T) {
	// A server path containing a mix of backslash and forward slash separators
	// (e.g. from a buggy client) should still match after normalization.
	m := New([]Rule{{From: `\\host`, To: "/mnt/user"}})
	got, ok := m.ToHost(`\\host\Movies/Example/film.mkv`)
	if !ok {
		t.Fatal("expected mixed-slash UNC path to match")
	}
	if want := "/mnt/user/Movies/Example/film.mkv"; got != want {
		t.Errorf("ToHost = %q, want %q", got, want)
	}
}
