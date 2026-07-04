#!/bin/bash
# Guards the .plg render (plugin/plugin.j2) against producing malformed XML - the
# CDATA-injection gap in #23. Mirrors the release.yml renderer (stdlib-only regex
# substitution) and its CDATA-safe escaping of the changelog, then asserts the
# rendered .plg is well-formed XML. Runs in CI and the pre-push gate so template
# or escaping drift fails at PR time, not at user install.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPL="${REPO_ROOT}/plugin/plugin.j2"
fail() { echo "FAIL: $1" >&2; exit 1; }

# render_check CHANGELOG -> prints "OK" (well-formed) or "MALFORMED"
render_check() {
    PLUGIN_VERSION="2026.07.99" PLUGIN_CHECKSUM="deadbeef" PLUGIN_CHANGELOG="$1" \
        python3 - "$TMPL" <<'PY'
import os, re, sys, xml.etree.ElementTree as ET
tmpl = open(sys.argv[1], encoding="utf-8").read()
token = re.compile(r"\{\{\s*env\[['\"]([^'\"]+)['\"]\]\s*\}\}")
rendered = token.sub(lambda m: os.environ[m.group(1)], tmpl)
try:
    ET.fromstring(rendered)
    print("OK")
except ET.ParseError:
    print("MALFORMED")
PY
}

# A normal changelog renders to well-formed XML.
[ "$(render_check 'Release 2026.07.99: fixed a thing')" = "OK" ] \
    || fail "normal changelog did not render well-formed .plg"

# A raw "]]>" in the changelog breaks the CDATA block (the gap #23 guards against).
[ "$(render_check 'boom]]>oops')" = "MALFORMED" ] \
    || fail "expected an unescaped ]]> to yield malformed XML"

# The release.yml escaping ("]]>" -> "]]]]><![CDATA[>") makes it well-formed again.
[ "$(render_check 'boom]]]]><![CDATA[>oops')" = "OK" ] \
    || fail "CDATA-escaped ]]> should render well-formed .plg"

echo "PASS: plg render CDATA safety"
