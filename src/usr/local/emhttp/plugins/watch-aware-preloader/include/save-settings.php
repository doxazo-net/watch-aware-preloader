<?php

declare(strict_types=1);

// Dedicated settings-save endpoint. Verify CSRF, write the flash .cfg, apply the
// API key (blank = keep, "clear" checkbox = clear), then run rc.preloadd render
// in ONE deterministic request. This replaces the old "Apply" form that combined
// #file + #command in a single /update.php POST - Unraid processes those as
// separate branches, so the combined form wrote neither the .cfg nor ran render
// (issue #30). Output goes to the webGui progress popup as plain text.

require_once __DIR__ . '/secrets.php';
require_once __DIR__ . '/settings.php';
require_once __DIR__ . '/paths.php';

header('Content-Type: text/plain');
// Buffer the body so http_response_code() stays effective after the first echo:
// this endpoint prints per-stage progress, and without buffering the first echo
// would flush headers and make a later 500 a no-op (with a "headers already
// sent" warning leaking into the progress popup).
ob_start();

// CSRF: re-parse var.ini and compare with hash_equals, identical to
// save-secret.php. This is independent of the page's $var, so it holds even if
// the .page include scope did not populate $var['csrf_token'].
$expected = '';
$varIni = '/var/local/emhttp/var.ini';
if (is_file($varIni)) {
    $var = @parse_ini_file($varIni);
    if (\is_array($var)) {
        $expected = (string) ($var['csrf_token'] ?? '');
    }
}
$provided = (string) ($_POST['csrf_token'] ?? '');
if ($expected === '' || !hash_equals($expected, $provided)) {
    http_response_code(403);
    echo "Refused: CSRF token mismatch.\n";
    exit;
}

// 1. Persist the settings .cfg.
$cfgPath = wap_default_cfg_path();
$cfgDir  = \dirname($cfgPath);
if (!is_dir($cfgDir)) {
    @mkdir($cfgDir, 0700, true);
}
if (@file_put_contents($cfgPath, wap_render_cfg($_POST), LOCK_EX) === false) {
    http_response_code(500);
    echo "Failed to write settings: the flash config path is not writable.\n";
    exit;
}
echo "Settings saved.\n";

// 2. Apply the API key. Blank keeps the existing key (so repeated settings saves
// never wipe the credential); the explicit "clear" checkbox removes it.
$secretPath = wap_default_secret_path();
$clear = ($_POST['clear_api_key'] ?? '') !== '';
$key   = (string) ($_POST['api_key'] ?? '');
if ($clear) {
    if (!wap_write_api_key($secretPath, '')) {
        http_response_code(500);
        echo "Failed to clear the API key: the secrets path is not writable.\n";
        exit;
    }
    echo "API key cleared.\n";
} elseif ($key !== '') {
    if (!wap_write_api_key($secretPath, $key)) {
        http_response_code(500);
        echo "Failed to write the API key: the secrets path is not writable.\n";
        exit;
    }
    echo "API key saved.\n";
} else {
    echo "API key unchanged.\n";
}

// 3. Re-render config.toml + the cron fragment from the saved .cfg.
$rc = wap_rc_script_path();
if (!is_file($rc)) {
    http_response_code(500);
    echo "Cannot render: rc.preloadd not found.\n";
    exit;
}
$out  = [];
$code = 0;
exec(escapeshellarg($rc) . ' render 2>&1', $out, $code);
echo "\nRender:\n" . implode("\n", $out) . "\n";
if ($code !== 0) {
    http_response_code(500);
    echo "Render failed (exit {$code}). Settings and key were saved; fix the values and re-apply.\n";
}
