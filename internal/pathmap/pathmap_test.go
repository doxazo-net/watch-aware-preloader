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

func TestToHostExactMatch(t *testing.T) {
	// The canonical == r.From branch: a serverPath equal to the rule prefix maps
	// straight to the To root with no trailing remainder.
	m := New([]Rule{{From: "/share", To: "/mnt/user"}})
	got, ok := m.ToHost("/share")
	if !ok {
		t.Fatal("expected an exact-match hit")
	}
	if want := "/mnt/user"; got != want {
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

func TestToHostPreservesPosixBackslash(t *testing.T) {
	// On POSIX a backslash is a legal filename character; a non-UNC path must be
	// matched verbatim, never rewritten (regression for the blanket-ReplaceAll bug).
	m := New([]Rule{{From: "/share", To: "/mnt/user"}})
	got, ok := m.ToHost(`/share/Artist\Name/movie.mkv`)
	if !ok {
		t.Fatal("expected match")
	}
	if want := `/mnt/user/Artist\Name/movie.mkv`; got != want {
		t.Errorf("ToHost = %q, want %q (literal backslash must be preserved)", got, want)
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

func TestUnraidUNCFallback(t *testing.T) {
	m := New(nil, WithUnraidUNCFallback())
	cases := []struct {
		in, want string
		ok       bool
	}{
		{`\\tower\Movies\Film\a.mkv`, "/mnt/user/Movies/Film/a.mkv", true},
		{`\\TOWER\TV\Show\S01E01.mkv`, "/mnt/user/TV/Show/S01E01.mkv", true}, // host case-agnostic
		{`\\host\Share`, "/mnt/user/Share", true},                            // no trailing segment
		{`/mnt/user/Movies/a.mkv`, "/mnt/user/Movies/a.mkv", true},           // host-correct path passes through
		{`\\host`, "", false},                  // no share segment
		{`\\host\..\..\etc\passwd`, "", false}, // traversal rejected (escapes /mnt/)
		{`/data/Movies/a.mkv`, "", false},      // non-/mnt, unmatched -> missing
	}
	for _, c := range cases {
		got, ok := m.ToHost(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ToHost(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestExplicitRuleBeatsFallback(t *testing.T) {
	m := New([]Rule{{From: `\\tower\Movies`, To: "/mnt/disk1/Movies"}}, WithUnraidUNCFallback())
	got, ok := m.ToHost(`\\tower\Movies\a.mkv`)
	if !ok || got != "/mnt/disk1/Movies/a.mkv" {
		t.Errorf("explicit rule should win: got (%q,%v)", got, ok)
	}
}

func TestFallbackDisabledByDefault(t *testing.T) {
	m := New(nil) // no option
	if got, ok := m.ToHost(`\\host\Share\a.mkv`); ok {
		t.Errorf("fallback must be opt-in; got (%q,%v)", got, ok)
	}
}
