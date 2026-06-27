# watch-aware-preloader

An Unraid plugin that preloads the media you are actually likely to play next into
the Linux page cache, so playback starts instantly instead of waiting 8-10 seconds
for an array disk to spin up.

Unlike the popular "Video Preloader" script (which guesses from filesystem
modification time), this uses your media server's own watch state - resume points,
next-up episodes, recently-added, and what each household user has been watching - to
decide what to warm, and sizes each preload by playback *duration* rather than a fixed
byte count.

- Native Unraid `.plg` plugin: a single static Go binary (`preloadd`) +
  PHP settings page.
- Supports Emby and Jellyfin.
- Runs as a **cron-invoked one-shot** (`preloadd -once`) like Fix Common Problems and
  the Mover - each run is a fresh sweep, so library changes are picked up every
  interval. An optional `--daemon` mode adds sub-minute reaction for those who want it.

## Status

Pre-implementation. The approved design lives in
[`docs/specs/2026-06-26-watch-aware-preloader-design.md`](docs/specs/2026-06-26-watch-aware-preloader-design.md).

Phase 1 (engine MVP, Emby, config via file) is the first deliverable.

## License

[MIT](LICENSE).
