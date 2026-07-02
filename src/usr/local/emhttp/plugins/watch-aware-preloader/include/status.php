<?php

declare(strict_types=1);

// Read-only helpers for the Watch-Aware Preloader status panel. No secrets, no
// TOML: this reads only the engine's status.json (JSON) and formats times.

const WAP_STATUS_SCHEMA_VERSION = 1;

/**
 * Decode and validate the engine's status.json.
 *
 * @return array<string, mixed>|null the decoded status, or null when the file is
 *         missing, unreadable, not valid JSON, or a different schema version.
 */
function wap_read_status(string $path): ?array
{
    if (!is_file($path)) {
        return null;
    }
    $raw = @file_get_contents($path);
    if ($raw === false) {
        return null;
    }
    $data = json_decode($raw, true);
    if (!\is_array($data)) {
        return null;
    }
    if (($data['schema_version'] ?? null) !== WAP_STATUS_SCHEMA_VERSION) {
        return null;
    }
    return $data;
}

/**
 * Convert an RFC3339 UTC timestamp to a labeled US Pacific string, e.g.
 * "2026-06-30 14:05:00 PDT". Returns the input unchanged if it cannot be parsed.
 */
function wap_format_pacific(string $rfc3339utc): string
{
    try {
        $dt = new DateTimeImmutable($rfc3339utc);
    } catch (Exception $e) {
        return $rfc3339utc;
    }
    $dt = $dt->setTimezone(new DateTimeZone('America/Los_Angeles'));
    return $dt->format('Y-m-d H:i:s T');
}
