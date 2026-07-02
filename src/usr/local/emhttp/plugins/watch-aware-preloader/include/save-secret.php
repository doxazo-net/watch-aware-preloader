<?php

declare(strict_types=1);

// Thin POST endpoint: verify CSRF, then write the API key to secrets.toml.
// Output goes to the webGui progress popup; never echo the key.

require_once __DIR__ . '/secrets.php';
require_once __DIR__ . '/paths.php';

header('Content-Type: text/plain');

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

$key = (string) ($_POST['api_key'] ?? '');
$secretPath = wap_default_secret_path();
if (!wap_write_api_key($secretPath, $key)) {
    http_response_code(500);
    echo "Failed to write the API key: the secrets path is not writable.\n";
    exit;
}

echo ($key === '') ? "API key cleared.\n" : "API key saved.\n";
