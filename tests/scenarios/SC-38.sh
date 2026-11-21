#!/usr/bin/env bash
# SC-38: the scenario harness runs lore as its person, also when the gate
# is started from inside a coding agent's session (devco:D-880, bug hunt
# H-139). The script plays that caller: an agent's marker is set before
# tests/lib/common.sh is sourced. whoami then still names the person, and a
# task added without --commitment is written (an agent would be asked for
# --commitment). With the marker and LORE_HUMAN unset for one call, lore
# still sees the agent: the harness neutralises the caller, not detection.
#
# Run via: LORE=/path/to/bin/lore bash tests/scenarios/SC-38.sh

# the caller is an agent session (a made-up thread id of an agent CLI)
export CODEX_THREAD_ID=sc38-thread
source "$(dirname "$0")/../lib/common.sh"
mk_tmp
git init -q
git remote add origin https://github.com/test/sc38.git
$LORE init --non-interactive --name=sc38 >/dev/null || fail "init failed"

# 1. the person, not "<person> via codex"
who=$($LORE whoami 2>&1) || fail "whoami failed: $who"
echo "$who" | grep -q "^identity .*Person" || fail "whoami names no person:
$who"
echo "$who" | grep -q " via " && fail "the harness runs lore as the calling agent:
$who"

# 2. a person's task add needs no --commitment
out=$($LORE task add --title "sc38 person task" 2>&1) || fail "a person's task add was refused:
$out"
echo "$out" | grep -q "commitment is required" && fail "a person's task add asked for --commitment:
$out"

# 3. the agent itself is still detected when the harness's switch is off
agent=$(env -u LORE_HUMAN $LORE whoami 2>&1) || fail "whoami as the agent failed: $agent"
# (" via <tool>": codex from the marker, or the agent the gate itself runs under)
echo "$agent" | grep -q "^identity .* via " || fail "with LORE_HUMAN unset lore does not see the agent:
$agent"

echo "SC-38 ok"
