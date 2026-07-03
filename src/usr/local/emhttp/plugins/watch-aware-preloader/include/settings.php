<?php

declare(strict_types=1);

// Settings sanitization helpers, applied by the root #include hook (presave.php)
// BEFORE /update.php writes the flash .cfg. Unraid serves direct plugin PHP
// endpoints as the unprivileged "nobody" user, which cannot write the 0600-root
// FAT flash dir; only /update.php (which runs as root) can. So the settings save
// goes through /update.php, and this code normalizes the posted values in place
// so /update.php writes clean, bounded KEY="value" pairs. No I/O here - pure
// transforms, unit-tested by test/settings_test.php.

/**
 * Sanitize a free-text .cfg scalar. /update.php writes values as KEY="$value"
 * with NO escaping, and rc.preloadd's cfg_get strips one surrounding quote pair
 * without unescaping, so the robust policy is to REMOVE characters that could
 * break the KEY="value" line rather than escape them. Control chars (newline/CR/
 * tab/etc.) are the real line-injection vector; double-quote and backslash never
 * appear legitimately in a server URL, comma-separated user list, or path-map,
 * so dropping them is lossless for real input and closes the injection surface.
 */
function wap_cfg_sanitize_str(string $v): string
{
    $v = preg_replace('/[\x00-\x1F\x7F"\\\\]/', '', $v);

    return trim((string) $v);
}

/**
 * Normalize a posted list-or-scalar field into a sanitized comma-separated cfg
 * value. Checkbox pickers post an array (USERS[]/LIBRARIES[]); a legacy free-text
 * field posts a scalar. Each element is run through wap_cfg_sanitize_str; empty
 * elements are dropped.
 *
 * @param mixed $v array<int,scalar>|scalar|null
 */
function wap_cfg_csv_from_list(mixed $v): string
{
    if (\is_array($v)) {
        $parts = [];
        foreach ($v as $item) {
            if (!\is_scalar($item)) {
                continue;
            }
            $s = wap_cfg_sanitize_str((string) $item);
            if ($s !== '') {
                $parts[] = $s;
            }
        }

        return implode(',', $parts);
    }

    return wap_cfg_sanitize_str((string) ($v ?? ''));
}

/**
 * A checkbox posts a value only when checked, so presence (any non-empty scalar)
 * means enabled; absence means disabled. Returns "1" or "0".
 *
 * @param mixed $v
 */
function wap_cfg_checkbox(mixed $v): string
{
    return (\is_scalar($v) && (string) $v !== '' && (string) $v !== '0') ? '1' : '0';
}

/**
 * Coerce a posted numeric field to an int within [$min, $max], falling back to
 * $default when the value is not a plain decimal or is out of range. Only decimal
 * digits (optionally signed, optionally with a fractional part that is then
 * truncated) are accepted; is_numeric() would also pass scientific notation like
 * "1e2" - which a number input can submit - and (int) "1e2" is 1, silently
 * mis-clamping. Reject those to $default.
 */
function wap_cfg_clamp_int(mixed $v, int $min, int $max, int $default): int
{
    if (!\is_scalar($v) || preg_match('/^\s*-?\d+(?:\.\d+)?\s*$/', (string) $v) !== 1) {
        return $default;
    }
    $n = (int) $v;
    if ($n < $min) {
        return $min;
    }
    if ($n > $max) {
        return $max;
    }

    return $n;
}

/**
 * Normalize the settings fields in $post IN PLACE so /update.php writes a clean,
 * bounded .cfg. Only the fields the form posts are touched; every other engine
 * default is left to rc.preloadd's cfg_get fallbacks. Numeric fields are clamped
 * to their valid ranges; string fields are sanitized; SERVER_TYPE is constrained
 * to the only adapter shipping in Phase 2 so a spoofed value cannot select an
 * unsupported server.
 *
 * @param array<string, mixed> $post the request map (typically $_POST), mutated in place
 */
function wap_sanitize_settings_post(array &$post): void
{
    // Only the Emby adapter ships in Phase 2, so pin it regardless of input.
    $post['SERVER_TYPE'] = 'emby';

    $url = wap_cfg_sanitize_str((string) ($post['SERVER_URL'] ?? ''));
    $post['SERVER_URL'] = ($url === '') ? 'http://localhost:8096' : $url;

    $post['USERS']     = wap_cfg_csv_from_list($post['USERS'] ?? '');
    $post['LIBRARIES'] = wap_cfg_csv_from_list($post['LIBRARIES'] ?? '');
    $post['PATH_MAPS'] = wap_cfg_sanitize_str((string) ($post['PATH_MAPS'] ?? ''));

    $post['RAM_PERCENT']    = (string) wap_cfg_clamp_int($post['RAM_PERCENT'] ?? null, 1, 100, 50);
    $post['TARGET_SECONDS'] = (string) wap_cfg_clamp_int($post['TARGET_SECONDS'] ?? null, 1, 86400, 20);
    $post['CRON_INTERVAL']  = (string) wap_cfg_clamp_int($post['CRON_INTERVAL'] ?? null, 1, 59, 15);

    foreach (['RESUME', 'NEXTUP', 'RECENT'] as $t) {
        $post["TIER_{$t}_ENABLED"] = wap_cfg_checkbox($post["TIER_{$t}_ENABLED"] ?? null);
        $post["TIER_{$t}_MAX"]     = (string) wap_cfg_clamp_int($post["TIER_{$t}_MAX"] ?? null, 0, 10000, 0);
    }
}
