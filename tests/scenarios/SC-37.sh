#!/usr/bin/env bash
# SC-37: gates and task merged on a checkout with live rows off
# (LORE_LIVE=0, later 213): gate request stamps the task through the core;
# an unknown id is E_NOT_FOUND (exit 27) naming the id with the task list
# hint; gate approve honours the mission's approvers read through the core
# (refused when the person is not on the list, approved when they are,
# any person approves a task with no mission);
# task merged records merged_at on a done task, says "already merged" the
# second time, refuses a task that is not done and an unknown id.
#
# Run via: LORE=/path/to/bin/lore bash tests/scenarios/SC-37.sh
#
# Only a person approves a gate, and lore detects a coding agent from the
# environment and from its parent processes. So the scenario re-runs
# itself with a clean environment in a new session whose parent is not the
# caller (a double fork under setsid; the caller only waits on a status file).

set -e

LORE="${LORE:-lore}"
LORE=$(command -v "$LORE")

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

if [ -z "${SC37_DETACHED:-}" ]; then
    command -v setsid >/dev/null 2>&1 || fail "setsid(1) not installed: this scenario needs a detached session"
    OUT=$(mktemp -d)
    trap 'rm -rf "$OUT"' EXIT
    # double fork: the subshell exits at once, so the detached run's parent
    # is init, not this shell (and not whatever agent started it).
    (env -i SC37_DETACHED=1 LORE="$LORE" OUT="$OUT" SCRIPT="$0" PATH=/usr/bin:/bin:/usr/local/bin \
        setsid sh -c 'bash "$SCRIPT" >"$OUT/log" 2>&1; echo $? >"$OUT/rc.tmp"; mv "$OUT/rc.tmp" "$OUT/rc"' &)
    for _ in $(seq 1 240); do
        [ -f "$OUT/rc" ] && break
        sleep 0.5
    done
    cat "$OUT/log"
    [ -f "$OUT/rc" ] || fail "the detached run did not finish in 120 s"
    exit "$(cat "$OUT/rc")"
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
export HOME="$TMP/home"
mkdir -p "$HOME" "$TMP/bin"
ln -s "$LORE" "$TMP/bin/lore"
export PATH="$TMP/bin:$PATH"
export LORE_LIVE=0
git config --global user.name "Pat Person"
git config --global user.email pat@example.test
git config --global init.defaultBranch main
git init -q --bare "$TMP/origin.git"
git clone -q "$TMP/origin.git" "$TMP/repo" 2>/dev/null
cd "$TMP/repo"
lore init --non-interactive --name=sc37 >/dev/null 2>&1 || fail "init failed"
git add -A && git commit -qm init && git push -q origin main 2>/dev/null || fail "first push"

lore whoami | grep -q "human:pat@example.test" || fail "not a person here: $(lore whoami | sed -n 2p)"
ME=$(lore whoami | grep -o 'act_[0-9a-f]\{32\}' | head -1)
# A well-formed task id (UUIDv7: version nibble 7, variant 8) that no row
# has, so the refusal is "not found" (27). A hex string that is not a
# UUIDv7 is E_INVALID_ID (20) before any lookup.
UNKNOWN=tsk_019a0000000070008000000000000000

# expect CODE PATTERN CMD...: exit CODE and PATTERN in the output.
expect() {
    local want="$1" pat="$2" out code
    shift 2
    set +e
    out=$("$@" 2>&1)
    code=$?
    set -e
    [ "$code" = "$want" ] || fail "$*: exit $code, want $want: $out"
    printf '%s' "$out" | grep -q -- "$pat" || fail "$*: no '$pat' in: $out"
}
tid() { grep -o 'tsk_[0-9a-f]\{32\}' | head -1; }

# gate request through the core; unknown id
TASK=$(lore task add "ship the release" --json | tid)
expect 0 "gate requested on $TASK" lore gate request "$TASK"
# task show does not print the gate fields; the row file is the truth.
rowfile() { find .lore/data/tasks -name "$1.json"; }
grep -q '"gate": *"requested"' "$(rowfile "$TASK")" || fail "the gate is not on the row: $(cat "$(rowfile "$TASK")")"
expect 27 "no task $UNKNOWN" lore gate request "$UNKNOWN"
expect 27 "lore task list" lore gate request "$UNKNOWN"

# gate approve honours the mission's approvers
OTHER=act_01936f0000007a1b8c2d3e4f5a6b7c8d # a well-formed actor id that is not this person
MSN=$(lore mission add "release 1" --approvers "$OTHER" --json | grep -o 'msn_[0-9a-f]\{32\}' | head -1)
[ -n "$MSN" ] || fail "mission add gave no id"
GATED=$(lore task add "deploy" --mission "$MSN" --json | tid)
expect 0 "gate requested" lore gate request "$GATED"
expect 20 "names its approvers; you are not one of them" lore gate approve "$GATED"
grep -q '"approved_by": *"act_' "$(rowfile "$GATED")" && fail "a refused approve stamped approved_by"
expect 0 "approved by" lore gate approve "$TASK" # no mission: any person approves
# a mission that lists this person (with someone else) lets them approve
MINE=$(lore mission add "release 2" --approvers "$OTHER,$ME" --json | grep -o 'msn_[0-9a-f]\{32\}' | head -1)
GATED=$(lore task add "deploy 2" --mission "$MINE" --json | tid)
expect 0 "gate requested" lore gate request "$GATED"
expect 0 "approved by Pat Person (git config)" lore gate approve "$GATED"
grep -q "\"approved_by\": *\"$ME\"" "$(rowfile "$GATED")" || fail "approved_by is not $ME: $(cat "$(rowfile "$GATED")")"

# task merged
expect 20 "only a done task can be merged" lore task merged "$TASK"
lore task done "$TASK" --no-lesson >/dev/null 2>&1 || fail "task done: $(lore task done "$TASK" --no-lesson 2>&1)"
expect 0 "merged (marked by hand" lore task merged "$TASK"
expect 0 "was already merged at" lore task merged "$TASK"
grep -q '"merged_at": *"2' "$(rowfile "$TASK")" || fail "merged_at not on the row: $(cat "$(rowfile "$TASK")")"
expect 27 "$UNKNOWN" lore task merged "$UNKNOWN"

echo "SC-37 ok"
