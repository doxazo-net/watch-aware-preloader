<?php

declare(strict_types=1);

// Shared default filesystem paths for the plugin's PHP surface (settings page +
// secret endpoint). These MUST stay in sync with the Go engine's built-in
// defaults. PHP never parses config.toml (that is the engine's job); it only
// needs to know where the engine writes by default, so these are plain literals.

/** Default path the engine writes its run summary to. */
function wap_default_status_path(): string
{
    return '/var/local/preloadd/status.json';
}

/** Default path the credentials file (secrets.toml) lives on flash. */
function wap_default_secret_path(): string
{
    return '/boot/config/plugins/watch-aware-preloader/secrets.toml';
}

/** Default path the engine writes the pickers cache to. */
function wap_default_pickers_path(): string
{
    return '/var/local/preloadd/pickers.json';
}

/** Default path `rc.preloadd test` writes its last connection-test result to. */
function wap_default_last_test_path(): string
{
    return '/var/local/preloadd/last-test.json';
}
