#!/usr/bin/env bash
# SC-36: the F15 direction rule on the real binary (later 208): a pointer
# stored where a reader could not read its target is E_LEAK_BLOCKED, exit
# 19, and nothing is written, for task add/edit --ref, comment add and link
# add. A shared store (the repo, an internal store) never points at the
# personal store; the personal store may point anywhere; repo and internal
# stores point at each other in an own repo. After the target's store is
# detached, task show prints "→ <id> (not available here)".
# The client-repo directions (client repo → internal store) need an org
# with a hosted store and are covered by ops/task TestRefsF15 and the team
# trial (step 06).
#
# Run via: LORE=/path/to/bin/lore bash tests/scenarios/SC-36.sh

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

TMP=$(mktemp -d)
export HOME="$TMP/home"
mkdir -p "$HOME"
# The git hooks lore installs call `lore`: put this binary first on PATH.
mkdir -p "$TMP/bin" && ln -s "$(command -v "$LORE")" "$TMP/bin/lore"
export PATH="$TMP/bin:$PATH"
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.test GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.test
mkdir -p "$TMP/web" && cd "$TMP/web"
git init -q .
lore init --non-interactive --name=sc36 >/dev/null 2>&1 || fail "init failed"
lore store init agency "file://$TMP/stores/agency" --yes >/dev/null 2>&1 || fail "store init agency"
lore store init me "file://$TMP/stores/me" --yes >/dev/null 2>&1 || fail "store init me"
git commit -q --allow-empty -m init
SHA=$(git rev-parse HEAD)

memid() { grep -o 'mem_[0-9a-f]\{32\}' | head -1; }
REPO_MEM=$(lore memory add "repo note" --json | memid)
ORG_MEM=$(lore memory add "agency note" --to agency --json | memid)
ME_MEM=$(lore memory add "my note" --to me --json | memid)
[ -n "$REPO_MEM" ] && [ -n "$ORG_MEM" ] && [ -n "$ME_MEM" ] || fail "memory add gave no id"

# rows PREFIX: how many rows of that id prefix the binary lists (comments
# and commit links are live rows, not files, so the list commands count).
rows() {
    local cmd
    case "$1" in
    tsk_) cmd="task list --all --json" ;;
    cmt_) cmd="comment list --json" ;;
    cml_) cmd="link list --json" ;;
    *) fail "rows: unknown prefix $1" ;;
    esac
    lore $cmd 2>/dev/null | grep -o "\"$1[0-9a-f]\{32\}" | sort -u | wc -l | tr -d ' '
}

# refused NAME PREFIX CMD...: exit 19, E_LEAK_BLOCKED, no new PREFIX row.
refused() {
    local name="$1" prefix="$2"
    shift 2
    local before out code
    before=$(rows "$prefix")
    set +e
    out=$("$@" 2>&1)
    code=$?
    set -e
    [ "$code" = 19 ] || fail "$name: exit $code, want 19: $out"
    printf '%s' "$out" | grep -q "E_LEAK_BLOCKED" || fail "$name: not E_LEAK_BLOCKED: $out"
    printf '%s' "$out" | grep -qi "nothing written" || fail "$name: no 'nothing written': $out"
    [ "$(rows "$prefix")" = "$before" ] || fail "$name: a refused write left a $prefix row"
}

allowed() {
    local name="$1"
    shift
    "$@" >/dev/null 2>&1 || fail "$name: refused: $("$@" 2>&1)"
}

# shared -> personal: refused for every pointer-writing command
refused "task add repo->me" tsk_ lore task add t1 --commitment accepted --ref "$ME_MEM"
refused "task add agency->me" tsk_ lore task add t2 --commitment accepted --to agency --ref "$ME_MEM"
TASK=$(lore task add plain --commitment accepted --json | grep -o 'tsk_[0-9a-f]\{32\}' | head -1)
refused "task edit repo->me" tsk_ lore task edit "$TASK" --ref "$ME_MEM"
lore task show "$TASK" | grep -q "$ME_MEM" && fail "task edit repo->me stored the pointer"
refused "comment add repo->me" cmt_ lore comment add --on-table memories --on-id "$ME_MEM" --body "on mine"
refused "link add repo->me" cml_ lore link add --entity "$ME_MEM" --commit "$SHA"

# personal -> anywhere, repo <-> internal: allowed (and the row counter
# sees the comment and link rows a pointer write makes)
c0=$(rows cmt_) l0=$(rows cml_)
allowed "comment add repo->repo" lore comment add --on-table memories --on-id "$REPO_MEM" --body "on the repo note"
allowed "link add me->me" lore link add --entity "$ME_MEM" --commit "$SHA" --to me
[ "$(rows cmt_)" -gt "$c0" ] && [ "$(rows cml_)" -gt "$l0" ] || fail "the row counter does not see comment/link rows"
allowed "task add me->repo" lore task add t3 --commitment accepted --to me --ref "$REPO_MEM"
allowed "task add me->agency" lore task add t4 --commitment accepted --to me --ref "$ORG_MEM"
allowed "task add me->me" lore task add t5 --commitment accepted --to me --ref "$ME_MEM"
allowed "task add agency->repo" lore task add t6 --commitment accepted --to agency --ref "$REPO_MEM"
ON_ORG=$(lore task add t7 --commitment accepted --ref "$ORG_MEM" --json | grep -o 'tsk_[0-9a-f]\{32\}' | head -1)
[ -n "$ON_ORG" ] || fail "task add repo->agency refused"

# the target's store detached: the pointer stays and reads as unavailable
lore store remove agency --yes >/dev/null 2>&1 || fail "store remove agency"
lore task show "$ON_ORG" | grep -q "→ $ORG_MEM (not available here)" || fail "show after detach: $(lore task show "$ON_ORG")"

echo "SC-36 ok"
