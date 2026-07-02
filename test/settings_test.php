<?php

declare(strict_types=1);

require __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/settings.php';

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

/** Parse a KEY="value" .cfg body into an assoc array the way a consumer would. */
function cfg_kv(string $body): array
{
    $out = [];
    foreach (explode("\n", $body) as $line) {
        if (preg_match('/^([A-Z_]+)="(.*)"$/', $line, $m) === 1) {
            $out[$m[1]] = $m[2];
        }
    }

    return $out;
}

// --- Defaults: an empty POST yields the documented engine defaults. ---
$kv = cfg_kv(wap_render_cfg([]));
check(($kv['SERVER_TYPE'] ?? null) === 'emby', 'default server type emby');
check(($kv['SERVER_URL'] ?? null) === 'http://localhost:8096', 'default server url');
check(($kv['USERS'] ?? null) === '', 'default users empty');
check(($kv['RAM_PERCENT'] ?? null) === '50', 'default ram 50');
check(($kv['TARGET_SECONDS'] ?? null) === '20', 'default target 20');
check(($kv['PATH_MAPS'] ?? null) === '', 'default path maps empty');
check(($kv['CRON_INTERVAL'] ?? null) === '15', 'default cron 15');

// --- Normal values pass through unchanged. ---
$kv = cfg_kv(wap_render_cfg([
    'SERVER_TYPE'    => 'emby',
    'SERVER_URL'     => 'http://tower:8096',
    'USERS'          => 'alice, bob',
    'RAM_PERCENT'    => '65',
    'TARGET_SECONDS' => '30',
    'PATH_MAPS'      => '/share=>/mnt/user; /media=>/mnt/user/media',
    'CRON_INTERVAL'  => '10',
]));
check($kv['SERVER_URL'] === 'http://tower:8096', 'server url round-trips');
check($kv['USERS'] === 'alice, bob', 'users round-trip');
check($kv['PATH_MAPS'] === '/share=>/mnt/user; /media=>/mnt/user/media', 'path maps round-trip');
check($kv['RAM_PERCENT'] === '65', 'ram round-trips');
check($kv['CRON_INTERVAL'] === '10', 'cron round-trips');

// --- SERVER_TYPE is constrained to the only shipping adapter. ---
$kv = cfg_kv(wap_render_cfg(['SERVER_TYPE' => 'jellyfin']));
check($kv['SERVER_TYPE'] === 'emby', 'spoofed server type forced to emby');

// --- Numeric clamping. ---
check(cfg_kv(wap_render_cfg(['RAM_PERCENT' => '0']))['RAM_PERCENT'] === '1', 'ram 0 -> 1');
check(cfg_kv(wap_render_cfg(['RAM_PERCENT' => '999']))['RAM_PERCENT'] === '100', 'ram 999 -> 100');
check(cfg_kv(wap_render_cfg(['RAM_PERCENT' => 'abc']))['RAM_PERCENT'] === '50', 'ram non-numeric -> default');
check(cfg_kv(wap_render_cfg(['CRON_INTERVAL' => '0']))['CRON_INTERVAL'] === '1', 'cron 0 -> 1');
check(cfg_kv(wap_render_cfg(['CRON_INTERVAL' => '99']))['CRON_INTERVAL'] === '59', 'cron 99 -> 59');
check(cfg_kv(wap_render_cfg(['TARGET_SECONDS' => '-5']))['TARGET_SECONDS'] === '1', 'target -5 -> 1');

// --- Injection hardening: a value cannot break the KEY="value" line or add a
// second key. Quote/backslash/control chars are removed. ---
$evil = wap_render_cfg([
    'SERVER_URL' => "http://x\"\nINJECTED=\"pwned",
    'USERS'      => "a\\b\tc",
]);
$kv = cfg_kv($evil);
check(!isset($kv['INJECTED']), 'newline cannot inject a second key');
check(!str_contains($kv['SERVER_URL'] ?? '', '"'), 'double-quote stripped from value');
check(!str_contains($kv['USERS'] ?? '', "\\"), 'backslash stripped from value');
check(!str_contains($kv['USERS'] ?? '', "\t"), 'tab stripped from value');
// Body has exactly the 2 comment lines + 7 KEY lines = 9 lines + trailing \n.
check(substr_count($evil, "\n") === 9, 'no extra physical lines from injection');

// --- Direct helper checks. ---
check(wap_cfg_sanitize_str("  trim me  ") === 'trim me', 'sanitize trims');
check(wap_cfg_clamp_int('50', 1, 100, 10) === 50, 'clamp passes in-range');
check(wap_cfg_clamp_int('', 1, 100, 10) === 10, 'clamp empty -> default');
check(wap_cfg_clamp_int('7.9', 1, 100, 10) === 7, 'clamp truncates float');

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: settings serializer\n";
