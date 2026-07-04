#!/bin/bash
# Guards the .plg render (plugin/plugin.j2) against producing malformed XML - the
# CDATA-injection gap in #23. Faithfully mirrors the release.yml renderer: the same
# stdlib regex substitution, the same explicit error on a missing env var, and the
# same "]]>" -> "]]]]><![CDATA[>" CDATA escaping (applied here so the test verifies
# the actual transform, not a hand-escaped string). Then asserts the rendered .plg
# is well-formed XML. Runs in CI and the pre-push gate so template or escaping drift
# fails at PR time, not at user install.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPL="${REPO_ROOT}/plugin/plugin.j2"
fail() { echo "FAIL: $1" >&2; exit 1; }

# render_check CHANGELOG ESCAPE(0|1) -> prints "OK" (well-formed) or "MALFORMED".
# ESCAPE=1 applies the same CDATA escaping release.yml does before rendering.
render_check() {
    PLUGIN_VERSION="2026.07.99" PLUGIN_CHECKSUM="deadbeef" \
    PLUGIN_CHANGELOG="$1" WAP_ESCAPE="$2" \
        python3 - "$TMPL" <<'PY'
import os, re, sys, xml.etree.ElementTree as ET

# Mirror release.yml: split "]]>" so it cannot terminate the CDATA block early.
if os.environ.get("WAP_ESCAPE") == "1":
    os.environ["PLUGIN_CHANGELOG"] = os.environ["PLUGIN_CHANGELOG"].replace(
        "]]>", "]]]]><![CDATA[>")

tmpl = open(sys.argv[1], encoding="utf-8").read()
token = re.compile(r"\{\{\s*env\[['\"]([^'\"]+)['\"]\]\s*\}\}")


def replace(match):
    key = match.group(1)
    value = os.environ.get(key)
    if value is None:  # same explicit, actionable failure as release.yml
        sys.exit(f"render: missing environment variable {key}")
    return value


rendered = token.sub(replace, tmpl)
try:
    # Validate on bytes (like release.yml's ET.parse of the file) so a future
    # encoding= declaration in the .plg cannot trip str-parsing.
    ET.fromstring(rendered.encode("utf-8"))
    print("OK")
except ET.ParseError:
    print("MALFORMED")
PY
}

RAW='boom]]>oops <b>and</b> more'

# A normal changelog renders to well-formed XML.
[ "$(render_check 'Release 2026.07.99: fixed a thing' 0)" = "OK" ] \
    || fail "normal changelog did not render well-formed .plg"

# A raw "]]>" (unescaped) breaks the CDATA block - the gap #23 guards against.
[ "$(render_check "$RAW" 0)" = "MALFORMED" ] \
    || fail "expected an unescaped ]]> to yield malformed XML"

# The SAME raw input, run through the release escaping, renders well-formed -
# verifying the escape transform itself, not a pre-escaped string.
[ "$(render_check "$RAW" 1)" = "OK" ] \
    || fail "escaped ]]> should render well-formed .plg"

echo "PASS: plg render CDATA safety"
