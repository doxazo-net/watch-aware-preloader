# Watch-Aware Preloader

An Unraid plugin that preloads the media you are actually likely to play next into
the Linux page cache, so playback starts instantly instead of waiting 8-10 seconds
for an array disk to spin up.

Unlike the popular [Video Preloader][vp] script by Marc Gutt (which guesses from
filesystem modification time), this uses your media server's own watch state - resume points,
next-up episodes, recently-added, and what each household user has been watching - to
decide what to warm, and sizes each preload by playback *duration* rather than a fixed
byte count.

- Native Unraid `.plg` plugin: a single static Go binary (`preloadd`) +
  PHP settings page.
- Supports Emby and Jellyfin.
- Runs as a **cron-invoked one-shot** (`preloadd -once`) like Fix Common Problems and
  the Mover - each run is a fresh sweep, so library changes are picked up every
  interval. An optional `--daemon` mode adds sub-minute reaction for those who want it.

[vp]: https://forums.unraid.net/topic/97982-video-preloader-avoids-hdd-spinup-latency-when-starting-a-movie-or-episode-through-plex-jellyfin-or-emby/

## Status

Pre-implementation. The approved design lives in
[`docs/specs/2026-06-26-watch-aware-preloader-design.md`](docs/specs/2026-06-26-watch-aware-preloader-design.md).

Phase 1 (engine MVP, Emby, config via file) is the first deliverable.

## Configuration

### Credentials

The Emby API key is a secret and is kept out of `config.toml`. Provide it either:

- in a secrets file (default `/boot/config/plugins/watch-aware-preloader/secrets.toml`,
  mode `0600`) under `[server].api_key` - see `secrets.example.toml`; or
- via the `EMBY_API_KEY` environment variable (which overrides the file).

`config.toml` must not contain `api_key`; the engine refuses to start if it does.
The secrets-file location can be overridden with the `secret_path` key in
`config.toml`.

## License

[MIT](LICENSE).
