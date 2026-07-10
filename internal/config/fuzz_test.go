package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzConfigLoad checks that Load never panics on arbitrary file contents. Load
// runs a third-party TOML decoder, then applyDefaults and Validate over the
// decoded struct; a malformed or hostile config file must surface as an error,
// never a panic. A returned error is the expected outcome for invalid input.
func FuzzConfigLoad(f *testing.F) {
	seeds := []string{
		"",
		"not toml at all",
		"[server]\ntype = \"emby\"\nurl = \"http://localhost:8096\"\n",
		"[server]\ntype = \"emby\"\napi_key = \"leaked\"\n",
		"[preload]\nram_percent = 999\n",
		"[preload]\nmin_head_mb = 10\nmax_head_mb = 1\n",
		"[residency]\nprobe_timeout = \"1s\"\n",
		"[[path_map]]\nfrom = \"\\\\\\\\host\\\\Share\"\nto = \"/mnt/user/Share\"\n",
		"=\n",
		"\x00\x01\x02",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Write the fuzz bytes to a real temp file so we exercise Load's actual
		// DecodeFile path (t.TempDir is auto-cleaned per fuzz iteration).
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("writing temp config: %v", err)
		}
		// Contract under test: no panic. An error (invalid TOML, failed
		// validation, api_key rejection) is a valid outcome and ignored.
		_, _ = Load(path)
	})
}
