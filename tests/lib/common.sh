#!/usr/bin/env bash
# common.sh — shared helpers for SC-*/CH-* scripts.
# Source from each scenario script:
#   source "$(dirname "$0")/../lib/common.sh"
#
# Provides:
#   LORE     — path to aicoder binary (env override allowed)
#   TMP         — fresh mktemp -d, auto-cleaned via trap
#   fail MSG    — print "FAIL: MSG" to stderr, exit 1
#   skip MSG    — print "SKIP: MSG", exit 0
#   pass NAME   — print "PASS: NAME", exit 0
#   need CMD    — skip with reason if external tool missing
#   require_db_field SQL EXPECTED — sqlite3 assertion helper

set -u

LORE="${LORE:-aicoder}"
TMP=""

cleanup() {
    if [ -n "${TMP:-}" ] && [ -d "$TMP" ]; then
        rm -rf "$TMP"
    fi
}
trap cleanup EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }
skip() { echo "SKIP: $1"; exit 0; }
pass() { echo "PASS: $1"; exit 0; }
need() { command -v "$1" >/dev/null 2>&1 || skip "missing tool: $1"; }

mk_tmp() {
    TMP=$(mktemp -d)
    cd "$TMP"
}

# Init a fresh aicoder project at cwd with given name.
init_project() {
    local name="${1:-test}"
    git init -q
    git remote add origin "https://github.com/test/${name}.git" 2>/dev/null || true
    $LORE init --non-interactive --name="$name" >/dev/null || fail "init failed"
}
