<?php

declare(strict_types=1);

require __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/status.php';

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

$tmp = tempnam(sys_get_temp_dir(), 'wapst');

// Missing file -> null.
unlink($tmp);
check(wap_read_status($tmp) === null, 'missing file returns null');

// Valid schema_version 1 -> decoded array.
file_put_contents($tmp, json_encode([
    'schema_version' => 1,
    'last_run' => '2026-06-30T21:05:00Z',
    'mode' => 'once',
    'ok' => true,
    'preloaded' => 3,
    'by_tier' => ['resume' => 1, 'next_up' => 2],
    'by_user' => ['3' => 3],
]));
$s = wap_read_status($tmp);
check(is_array($s), 'valid file returns array');
check(($s['preloaded'] ?? null) === 3, 'preloaded decoded');
check(($s['by_tier']['next_up'] ?? null) === 2, 'by_tier decoded');

// Wrong schema_version -> null.
file_put_contents($tmp, json_encode(['schema_version' => 2, 'last_run' => 'x']));
check(wap_read_status($tmp) === null, 'schema_version mismatch returns null');

// Non-JSON -> null.
file_put_contents($tmp, 'not json');
check(wap_read_status($tmp) === null, 'invalid JSON returns null');

// Pacific formatting: 21:05 UTC on 2026-06-30 is 14:05 PDT.
$formatted = wap_format_pacific('2026-06-30T21:05:00Z');
check(str_contains($formatted, '14:05:00'), "pacific hour correct, got {$formatted}");
check(str_contains($formatted, 'PDT'), "pacific label present, got {$formatted}");

// Unparseable time -> returned unchanged.
check(wap_format_pacific('garbage') === 'garbage', 'unparseable time passthrough');

// --- wap_read_last_test: the connection-test result written by rc.preloadd test.
$lt = tempnam(sys_get_temp_dir(), 'waplt');

// Missing file -> null.
unlink($lt);
check(wap_read_last_test($lt) === null, 'last-test missing file returns null');

// Valid schema_version 1 -> decoded array.
file_put_contents($lt, json_encode([
    'schema_version' => 1,
    'tested_at' => '2026-06-30T21:05:00Z',
    'ok' => true,
    'message' => 'OK: server reachable and API key accepted.',
]));
$t = wap_read_last_test($lt);
check(is_array($t), 'valid last-test returns array');
check(($t['ok'] ?? null) === true, 'last-test ok decoded');
check(($t['message'] ?? null) === 'OK: server reachable and API key accepted.', 'last-test message decoded');

// Wrong schema_version -> null.
file_put_contents($lt, json_encode(['schema_version' => 2, 'ok' => true]));
check(wap_read_last_test($lt) === null, 'last-test schema_version mismatch returns null');

// Non-JSON -> null.
file_put_contents($lt, 'not json');
check(wap_read_last_test($lt) === null, 'last-test invalid JSON returns null');

@unlink($lt);
@unlink($tmp);

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: status helpers\n";
