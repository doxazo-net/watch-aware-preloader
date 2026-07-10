package pathmap

import (
	"strings"
	"testing"
)

// FuzzToHost checks that ToHost never panics regardless of the server path it is
// handed. ToHost mixes UNC canonicalization, longest-prefix matching, and a
// path.Clean containment guard, all of which run on adversarial media-server
// input; a returned (_, false) is an acceptable "no mapping" outcome, but a
// panic (slice bounds, nil deref) would crash a sweep.
func FuzzToHost(f *testing.F) {
	seeds := []string{
		"",
		"/",
		"//server/share/movie.mkv",
		`\\host\share\movie.mkv`,
		`\\host\Share`,
		`\\host\..\..\etc\passwd`,
		"/mnt/user/Media/movie.mkv",
		"/mnt/user/../etc/shadow",
		"/data/movies/a.mkv",
		"relative/path",
		"\x00\x01\x02",
		"C:\\Users\\media\\a.mkv",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// A representative mapper: an explicit rule plus the UNC fallback, exercising
	// both the prefix-match and containment-guard branches.
	m := New(
		[]Rule{
			{From: "/data/movies", To: "/mnt/user/Media"},
			{From: `\\host\Share`, To: "/mnt/user/Share"},
		},
		WithUnraidUNCFallback(),
	)

	// A second, empty mapper (no rules, no UNC fallback) so the fuzzer also drives
	// ToHost's pure pass-through branch (len(rules)==0 && !uncFallback), which the
	// rules+fallback mapper above can never reach.
	empty := New(nil)

	f.Fuzz(func(t *testing.T, serverPath string) {
		// Config 1 - rules + UNC fallback. Contract: no panic, and any "ok"
		// mapping must carry a non-empty host (an empty host with ok=true would
		// mean we claimed a mapping to nowhere).
		host, ok := m.ToHost(serverPath)
		if ok && host == "" {
			t.Fatalf("rules+fallback ToHost returned ok with an empty host for %q", serverPath)
		}

		// Config 2 - empty mapper, the pass-through branch. Here a non-UNC path
		// (no leading `\\`) maps to itself verbatim, so an empty input legitimately
		// yields ("", true) - the ok+empty-host invariant above does NOT hold and
		// must not be asserted. The invariants that DO hold: it never panics, a
		// non-UNC path passes through unchanged, and a UNC path is left unmapped.
		ehost, eok := empty.ToHost(serverPath)
		if strings.HasPrefix(serverPath, `\\`) {
			if eok {
				t.Fatalf("empty mapper mapped UNC path %q -> (%q, true); want no mapping", serverPath, ehost)
			}
		} else if !eok || ehost != serverPath {
			t.Fatalf("empty mapper failed to pass %q through unchanged: got (%q, %v)", serverPath, ehost, eok)
		}
	})
}
