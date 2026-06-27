// Command preloadd is the watch-aware media preloader daemon.
//
// It warms the Linux page cache with the media each household user is most
// likely to play next, derived from the media server's (Emby/Jellyfin) watch
// state - resume points, next-up episodes, and recently-added items - rather
// than filesystem modification time. See docs/specs for the full design.
//
// This is scaffolding only; the Phase 1 engine is not yet implemented.
package main

import (
	"fmt"
	"os"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Printf("preloadd %s\n", version)
	fmt.Fprintln(os.Stderr, "not yet implemented - see docs/specs for the Phase 1 plan")
}
