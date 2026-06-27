<?php

declare(strict_types=1);

// Style/auto-format config for the Unraid plugin's PHP settings page.
// Covers both *.php and Unraid's *.page files under plugin/.
$finder = (new PhpCsFixer\Finder())
    ->in(__DIR__ . '/plugin')
    ->name('*.php')
    ->name('*.page');

return (new PhpCsFixer\Config())
    ->setRiskyAllowed(true)
    ->setRules([
        '@PSR12' => true,
        'array_syntax' => ['syntax' => 'short'],
        'ordered_imports' => ['sort_algorithm' => 'alpha'],
        'no_unused_imports' => true,
        'single_quote' => true,
        'trailing_comma_in_multiline' => true,
        'native_function_invocation' => ['include' => ['@compiler_optimized']],
    ])
    ->setFinder($finder);
