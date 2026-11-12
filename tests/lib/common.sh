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
#   need CMD    — FAIL (exit 1) when a tool the script needs is missing;
#                 `need sqlite3` builds tests/tools/sqlite3 (Go) instead
#   json_get EXPR / json_true EXPR — read JSON on stdin (python3, below)
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
# linked into the shim from the caller's PATH when present; `need` fails a
# script whose tool is missing. These are the tools every gate machine has
# (git, Go to build lore, python3 from the system or Xcode tools); jq and the
# sqlite3 shell are not on every machine, so no script uses jq (json_get and
# json_true below use python3) and `need sqlite3` builds a Go stand-in.
HERMETIC_TOOLS="git python3 go"
# LORE_REPO_ROOT is the checkout this file lives in (the Go helpers' module).
LORE_REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# LORE_TOOL_HOME is HOME as the caller had it: isolate_home moves HOME, and a
# go build under the new one would start from an empty module and build cache.
LORE_TOOL_HOME="$HOME"
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

# need fails loudly when a tool is missing: a script that skipped proved
# nothing on that machine, and the gate printed it as a pass. sqlite3 is the
# Go stand-in in tests/tools/sqlite3 (same driver as lore), built into this
# script's shim, so the check runs the same on every machine.
need() {
    case "$1" in
    sqlite3) build_sqlite3 ;;
    *) command -v "$1" >/dev/null 2>&1 || fail "missing tool: $1 (this script needs it; install it on this machine)" ;;
    esac
}

build_sqlite3() {
    command -v go >/dev/null 2>&1 || fail "missing tool: go (needed to build tests/tools/sqlite3)"
    [ -n "$LORE_SHIM" ] || fail "no PATH shim: \$LORE ($LORE) did not resolve"
    [ -x "$LORE_SHIM/sqlite3" ] && return 0
    (cd "$LORE_REPO_ROOT" && HOME="$LORE_TOOL_HOME" CGO_ENABLED=0 go build -o "$LORE_SHIM/sqlite3" ./tests/tools/sqlite3) \
        || fail "cannot build tests/tools/sqlite3"
}

# json_get EXPR prints EXPR, a python expression over d (the JSON read from
# stdin): a string as is, anything else as JSON. json_true EXPR exits 0
# when EXPR is neither null nor false (jq -e's rule). alt(a, b, ...) is jq's
# `a // b`: the first value that is neither null nor false. Both need python3.
JSON_PRELUDE='import json,sys
def alt(*xs):
    return next((x for x in xs if x is not None and x is not False), None)
d=json.load(sys.stdin)
v=eval(sys.argv[1])'
json_get() {
    python3 -c "$JSON_PRELUDE
print(v if isinstance(v,str) else json.dumps(v))" "$1"
}
json_true() {
    python3 -c "$JSON_PRELUDE
sys.exit(1 if v is None or v is False else 0)" "$1"
}

# mk_tmp makes the script's temp folder, cds into it and gives the script
# its own HOME there (isolate_home): no scenario reads or writes the
# developer's real ~/.lore, and a doctor or identity check sees only what
# the script set up, not whatever that machine holds.
mk_tmp() {
    TMP=$(mktemp -d)
    cd "$TMP"
    isolate_home
}

# lore_count TABLE prints how many live rows of TABLE this repo's lens
# holds, read through `lore sql` (the files are the truth, the cache is
# rebuilt from them; neither is opened behind lore's back). Fails the
# script when the answer is not a number.
lore_count() {
    local n
    n=$($LORE sql "SELECT COUNT(*) AS n FROM $1" --format csv | sed -n 2p) || fail "lore sql count $1"
    case "$n" in '' | *[!0-9]*) fail "lore sql count $1: not a number: '$n'" ;; esac
    printf '%s\n' "$n"
}

# isolate_home gives the script its own lore home ($TMP/home) and a git
# identity, so a scenario that reads or tampers ~/.lore (the machine store,
# the audit chain) never touches the developer's real one. mk_tmp calls it;
# calling it again is harmless.
# repo_cache prints the cache file of the code repo checkout at the current
# folder (its top): <lore home>/cache/repo/<folder name>-<12 hex of sha256 of
# the real path>.db (wire.RepoCachePath; the cache left the work tree, so a
# command never creates a file in the repo). Needs python3.
repo_cache() {
    python3 -c 'import hashlib,os,re; t=os.path.realpath(os.getcwd()); n=re.sub(r"[^A-Za-z0-9._-]","_",os.path.basename(t)).strip(".") or "repo"; print(os.path.join(os.environ["HOME"],".lore","cache","repo",n+"-"+hashlib.sha256(t.encode()).hexdigest()[:12]+".db"))'
}

isolate_home() {
    export HOME="$TMP/home"
    mkdir -p "$HOME"
    export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    git config --global user.email person@example.com
    git config --global user.name Person
    git config --global init.defaultBranch main
}

# Init a fresh aicoder project at cwd with given name.
init_project() {
    local name="${1:-test}"
    git init -q
    git remote add origin "https://github.com/test/${name}.git" 2>/dev/null || true
    $LORE init --non-interactive --name="$name" >/dev/null || fail "init failed"
}
