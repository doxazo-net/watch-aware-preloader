<?php

declare(strict_types=1);

// /update.php #include hook. update.php runs as root and `include`s this file (in
// its own scope, with $_POST available) BEFORE it writes the flash .cfg, so this
// is the one place a settings save can write root-owned flash files. It does two
// things:
//   1. Writes the API key to secrets.toml (root-only file; a direct plugin
//      endpoint runs as "nobody" and cannot). Posted as #api_key so /update.php
//      excludes it from the .cfg (keys prefixed with # are not written).
//      - blank + no clear  -> keep the existing key (repeated saves never wipe it)
//      - #clear_api_key=1   -> remove the key
//      - non-blank          -> store the new key
//   2. Normalizes the settings fields in $_POST in place so /update.php writes a
//      clean, bounded .cfg (clamps numerics, sanitizes strings).
// It must NOT echo: update.php streams the progress popup, and stray output here
// would corrupt it. Failures are logged to syslog.

require_once __DIR__ . '/secrets.php';
require_once __DIR__ . '/settings.php';
require_once __DIR__ . '/paths.php';

$secretPath = wap_default_secret_path();
$clear = (($_POST['#clear_api_key'] ?? '') === '1');
$key   = (string) ($_POST['#api_key'] ?? '');

if ($clear) {
    if (!wap_write_api_key($secretPath, '')) {
        syslog(LOG_ERR, 'watch-aware-preloader: failed to clear API key (secrets path not writable)');
    }
} elseif ($key !== '') {
    if (!wap_write_api_key($secretPath, $key)) {
        syslog(LOG_ERR, 'watch-aware-preloader: failed to write API key (secrets path not writable)');
    }
}
// blank + not clearing => leave the existing key untouched.

// Normalize the settings fields that /update.php is about to write to the .cfg.
wap_sanitize_settings_post($_POST);
