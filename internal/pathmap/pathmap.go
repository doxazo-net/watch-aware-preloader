// Package pathmap rewrites media-server-reported paths to host filesystem paths.
package pathmap

import (
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
	rules []Rule
}

// New returns a Mapper. Rules are sorted so the longest From prefix is tried
// first, giving deterministic results regardless of input order.
func New(rules []Rule) *Mapper {
	cp := make([]Rule, len(rules))
	for i, r := range rules {
		cp[i] = Rule{From: normalizePrefix(r.From), To: normalizePrefix(r.To)}
	}
	sort.SliceStable(cp, func(i, j int) bool {
		return len(cp[i].From) > len(cp[j].From)
	})
	return &Mapper{rules: cp}
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

// ToHost rewrites serverPath. With no rules, the path passes through unchanged
// (the server already reports host-correct paths). Returns false when rules
// exist but none match.
func (m *Mapper) ToHost(serverPath string) (string, bool) {
	if len(m.rules) == 0 {
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
	return "", false
}
