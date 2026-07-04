package libscope

import (
	"strings"
	"testing"
)

// fakeToHost mimics the real mapper for the live topology: UNC item paths and
// /share container Locations both normalize to /mnt/user/<Share>/...
func fakeToHost(p string) (string, bool) {
	switch {
	case strings.HasPrefix(p, `\\`):
		norm := strings.ReplaceAll(p, `\`, "/") // //host/Share/rest
		rest := strings.TrimPrefix(norm, "//")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			return "", false
		}
		return "/mnt/user/" + parts[1], true
	case strings.HasPrefix(p, "/share/"):
		return "/mnt/user/" + strings.TrimPrefix(p, "/share/"), true
	case strings.HasPrefix(p, "/mnt/"):
		return p, true
	}
	return "", false
}

var libs = []Library{
	{ID: "111", Locations: []string{"/share/Movies", "/share/Videos"}},
	{ID: "222", Locations: []string{"/share/Music"}},
}

func TestEmptyEnabledAllowsAll(t *testing.T) {
	s, fellBack := New(libs, nil, fakeToHost)
	if !s.Allowed(`\\host\Anything\x.mkv`) {
		t.Error("empty enabledIDs must allow all items")
	}
	if fellBack {
		t.Error("no selection requested is not a fallback")
	}
}

func TestScopedToSelectedLibrary(t *testing.T) {
	s, fellBack := New(libs, []string{"111"}, fakeToHost)
	if fellBack {
		t.Error("a resolvable selection must not report a fallback")
	}
	cases := []struct {
		path string
		want bool
	}{
		{`\\tower\Movies\Film\a.mkv`, true},         // UNC item in a selected library
		{`\\tower\Videos\b.mkv`, true},              // second location of the same library
		{`/share/Movies/c.mkv`, true},               // container-form path in scope
		{`\\tower\Music\d.flac`, false},             // Music (222) not selected
		{`\\tower\Books\e.epub`, false},             // not in any selected library
		{`/mnt/user/Movies/already-host.mkv`, true}, // already-host path in scope
	}
	for _, c := range cases {
		if got := s.Allowed(c.path); got != c.want {
			t.Errorf("Allowed(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestUnmappablePathExcluded(t *testing.T) {
	s, _ := New(libs, []string{"111"}, fakeToHost)
	if s.Allowed("/some/foreign/path.mkv") {
		t.Error("an item whose path cannot be mapped must be excluded from a scoped sweep")
	}
}

func TestNilToHostAllowsAll(t *testing.T) {
	// A caller that forgets to thread the mapper must not panic; scoping simply
	// cannot be applied, so allow all - and report the fallback.
	s, fellBack := New(libs, []string{"111"}, nil)
	if !s.Allowed(`\\host\Movies\x.mkv`) {
		t.Error("a nil toHost must fall back to allow-all, not panic")
	}
	if !fellBack {
		t.Error("a nil toHost with a non-empty selection must report a fallback")
	}
}

func TestSelectionWithNoMappableLocationFallsBackToAll(t *testing.T) {
	// enabledIDs matches a library, but its Location never maps -> allow all
	// rather than warm nothing, and report the fallback so the caller can warn.
	unmappable := []Library{{ID: "999", Locations: []string{"/foreign/root"}}}
	s, fellBack := New(unmappable, []string{"999"}, fakeToHost)
	if !s.Allowed(`\\host\Movies\x.mkv`) {
		t.Error("a selection that yields no usable prefix must fall back to allow-all")
	}
	if !fellBack {
		t.Error("a selection that yields no usable prefix must report a fallback")
	}
}
