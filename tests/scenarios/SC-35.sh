#!/usr/bin/env bash
# SC-35: the secret menu through a real terminal (pty), r/u/n/c, and one
# secret = one finding in a resource import's refusal (later 5, fb-sec2).
# The value never shows past its masked preview; c writes nothing; r
# stores a reference; u redacts as unstored; n keeps the text with allow
# entries.
#
# Run via: LORE=/path/to/bin/lore bash tests/scenarios/SC-35.sh
# Needs script(1) (util-linux) for the pty; skips loudly without it.

set -e
trap 'cleanup' EXIT

LORE="${LORE:-lore}"

cleanup() {
    if [ -n "${TMP:-}" ] && [ -d "$TMP" ]; then
        rm -rf "$TMP"
    fi
}

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

if ! command -v script >/dev/null 2>&1; then
    fail "script(1) not installed: this scenario needs a pty"
fi

TMP=$(mktemp -d)
export HOME="$TMP/home"
mkdir -p "$HOME"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.test GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.test
mkdir -p "$TMP/repo" && cd "$TMP/repo"
git init -q .
$LORE init --non-interactive --name=sc35 >/dev/null 2>&1 || fail "init failed"

# The fixture key is built at run time (CLAUDE.md convention 8).
KEY="AKIA$(printf 'Q%.0s' 1 2 3 4)7XRT2MNB5KZ3"
mems() { find .lore/data -name 'mem_*' | wc -l | tr -d ' '; }
allows() { ls .lore/data/secret_allows 2>/dev/null | wc -l | tr -d ' '; }

# menu ANSWERS TEXT: answer the menu through a pty; prints the output.
menu() {
    local answers="$1" text="$2"
    (sleep 1; printf "$answers"; sleep 1) | script -qec "$LORE memory add \"$text\"" /dev/null 2>&1 | tr -d '\r'
}
novalue() {
    # the typed key never shows; only its masked preview does
    if printf '%s' "$1" | grep -q "$KEY"; then fail "$2: the secret value was printed"; fi
}

out=$(menu 'c\n' "key $KEY")
novalue "$out" "cancel"
printf '%s' "$out" | grep -q "1 possible secret" || fail "cancel: no menu: $out"
printf '%s' "$out" | grep -q "E_NEEDS_CONFIRM" || fail "cancel: not refused: $out"
[ "$(mems)" = 0 ] || fail "cancel wrote a memory"

out=$(menu 'r\ngopass://acme/aws\n' "key $KEY")
novalue "$out" "redact"
[ "$(mems)" = 1 ] || fail "redact: memory count $(mems), want 1"
grep -rq 'secret:gopass://acme/aws' .lore/data/memories || fail "redact: no reference stored"

out=$(menu 'u\n' "key $KEY 2")
novalue "$out" "unstored"
[ "$(mems)" = 2 ] || fail "unstored: memory count $(mems), want 2"
grep -rq 'secret:unstored' .lore/data/memories || fail "unstored: no \${secret:unstored} stored"

out=$(menu 'n\naws docs sample\ny\n' "key $KEY 3")
novalue "$out" "not a secret"
[ "$(mems)" = 3 ] || fail "not a secret: memory count $(mems), want 3"
[ "$(allows)" -ge 1 ] || fail "not a secret: no allow entry"
grep -rlq "$KEY 3" .lore/data/memories || fail "not a secret: the text was not kept"

# resource import: one secret is one finding (two rules matching the same
# value are one finding), and nothing is written.
$LORE resource kind add host --yes >/dev/null || fail "kind add failed"
$LORE resource field add host note --type text --yes >/dev/null || fail "field add failed"
printf 'name,note\nd,key %s\n' "$KEY" > bad.csv
set +e
json=$($LORE resource import bad.csv --kind host --yes --json </dev/null 2>&1)
set -e
novalue "$json" "import"
n=$(printf '%s' "$json" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["error"]["findings"]))') || fail "import: no findings in $json"
[ "$n" = 1 ] || fail "import: $n findings, want 1"
[ -z "$(find .lore/data -name 'res_*')" ] || fail "import wrote a resource"

echo "PASS: SC-35"
