package pathmap

import "testing"

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

	f.Fuzz(func(t *testing.T, serverPath string) {
		// Contract under test: no panic. The (host, ok) result is not asserted -
		// any deterministic classification is valid.
		host, ok := m.ToHost(serverPath)
		if ok && host == "" {
			t.Fatalf("ToHost returned ok with an empty host for %q", serverPath)
		}
	})
}
