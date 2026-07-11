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

// --- wap_read_estimate: the projection written by rc.preloadd estimate.
$est = tempnam(sys_get_temp_dir(), 'wapest');

// Missing file -> null.
unlink($est);
check(wap_read_estimate($est) === null, 'estimate missing file returns null');

// Valid schema_version 1 -> decoded array with rows.
file_put_contents($est, json_encode([
    'schema_version' => 1,
    'generated_at' => '2026-07-10T21:05:00Z',
    'budget_bytes' => 17179869184,
    'ceiling_per_user_tier' => 200,
    'rows' => [
        ['u' => '3', 't' => 'resume', 'l' => 'L1', 'b' => 41155072, 'r' => 0],
        ['u' => '7', 't' => 'next-up', 'l' => '', 'b' => 38000000, 'r' => 1],
    ],
    'meta' => ['target_seconds' => 20, 'ram_percent' => 50, 'item_count' => 2, 'ceiling_truncated' => false],
]));
$e = wap_read_estimate($est);
check(is_array($e), 'valid estimate returns array');
check(($e['budget_bytes'] ?? null) === 17179869184, 'budget_bytes decoded');
check(is_array($e['rows'] ?? null) && count($e['rows']) === 2, 'rows decoded');
check(($e['rows'][0]['t'] ?? null) === 'resume', 'row tier decoded');

// Wrong schema_version -> null.
file_put_contents($est, json_encode(['schema_version' => 2, 'rows' => []]));
check(wap_read_estimate($est) === null, 'estimate schema_version mismatch returns null');

// Non-JSON -> null.
file_put_contents($est, 'not json');
check(wap_read_estimate($est) === null, 'estimate invalid JSON returns null');

@unlink($est);

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: status helpers\n";
