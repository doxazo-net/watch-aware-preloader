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
	{ID: "111", Locations: []string{"/share/Movies", "/share/4K_Movies"}},
	{ID: "222", Locations: []string{"/share/Music"}},
}

func TestEmptyEnabledAllowsAll(t *testing.T) {
	s := New(libs, nil, fakeToHost)
	if !s.Allowed(`\\host\Anything\x.mkv`) {
		t.Error("empty enabledIDs must allow all items")
	}
}

func TestScopedToSelectedLibrary(t *testing.T) {
	s := New(libs, []string{"111"}, fakeToHost)
	cases := []struct {
		path string
		want bool
	}{
		{`\\outatime\Movies\Film\a.mkv`, true},      // UNC item in a selected library
		{`\\outatime\4K_Movies\b.mkv`, true},        // second location of the same library
		{`/share/Movies/c.mkv`, true},               // container-form path in scope
		{`\\outatime\Music\d.flac`, false},          // Music (222) not selected
		{`\\outatime\Books\e.epub`, false},          // not in any selected library
		{`/mnt/user/Movies/already-host.mkv`, true}, // already-host path in scope
	}
	for _, c := range cases {
		if got := s.Allowed(c.path); got != c.want {
			t.Errorf("Allowed(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestUnmappablePathExcluded(t *testing.T) {
	s := New(libs, []string{"111"}, fakeToHost)
	if s.Allowed("/some/foreign/path.mkv") {
		t.Error("an item whose path cannot be mapped must be excluded from a scoped sweep")
	}
}

func TestSelectionWithNoMappableLocationFallsBackToAll(t *testing.T) {
	// enabledIDs matches a library, but its Location never maps -> allow all
	// rather than warm nothing.
	unmappable := []Library{{ID: "999", Locations: []string{"/foreign/root"}}}
	s := New(unmappable, []string{"999"}, fakeToHost)
	if !s.Allowed(`\\host\Movies\x.mkv`) {
		t.Error("a selection that yields no usable prefix must fall back to allow-all")
	}
}
