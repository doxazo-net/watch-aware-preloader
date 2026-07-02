<?php

declare(strict_types=1);

// Write-only credential helpers. The API key is written to secrets.toml and is
// NEVER read back into the UI - the page shows only "set" / "not set". On the
// FAT32 flash path chmod is a no-op (mount umask governs); the key's protection
// is the flash/root boundary, the Unraid norm.

/**
 * Write [server].api_key to $secretPath, overwriting the file. Creates the
 * parent directory and best-effort tightens the file to 0600.
 */
function wap_write_api_key(string $secretPath, string $key): void
{
    $dir = \dirname($secretPath);
    if (!is_dir($dir)) {
        @mkdir($dir, 0700, true);
    }
    // Escape for a TOML basic (double-quoted) string. Backslash first, then the
    // quote and the common control chars; any remaining control char (illegal
    // raw in a TOML basic string) becomes \uXXXX. Surrounding whitespace is
    // stripped - a pasted key never legitimately has it, and a trailing newline
    // would otherwise break secrets.toml.
    $key = trim($key);
    $escaped = str_replace(
        ['\\', '"', "\n", "\r", "\t"],
        ['\\\\', '\\"', '\\n', '\\r', '\\t'],
        $key
    );
    $escaped = preg_replace_callback(
        '/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/',
        static fn (array $m): string => \sprintf('\\u%04X', \ord($m[0])),
        $escaped
    );
    $body = "# Credentials ONLY. Never commit; never put these in config.toml.\n"
          . "[server]\n"
          . "api_key = \"{$escaped}\"\n";
    file_put_contents($secretPath, $body, LOCK_EX);
    @chmod($secretPath, 0600);
}

/** True iff secrets.toml has a non-empty [server].api_key. */
function wap_api_key_is_set(string $secretPath): bool
{
    if (!is_file($secretPath)) {
        return false;
    }
    $raw = @file_get_contents($secretPath);
    if ($raw === false) {
        return false;
    }
    if (preg_match('/^\s*api_key\s*=\s*"(.*)"\s*$/m', $raw, $m) === 1) {
        return trim($m[1]) !== '';
    }
    return false;
}
