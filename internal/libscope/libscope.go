// Package libscope decides whether a media item belongs to one of the
// operator-selected libraries, so a sweep can be scoped to specific libraries.
//
// The item path the server reports (e.g. a UNC `\\host\Share\...`) and a
// library's configured Location (e.g. a container path `/share/Share`) are often
// in different forms. Rather than reconcile them directly, libscope maps both
// through the same path mapper (the one the preloader already uses) so they land
// in a common host-path namespace (`/mnt/user/Share/...`), then does a prefix
// check. This reuses the mapper's UNC/docker/share normalization instead of
// re-deriving it.
package libscope

import "strings"

// ToHost normalizes a server path to a host path, reporting whether it mapped.
// It matches pathmap.Mapper.ToHost, so a caller passes that method directly.
type ToHost func(serverPath string) (string, bool)

// Library is the minimal library shape libscope needs: a stable ID and the
// source Locations the server reports for it.
type Library struct {
	ID        string
	Locations []string
}

// Scope reports whether an item falls under a selected library. The zero value
// is not usable; construct with New.
type Scope struct {
	allowAll     bool
	toHost       ToHost
	hostPrefixes []string // selected libraries' Locations, mapped to host paths
}

// New builds a Scope. enabledIDs selects which libraries are in scope; an empty
// enabledIDs (or one that matches no library with a mappable Location) means
// "all libraries" - Allowed then always returns true, preserving the unscoped
// default. Locations that do not map through toHost are skipped.
func New(libraries []Library, enabledIDs []string, toHost ToHost) *Scope {
	if len(enabledIDs) == 0 {
		return &Scope{allowAll: true}
	}
	want := make(map[string]bool, len(enabledIDs))
	for _, id := range enabledIDs {
		want[id] = true
	}
	var prefixes []string
	for _, lib := range libraries {
		if !want[lib.ID] {
			continue
		}
		for _, loc := range lib.Locations {
			if host, ok := toHost(loc); ok {
				prefixes = append(prefixes, strings.TrimRight(host, "/"))
			}
		}
	}
	// No selected library yielded a usable prefix (e.g. IDs matched nothing, or
	// no Location mapped): fall back to allow-all rather than silently warming
	// nothing.
	if len(prefixes) == 0 {
		return &Scope{allowAll: true}
	}
	return &Scope{toHost: toHost, hostPrefixes: prefixes}
}

// Allowed reports whether itemServerPath falls under a selected library. An item
// whose path cannot be mapped to a host path is excluded (it cannot be confirmed
// in scope).
func (s *Scope) Allowed(itemServerPath string) bool {
	if s.allowAll {
		return true
	}
	host, ok := s.toHost(itemServerPath)
	if !ok {
		return false
	}
	host = strings.TrimRight(host, "/")
	for _, p := range s.hostPrefixes {
		if host == p || strings.HasPrefix(host, p+"/") {
			return true
		}
	}
	return false
}
