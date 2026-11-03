#!/usr/bin/env bash
# run.sh: the real-repo trial. Plays an agency (two developers, two agents) working
# in a client's repo on a real git host, with the agency's internal notes in a
# second repo (the store), and checks after every step that nothing internal
# reached the client's clone. See docs/team/real-repo-trial.md.
#
#   LORE_TRIAL_CLIENT_REMOTE=git@github.com:you/lore-trial-client.git \
#   LORE_TRIAL_STORE_REMOTE=git@github.com:you/lore-trial-store.git \
#   tests/team/trial/run.sh
#
# MANUAL ONLY: it deletes every ref of both remotes at the start (guarded by a
# marker file, see lib.sh), pushes to them for real and takes a few minutes. It
# must never run in CI or in any automatic gate.
#
#   LORE_TRIAL_DIR      work dir (default: a new mktemp -d); must be empty or absent
#   LORE_BIN            lore binary to drive (default: built from this checkout)
#   LORE_TRIAL_ADOPT    remote URL(s), space-separated, that may be wiped once although they carry no marker
#
# Stdout ends with one row per step (checks passed, client leak scan, result).
# Exit 1 when any check failed or any internal marker reached the client clone;
# exit 2 when the trial refused to start (missing settings or the safety guard).

set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"

CLIENT_URL="${LORE_TRIAL_CLIENT_REMOTE:-}"
STORE_URL="${LORE_TRIAL_STORE_REMOTE:-}"
if [ -z "$CLIENT_URL" ] || [ -z "$STORE_URL" ]; then
  {
    echo "refused: set LORE_TRIAL_CLIENT_REMOTE and LORE_TRIAL_STORE_REMOTE to two private repos you created for this trial, e.g."
    echo "  LORE_TRIAL_CLIENT_REMOTE=git@github.com:you/lore-trial-client.git"
    echo "  LORE_TRIAL_STORE_REMOTE=git@github.com:you/lore-trial-store.git"
    echo "The trial deletes every ref of both, so never point them at a real repo. See docs/team/real-repo-trial.md."
  } >&2
  exit 2
fi
if [ "$CLIENT_URL" = "$STORE_URL" ]; then
  echo "refused: LORE_TRIAL_CLIENT_REMOTE and LORE_TRIAL_STORE_REMOTE must be two different repos" >&2
  exit 2
fi

T="${LORE_TRIAL_DIR:-$(mktemp -d)}"
mkdir -p "$T"
if [ -n "$(ls -A "$T")" ]; then
  echo "refused: LORE_TRIAL_DIR $T is not empty; give an empty or new directory" >&2
  exit 2
fi
T="$(cd "$T" && pwd)"
mkdir -p "$T/homes" "$T/work" "$T/logs" "$T/bin"
LOG="$T/logs/trial.log"

# shellcheck source=lib.sh
. "$HERE/lib.sh"

echo "trial dir: $T (transcript: $LOG, client scans: $T/logs/clientcheck.log)" >&2

# Safety guard first: nothing is built, cloned or pushed until both remotes pass it.
guard_remote client "$CLIENT_URL" || exit 2
guard_remote store "$STORE_URL" || exit 2

if [ -z "${LORE_BIN:-}" ]; then
  LORE_BIN="$T/bin/lore-build"
  echo "building lore from $REPO" >&2
  (cd "$REPO" && CGO_ENABLED=0 go build -o "$LORE_BIN" ./saas/cmd/cli) || { echo "build failed" >&2; exit 2; }
fi
# Hooks lore installs call plain `lore` from PATH: put the binary under test first.
ln -sf "$LORE_BIN" "$T/bin/lore"
export PATH="$T/bin:$PATH"

CLIENT_SLUG=$(repo_slug "$CLIENT_URL")
CLIENT_NAME=${CLIENT_SLUG##*/}

# Step bookkeeping: one row per step in the final table.
ROWS=()
STEP=""
STEP_CHECKS=0
STEP_FAILS=0
STEP_LEAK=clean
TOTAL_FAILS=0
LEAKS=0

begin_step() {
  STEP="$1"
  STEP_CHECKS=0
  STEP_FAILS=0
  STEP_LEAK="-"
  LAST_OUT=""
  printf '\n######## %s ########\n' "$STEP" >> "$LOG"
  echo "== $STEP" >&2
}

# end_step [noscan]: run the client leak scan (unless noscan) and record the row.
end_step() {
  if [ "${1:-}" != noscan ]; then client_scan; fi
  local result=PASS
  if [ "$STEP_FAILS" -gt 0 ] || [ "$STEP_LEAK" = LEAK ]; then result=FAIL; fi
  [ "$STEP_LEAK" = LEAK ] && LEAKS=$((LEAKS + 1))
  TOTAL_FAILS=$((TOTAL_FAILS + STEP_FAILS))
  ROWS+=("$(printf '%-34s %5s/%-5s %-10s %s' "$STEP" "$((STEP_CHECKS - STEP_FAILS))" "$STEP_CHECKS" "$STEP_LEAK" "$result")")
}

HB_PID=""
cleanup() { if [ -n "$HB_PID" ]; then kill -9 "$HB_PID" 2>/dev/null || true; fi; }
trap cleanup EXIT

begin_step "00 wipe both remotes"
wipe_remote client "$CLIENT_URL" client >> "$LOG" 2>&1
check "wipe the client remote $CLIENT_URL" $?
wipe_remote store "$STORE_URL" store >> "$LOG" 2>&1
check "wipe the store remote $STORE_URL" $?
end_step noscan
if [ "$STEP_FAILS" -gt 0 ]; then
  echo "the wipe failed; stopping before any step runs (see $LOG)" >&2
  printf '%s\n' "${ROWS[@]}"
  exit 1
fi

for s in "$HERE"/steps/[0-9]*.sh; do
  . "$s"
done

printf '\nreal-repo trial: %s (client) + %s (store)\n' "$CLIENT_URL" "$STORE_URL"
printf '%-34s %-11s %-10s %s\n' "step" "checks" "leak scan" "result"
printf '%s\n' "${ROWS[@]}"
if [ "$TOTAL_FAILS" -eq 0 ] && [ "$LEAKS" -eq 0 ]; then
  printf 'PASS: every check passed and no internal marker reached the client clone (%s)\n' "$T"
  exit 0
fi
printf 'FAIL: %d check(s) failed, %d step(s) with an internal marker in the client clone; transcript %s\n' "$TOTAL_FAILS" "$LEAKS" "$LOG"
exit 1
