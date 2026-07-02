// Package pathmap rewrites media-server-reported paths to host filesystem paths.
package pathmap

import (
	"path"
	"sort"
	"strings"
)

// Rule maps a server path prefix (From) to a host path prefix (To).
type Rule struct {
	From string
	To   string
}

// Mapper applies path rules, longest matching prefix first.
type Mapper struct {
	rules       []Rule
	uncFallback bool
}

// Option configures a Mapper.
type Option func(*Mapper)

// WithUnraidUNCFallback rewrites a UNC path (`\\host\Share\rest`) that matches no
// explicit rule to the Unraid share root `/mnt/user/Share/rest`, host-agnostically.
// This encodes the Unraid SMB convention (an exported share `\\host\Share` is the
// same data as `/mnt/user/Share`) and is safe here because this binary only ever
// runs on an Unraid host.
func WithUnraidUNCFallback() Option {
	return func(m *Mapper) { m.uncFallback = true }
}

// New returns a Mapper. Rules are sorted so the longest From prefix is tried
// first, giving deterministic results regardless of input order.
func New(rules []Rule, opts ...Option) *Mapper {
	cp := make([]Rule, len(rules))
	for i, r := range rules {
		cp[i] = Rule{From: normalizePrefix(r.From), To: normalizePrefix(r.To)}
	}
	sort.SliceStable(cp, func(i, j int) bool {
		return len(cp[i].From) > len(cp[j].From)
	})
	m := &Mapper{rules: cp}
	for _, o := range opts {
		o(m)
	}
	return m
}

// normalizePrefix canonicalizes a rule prefix: convert backslashes to forward
// slashes (so Windows/UNC server paths like `\\host\Share` match), then strip a
// redundant trailing slash so the boundary check in ToHost ("/share/" + "/")
// does not double the separator and miss matches. Root ("/") is preserved.
func normalizePrefix(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	if s == "/" {
		return s
	}
	return strings.TrimRight(s, "/")
}

// unraidUNCToHost maps `\\host\Share\rest` -> `/mnt/user/Share/rest`, dropping the
// host component entirely (SMB share names are case-insensitive; the host varies).
// Returns false for non-UNC paths or a UNC path with no share segment.
func unraidUNCToHost(p string) (string, bool) {
	if !strings.HasPrefix(p, `\\`) {
		return "", false
	}
	norm := strings.ReplaceAll(p, `\`, "/") // //host/Share/rest
	rest := strings.TrimPrefix(norm, "//")  // host/Share/rest
	parts := strings.SplitN(rest, "/", 2)   // [host, Share/rest]
	if len(parts) < 2 || parts[1] == "" {
		return "", false
	}
	return "/mnt/user/" + parts[1], true
}

// ToHost rewrites serverPath. With no rules and no fallback, POSIX paths pass
// through unchanged (the server already reports host-correct paths); UNC paths
// return false. With rules but no match, returns false unless the UNC fallback
// is enabled. With the UNC fallback on, an already host-correct `/mnt/` path
// also passes through, and any resulting path is confirmed to stay under
// `/mnt/` - a crafted traversal (e.g. `\\host\..\..\etc`) is rejected.
func (m *Mapper) ToHost(serverPath string) (string, bool) {
	if len(m.rules) == 0 && !m.uncFallback && !strings.HasPrefix(serverPath, `\\`) {
		return serverPath, true
	}
	// Canonicalize only Windows/UNC inputs (leading `\\`) so they resolve via the
	// existing longest-prefix logic. On POSIX a backslash is a legal filename
	// character, so a non-UNC path is matched verbatim and never rewritten.
	canonical := serverPath
	if strings.HasPrefix(serverPath, `\\`) {
		canonical = strings.ReplaceAll(serverPath, `\`, "/")
	}
	for _, r := range m.rules {
		if canonical == r.From || strings.HasPrefix(canonical, r.From+"/") {
			return r.To + strings.TrimPrefix(canonical, r.From), true
		}
	}
	if m.uncFallback {
		// UNC convention: \\host\Share\rest -> /mnt/user/Share/rest.
		if host, ok := unraidUNCToHost(serverPath); ok {
			return containedUnderMnt(host)
		}
		// Already host-correct Unraid path: identity-map anything already rooted
		// under /mnt/ (a native/non-docker media server reporting POSIX paths, or a
		// 1:1 bind) so it still warms instead of counting as missing.
		if strings.HasPrefix(serverPath, "/mnt/") {
			return containedUnderMnt(serverPath)
		}
	}
	return "", false
}

// containedUnderMnt cleans p and confirms it stays within /mnt/, rejecting any
// path-traversal escape (e.g. a crafted `\\host\..\..\etc`). Returns false when the
// cleaned path is not under /mnt/.
func containedUnderMnt(p string) (string, bool) {
	clean := path.Clean(p)
	if clean != "/mnt" && !strings.HasPrefix(clean, "/mnt/") {
		return "", false
	}
	return clean, true
}
