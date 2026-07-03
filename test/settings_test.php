<?php

declare(strict_types=1);

$wapImpl = __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/settings.php';
if (!is_file($wapImpl)) {
    fwrite(STDERR, "FAIL: implementation not found: {$wapImpl}\n");
    exit(1);
}
require_once $wapImpl;

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

// --- wap_cfg_sanitize_str ---
check(wap_cfg_sanitize_str('  trim me  ') === 'trim me', 'sanitize trims');
check(wap_cfg_sanitize_str("a\"b") === 'ab', 'double-quote stripped');
check(wap_cfg_sanitize_str('a\\b') === 'ab', 'backslash stripped');
check(wap_cfg_sanitize_str("a\nb\tc") === 'abc', 'control chars stripped');
check(wap_cfg_sanitize_str('/share=>/mnt/user; /m=>/mnt/m') === '/share=>/mnt/user; /m=>/mnt/m', 'path map preserved');

// --- wap_cfg_clamp_int ---
check(wap_cfg_clamp_int('50', 1, 100, 10) === 50, 'clamp passes in-range');
check(wap_cfg_clamp_int('0', 1, 100, 10) === 1, 'clamp below min -> min');
check(wap_cfg_clamp_int('999', 1, 100, 10) === 100, 'clamp above max -> max');
check(wap_cfg_clamp_int('', 1, 100, 10) === 10, 'clamp empty -> default');
check(wap_cfg_clamp_int('abc', 1, 100, 10) === 10, 'clamp non-numeric -> default');
check(wap_cfg_clamp_int('7.9', 1, 100, 10) === 7, 'clamp truncates float');
check(wap_cfg_clamp_int('1e2', 1, 100, 10) === 10, 'clamp rejects scientific notation');
check(wap_cfg_clamp_int('0x10', 1, 100, 10) === 10, 'clamp rejects hex');
check(wap_cfg_clamp_int('  25  ', 1, 100, 10) === 25, 'clamp tolerates surrounding whitespace');

// --- wap_sanitize_settings_post: normalizes $_POST in place for /update.php ---
$post = [
    'SERVER_TYPE'    => 'jellyfin',                     // spoofed -> pinned to emby
    'SERVER_URL'     => "http://tower:8096\n",          // trailing newline stripped
    'USERS'          => 'alice, bob',
    'RAM_PERCENT'    => '999',                          // clamped to 100
    'TARGET_SECONDS' => 'abc',                          // -> default 20
    'PATH_MAPS'      => '/library=>/mnt/user/media',
    'CRON_INTERVAL'  => '0',                            // clamped to 1
];
wap_sanitize_settings_post($post);
check($post['SERVER_TYPE'] === 'emby', 'server type pinned to emby');
check($post['SERVER_URL'] === 'http://tower:8096', 'server url sanitized');
check($post['USERS'] === 'alice, bob', 'users preserved');
check($post['RAM_PERCENT'] === '100', 'ram clamped to max (string)');
check($post['TARGET_SECONDS'] === '20', 'target non-numeric -> default (string)');
check($post['PATH_MAPS'] === '/library=>/mnt/user/media', 'path maps preserved');
check($post['CRON_INTERVAL'] === '1', 'cron clamped to min (string)');

// Empty/missing fields fall back to documented defaults.
$empty = [];
wap_sanitize_settings_post($empty);
check($empty['SERVER_URL'] === 'http://localhost:8096', 'default server url');
check($empty['USERS'] === '', 'default users empty');
check($empty['RAM_PERCENT'] === '50', 'default ram 50');
check($empty['CRON_INTERVAL'] === '15', 'default cron 15');

// Injection: a newline in a value cannot survive to break the KEY="value" line.
$evil = ['SERVER_URL' => "http://x\nINJECTED=pwned"];
wap_sanitize_settings_post($evil);
check(!str_contains($evil['SERVER_URL'], "\n"), 'newline stripped from value');

// --- wap_cfg_csv_from_list ---
check(wap_cfg_csv_from_list(['id-a', 'id-b']) === 'id-a,id-b', 'array joined to csv');
check(wap_cfg_csv_from_list(['id-a', '', ' id-b ']) === 'id-a,id-b', 'array trims and drops empties');
check(wap_cfg_csv_from_list('legacy,names') === 'legacy,names', 'scalar passes through sanitized');
check(wap_cfg_csv_from_list(["a\"b", 'c']) === 'ab,c', 'array elements sanitized');
check(wap_cfg_csv_from_list(['a', ['nested'], 'b']) === 'a,b', 'non-scalar array items skipped');
check(wap_cfg_csv_from_list(['Movies, TV', 'x']) === 'Movies TV,x', 'commas stripped from items (delimiter safety)');
check(wap_cfg_csv_from_list(null) === '', 'null yields empty string');

// --- wap_sanitize_settings_post: USERS[]/LIBRARIES[] arrays ---
$post = ['USERS' => ['id-a', 'id-b'], 'LIBRARIES' => ['lib-1']];
wap_sanitize_settings_post($post);
check($post['USERS'] === 'id-a,id-b', 'USERS array normalized to csv');
check($post['LIBRARIES'] === 'lib-1', 'LIBRARIES array normalized to csv');

$post2 = [];
wap_sanitize_settings_post($post2);
check($post2['LIBRARIES'] === '', 'LIBRARIES defaults empty');

// --- tier dials ---
$post = [
    'TIER_RESUME_ENABLED' => '1', 'TIER_RESUME_MAX' => '15',
    'TIER_NEXTUP_MAX' => '0',                 // NEXTUP_ENABLED absent (unchecked)
    'TIER_RECENT_ENABLED' => 'on', 'TIER_RECENT_MAX' => '5',
];
wap_sanitize_settings_post($post);
check($post['TIER_RESUME_ENABLED'] === '1', 'resume enabled normalized to 1');
check($post['TIER_RESUME_MAX'] === '15', 'resume max preserved');
check($post['TIER_NEXTUP_ENABLED'] === '0', 'absent tier checkbox => 0');
check($post['TIER_RECENT_ENABLED'] === '1', 'any present checkbox value => 1');
check($post['TIER_RECENT_MAX'] === '5', 'recent max preserved');

$empty = [];
wap_sanitize_settings_post($empty);
check($empty['TIER_RESUME_ENABLED'] === '0', 'all-absent => disabled flag 0');
check($empty['TIER_RESUME_MAX'] === '0', 'max default 0');

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: settings sanitizer\n";
