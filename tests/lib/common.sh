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
#
# PATH is hermetic: sourcing this file replaces the caller's PATH with a shim
# dir holding only the binary under test (as `lore`) and the external tools
# scenarios use, followed by the system dirs. The developer's PATH never
# leaks in, so an installed lore (Homebrew, ~/go/bin) can neither shadow the
# binary under test in the hooks lore installs nor make `lore doctor` report
# a PATH conflict that exists only on that machine.

set -u

LORE="${LORE:-aicoder}"
TMP=""
LORE_SHIM=""

# HERMETIC_TOOLS are the external programs scenarios and chaos scripts run
# that may live outside the system dirs (Homebrew, a Go install). Each is
# linked into the shim from the caller's PATH when present; `need` still
# skips a script whose tool is missing.
HERMETIC_TOOLS="git jq sqlite3 python3 go"
# HERMETIC_SYSTEM_PATH holds the base tools (sh, which, mktemp, hdiutil, …).
# /usr/local/bin is left out on purpose: Intel Homebrew installs lore there.
HERMETIC_SYSTEM_PATH="/usr/bin:/bin:/usr/sbin:/sbin"

hermetic_path() {
    local bin tool
    bin=$(command -v "$LORE" 2>/dev/null) || return 0 # unresolvable: the first $LORE call fails loudly
    case "$bin" in /*) ;; *) bin="$PWD/$bin" ;; esac
    LORE_SHIM=$(mktemp -d)
    ln -s "$bin" "$LORE_SHIM/lore"
    for tool in $HERMETIC_TOOLS; do
        if command -v "$tool" >/dev/null 2>&1; then
            ln -s "$(command -v "$tool")" "$LORE_SHIM/$tool"
        fi
    done
    LORE="$bin"
    PATH="$LORE_SHIM:$HERMETIC_SYSTEM_PATH"
    export LORE PATH
}

cleanup() {
    if [ -n "${TMP:-}" ] && [ -d "$TMP" ]; then
        rm -rf "$TMP"
    fi
    if [ -n "${LORE_SHIM:-}" ] && [ -d "$LORE_SHIM" ]; then
        rm -rf "$LORE_SHIM"
    fi
}
trap cleanup EXIT

hermetic_path

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
