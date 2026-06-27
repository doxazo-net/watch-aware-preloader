# watch-aware-preloader

An Unraid plugin that preloads the media you are actually likely to play next into
the Linux page cache, so playback starts instantly instead of waiting 8-10 seconds
for an array disk to spin up.

Unlike the popular "Video Preloader" script (which guesses from filesystem
modification time), this uses your media server's own watch state - resume points,
next-up episodes, recently-added, and what each household user has been watching - to
decide what to warm, and sizes each preload by playback *duration* rather than a fixed
byte count.

- Native Unraid `.plg` plugin: host daemon (`preloadd`, a single static Go binary) +
  PHP settings page.
- Supports Emby and Jellyfin.
- Event-driven (reacts within seconds of play/pause/stop) with a periodic backstop.

## Status

Pre-implementation. The approved design lives in
[`docs/specs/2026-06-26-watch-aware-preloader-design.md`](docs/specs/2026-06-26-watch-aware-preloader-design.md).

Phase 1 (engine MVP, Emby, config via file) is the first deliverable.

## License

[MIT](LICENSE).
