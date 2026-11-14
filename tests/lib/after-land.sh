#!/usr/bin/env bash
# after-land.sh: the full gate (`task check`) on what landed: origin/dev as it is now, in a
# fresh worktree, never the work tree you are in. Used by `task lore:check:after-land`.
#
# The landing rule (docs/DEVELOPMENT.md "Landing"): a small fix may land after `task check:fast`;
# the full gate then runs here on the landed main, and a failure is fixed at once.
#
# Writes LORE_GATE_REPORT (default ${XDG_STATE_HOME:-~/.local/state}/lore-gate/last.md): first
# line "PASS <commit> <subject>" or "FAIL <commit> <subject>", then the gate's timing table; the
# step logs go to a folder beside it (<report>.logs/). The worktree is removed afterwards. Exit
# status is the gate's. LORE_AFTER_LAND_REF names another ref to test (default origin/dev, the branch fixes land on).
set -uo pipefail

ref="${LORE_AFTER_LAND_REF:-origin/dev}"
remote="${ref%%/*}"
branch="${ref#*/}"
report="${LORE_GATE_REPORT:-${XDG_STATE_HOME:-$HOME/.local/state}/lore-gate/last.md}"
logs="$report.logs"
mkdir -p "$(dirname "$report")"

# git network calls can hang on a stuck connection: bounded, retried.
export GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh -o ConnectTimeout=20}"
fetched=0
for _ in 1 2 3 4 5; do
  if git fetch -q "$remote" "$branch"; then fetched=1; break; fi
  sleep 5
done
if [ "$fetched" -ne 1 ]; then
  printf 'FAIL could not fetch %s\n' "$ref" >"$report"
  echo "✘ after-land: could not fetch $ref" >&2
  exit 1
fi
sha=$(git rev-parse "$ref") || exit 1
subject=$(git log -1 --format=%s "$sha")

wt=$(mktemp -d "${TMPDIR:-/tmp}/lore-after-land.XXXXXX")
rmdir "$wt"
git worktree add -q --detach "$wt" "$sha" || exit 1
cleanup() { git worktree remove --force "$wt" >/dev/null 2>&1 || rm -rf "$wt"; git worktree prune; }
trap cleanup EXIT

echo "after-land: task check on $ref = $sha ($subject) in $wt"
(cd "$wt" && task check </dev/null) >"$wt.log" 2>&1
rc=$?

rm -rf "$logs"
mkdir -p "$logs"
cp -R "$wt/tmp/check/." "$logs/" 2>/dev/null
mv "$wt.log" "$logs/check.log"
{
  if [ "$rc" -eq 0 ]; then echo "PASS $sha $subject"; else echo "FAIL $sha $subject"; fi
  echo
  echo "Full gate (task check) on $ref, $(date -u +%Y-%m-%dT%H:%M:%SZ). Step logs: $logs"
  echo
  echo '```'
  sed -n '/^gate timings so far/,$p' "$logs/check.log" | tail -n 60
  echo '```'
} >"$report"
head -1 "$report"
exit "$rc"
