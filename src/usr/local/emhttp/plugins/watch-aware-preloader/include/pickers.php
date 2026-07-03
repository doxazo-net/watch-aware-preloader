<?php

declare(strict_types=1);

// Read-only accessors for pickers.json, the root-written cache of server-queried
// users / libraries / auto-detected path maps. The settings page renders as the
// unprivileged webGui user and cannot query the server (it cannot read the
// 0600-root API key), so rc.preloadd (root) writes this cache on a successful
// connection test and the page reads it here. No secrets are ever in this file.

require_once __DIR__ . '/paths.php';

/**
 * Decode pickers.json, or null when absent or invalid.
 *
 * @return array<string, mixed>|null
 */
function wap_read_pickers(string $path): ?array
{
    if (!is_file($path) || !is_readable($path)) {
        return null;
    }
    $raw = file_get_contents($path);
    if ($raw === false) {
        return null;
    }
    /** @var array<string, mixed>|null $data */
    $data = json_decode($raw, true);

    return \is_array($data) ? $data : null;
}

/**
 * A cache is fresh when present and its server_url matches (trailing slash ignored).
 *
 * @param array<string, mixed>|null $pickers
 */
function wap_pickers_fresh(?array $pickers, string $serverUrl): bool
{
    if ($pickers === null) {
        return false;
    }
    $cached = rtrim((string) ($pickers['server_url'] ?? ''), '/');

    return $cached !== '' && $cached === rtrim($serverUrl, '/');
}

/**
 * @param array<string, mixed>|null $p
 * @return list<array{id:string,name:string}>
 */
function wap_picker_users(?array $p): array
{
    return ($p !== null && \is_array($p['users'] ?? null)) ? $p['users'] : [];
}

/**
 * @param array<string, mixed>|null $p
 * @return list<array{id:string,name:string}>
 */
function wap_picker_libraries(?array $p): array
{
    return ($p !== null && \is_array($p['libraries'] ?? null)) ? $p['libraries'] : [];
}

/**
 * @param array<string, mixed>|null $p
 * @return array{rules?:list<array{from:string,to:string,source:string}>,unraid_unc_fallback?:bool}
 */
function wap_picker_pathmaps(?array $p): array
{
    return ($p !== null && \is_array($p['pathmaps'] ?? null)) ? $p['pathmaps'] : [];
}
