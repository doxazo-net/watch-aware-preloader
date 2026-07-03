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

- in a secrets file (default `/boot/config/plugins/watch-aware-preloader/secrets.toml`)
  under `[server].api_key` - see `secrets.example.toml`; or
- via the `EMBY_API_KEY` environment variable (which overrides the file).

`config.toml` must not contain `api_key`; the engine refuses to start if it does.
The secrets-file location can be overridden with the `secret_path` key in
`config.toml`.

Note on the default location: `/boot` is the Unraid USB flash drive (FAT32),
which does not enforce Unix file permissions - so the secrets file is only as
protected as flash/root access to the server (the same model every Unraid plugin
uses for stored credentials). Point `secret_path` at a Linux filesystem if you
want `0600` file-mode enforcement.

## Installation (Unraid plugin)

Install by URL (Plugins -> Install Plugin). The release workflow attaches the
generated `.plg` to each release, so install from the stable "latest release"
asset URL (it always tracks the newest stable release's package):

```text
https://github.com/doxazo-net/watch-aware-preloader/releases/latest/download/watch-aware-preloader.plg
```

To install a specific pre-release build for testing, use that release's
versioned asset URL instead (the `latest` URL only resolves to stable releases):

```text
https://github.com/doxazo-net/watch-aware-preloader/releases/download/<version>/watch-aware-preloader.plg
```

On install the plugin:
- extracts the `preloadd` binary to `/usr/local/emhttp/plugins/watch-aware-preloader/`
- seeds `/boot/config/plugins/watch-aware-preloader/secrets.toml` (see the
  Credentials note above about flash file permissions) and generates
  `config.toml` from your settings
- installs a cron job running `preloadd -once` on the configured interval

After install, configure everything from the webGui at
**Settings -> Watch-Aware Preloader**: set the server URL, users, RAM budget,
target seconds, path maps, and schedule interval, then paste your API key into
the write-only API-key field (stored in `secrets.toml`, never shown back). Use
**Test connection** to verify, and **Run now** to warm immediately. The status
panel shows the last run (time shown in US Pacific).

`config.toml` is generated from these settings on every save and every boot -
edit settings in the webGui (or the plugin `.cfg`), not `config.toml` directly.
Uninstalling removes the cron job and binary but preserves your settings and
`secrets.toml` on the flash drive.

Releases are tagged with letter-free versions (Slackware requirement), e.g.
`2026.07.01`.

## License

[MIT](LICENSE).
