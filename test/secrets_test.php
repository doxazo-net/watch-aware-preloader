<?php

declare(strict_types=1);

require __DIR__ . '/../src/usr/local/emhttp/plugins/watch-aware-preloader/include/secrets.php';

$failures = 0;
function check(bool $cond, string $msg): void
{
    global $failures;
    if (!$cond) {
        fwrite(STDERR, "FAIL: {$msg}\n");
        $failures++;
    }
}

$dir = sys_get_temp_dir() . '/wapsec_' . getmypid();
$path = $dir . '/secrets.toml';
@mkdir($dir, 0700, true);

check(wap_api_key_is_set($path) === false, 'unset when file missing');

wap_write_api_key($path, 'abc123');
check(is_file($path), 'file created');
$contents = file_get_contents($path);
check(str_contains($contents, 'api_key = "abc123"'), 'key written under [server]');
check(str_contains($contents, '[server]'), '[server] table written');
check(wap_api_key_is_set($path) === true, 'set after write');

// TOML-injection hardening (hostile-review finding 4): a key containing " \ or
// control chars (e.g. a pasted interior newline/tab) must be escaped so
// secrets.toml stays valid single-line TOML and round-trips.
$evil = "a\"b\\c\n\tx"; // -> a"b\c<newline><tab>x
wap_write_api_key($path, $evil);
$contents = file_get_contents($path);
// The value must stay on ONE physical line: comment + [server] + api_key line
// each end in \n, so exactly 3 raw newlines; an injected raw newline -> 4+.
check(substr_count($contents, "\n") === 3, 'no injected raw newline in secrets.toml');
check(str_contains($contents, '\\"'), 'double-quote escaped');
check(str_contains($contents, '\\\\'), 'backslash escaped');
check(str_contains($contents, '\\n'), 'newline escaped as \\n');
check(str_contains($contents, '\\t'), 'tab escaped as \\t');
check(wap_api_key_is_set($path) === true, 'evil key reported set');

// Write-failure propagation (CR review finding 1): a write to an unwritable
// location must return false so the caller reports a real error, not "saved".
// Root ignores mode bits, so skip the case when running as root.
$isRoot = \function_exists('posix_geteuid')
    ? (posix_geteuid() === 0)
    : (trim((string) @shell_exec('id -u')) === '0');
if ($isRoot) {
    fwrite(STDOUT, "SKIP: write-failure case (running as root ignores mode bits)\n");
} else {
    $roDir = sys_get_temp_dir() . '/wapsec_ro_' . getmypid();
    @mkdir($roDir, 0700, true);
    @chmod($roDir, 0500); // read+execute only: creating a subdir/file must fail
    $roPath = $roDir . '/nope/secrets.toml'; // parent 'nope' cannot be created
    check(wap_write_api_key($roPath, 'xyz') === false, 'write failure reported on unwritable path');
    check(!is_file($roPath), 'no file created on failed write');
    @chmod($roDir, 0700);
    @rmdir($roDir);
}

// Overwrite with empty -> reported unset.
wap_write_api_key($path, '');
check(wap_api_key_is_set($path) === false, 'empty key reported unset');

@unlink($path);
@rmdir($dir);

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failure(s)\n");
    exit(1);
}
echo "PASS: secrets helpers\n";
