#!/usr/bin/env bash
# lib.sh: helpers for the real-repo trial (tests/team/trial/run.sh). Source it; do not run it.
#
# The trial plays an agency (dev1, dev2, two agents) working in a client's repo on
# a real git host, with the agency's internal notes in a second repo (the store).
# Everything local lives under $T: one HOME per person in $T/homes, one clone per
# person in $T/work, transcripts in $T/logs. The real HOME and ~/.lore are never read.
#
# Needs: LORE_BIN (the lore binary under test), CLIENT_URL, STORE_URL and T.

set -u

# MARKER_FILE / MARKER_TEXT: the file the wipe puts in the first commit of both
# trial repos. The guard wipes a remote only when it has no refs at all or when the
# first commit of its default branch holds exactly this file with exactly this text,
# so a real repo given by mistake is never touched. The name starts with README
# because a lore store refuses top-level files other than README*, LICENSE*,
# .gitattributes and lore-store.yaml (E_NOT_A_STORE).
MARKER_FILE=README.lore-trial.md
MARKER_TEXT="lore real-repo trial scratch repo: tests/team/trial/run.sh deletes every ref here on each run"
# NET_TRIES: tries for every git network call the trial makes itself (the host can be flaky).
NET_TRIES=5
# LEASE_WAIT: seconds to wait for a killed agent's lease to expire. The agency
# config below sets lease.ttl 90s and grace 15s; the rest is slack for renew timing.
LEASE_WAIT=150
# LEAK_RE: what the leak scan looks for. Every internal row the trial writes starts
# with INTERNAL-MARKER-<n>; the note shared with the client on purpose uses SHARE-OK-MARKER.
LEAK_RE='INTERNAL-MARKER-[0-9]+'

export GIT_SSH_COMMAND="ssh -o ConnectTimeout=20"
export GIT_TERMINAL_PROMPT=0
unset LORE_ACTOR LORE_SESSION LORE_ORG

# retry CMD…: run a git network call up to NET_TRIES times.
retry() {
  local i
  for ((i = 1; i <= NET_TRIES; i++)); do
    "$@" && return 0
    echo "[try $i of $NET_TRIES failed: $*]" >&2
    sleep 3
  done
  return 1
}
# The scenario commands run in child shells; they use retry for pushes and pulls that must succeed.
export NET_TRIES
export -f retry

# repo_slug URL: host/owner/repo for a remote URL (scp-like, ssh:// or https://), as lore names it.
repo_slug() {
  local u="${1%.git}"
  u="${u#*://}"
  u="${u#*@}"
  printf '%s\n' "${u/://}"
}

# ---------------------------------------------------------------------------
# Safety guard and wipe
# ---------------------------------------------------------------------------

# guard_remote NAME URL: refuse (return 1, with a message) unless URL may be wiped:
# it has no refs at all, its default branch's first commit carries the marker, or
# LORE_TRIAL_ADOPT lists exactly this URL (space-separated; the one-time bootstrap
# of a scratch repo that predates the marker). Reads only; never writes to the remote.
guard_remote() {
  local name="$1" url="$2" refs head probe root text
  if ! refs=$(retry git ls-remote "$url"); then
    echo "refused: cannot list the refs of the $name remote $url" >&2
    return 1
  fi
  if [ -z "$refs" ]; then
    echo "guard: $name remote $url has no refs (empty); it may be wiped" >&2
    return 0
  fi
  local a
  for a in ${LORE_TRIAL_ADOPT:-}; do
    if [ "$a" = "$url" ]; then
      echo "guard: LORE_TRIAL_ADOPT names $url: adopting it once although it carries no marker" >&2
      return 0
    fi
  done
  head=$(retry git ls-remote --symref "$url" HEAD | awk '$1 == "ref:" { print $2 }')
  if [ "$head" != refs/heads/main ]; then
    echo "refused: the $name remote $url has refs but its default branch is '${head:-none}', not main; it is not a trial repo" >&2
    return 1
  fi
  probe=$(mktemp -d)
  git init -q --bare "$probe"
  if ! retry git -C "$probe" fetch -q "$url" "+$head:refs/heads/probe"; then
    rm -rf "$probe"
    echo "refused: cannot fetch $head of the $name remote $url to look for the marker" >&2
    return 1
  fi
  root=$(git -C "$probe" rev-list --first-parent refs/heads/probe | tail -n 1)
  text=$(git -C "$probe" show "$root:$MARKER_FILE" 2>/dev/null || true)
  rm -rf "$probe"
  if [ "$text" != "$MARKER_TEXT" ]; then
    {
      echo "refused: the $name remote $url has commits, and the first commit of main has no $MARKER_FILE file with the trial's text."
      echo "  The trial deletes every ref of both remotes, so it runs only on an empty repo or on one an earlier run created."
      echo "  Nothing was changed. If this really is a scratch repo for the trial, empty it, or set LORE_TRIAL_ADOPT=$url for one run."
    } >&2
    return 1
  fi
  echo "guard: $name remote $url carries the trial marker; it may be wiped" >&2
}

# wipe_remote NAME URL KIND: replace everything on URL with one fresh first commit.
# KIND client also adds the client's website files. Pushes the marker commit to main
# (the one forced push of the trial), then deletes every other branch, tag and
# refs/lore/* ref. Call only after guard_remote said yes for this URL.
wipe_remote() {
  local name="$1" url="$2" kind="$3" seed refs del=()
  seed=$(mktemp -d)
  git init -q -b main "$seed"
  printf '%s\n' "$MARKER_TEXT" > "$seed/$MARKER_FILE"
  if [ "$kind" = client ]; then
    printf '# Client website\n' > "$seed/README.md"
    printf 'console.log(1)\n' > "$seed/index.js"
  fi
  git -C "$seed" add -A
  git -C "$seed" -c user.name="lore trial" -c user.email=trial@example.invalid commit -q -m "trial: fresh start"
  if ! retry git -C "$seed" push -q "$url" "+main:refs/heads/main"; then
    rm -rf "$seed"
    echo "wipe: could not push the fresh first commit to $url" >&2
    return 1
  fi
  # Every ref but main and HEAD; refs/pull/* are read-only on hosts that make them.
  refs=$(retry git ls-remote "$url" | awk '{ print $2 }' | grep -v -E '^(HEAD|refs/heads/main|refs/pull/.*)$' || true)
  while IFS= read -r r; do [ -n "$r" ] && del+=(":$r"); done <<< "$refs"
  if [ "${#del[@]}" -gt 0 ] && ! retry git -C "$seed" push -q "$url" "${del[@]}"; then
    rm -rf "$seed"
    echo "wipe: could not delete the old refs of $url" >&2
    return 1
  fi
  rm -rf "$seed"
  refs=$(retry git ls-remote "$url" | awk '{ print $2 }' | grep -v -E '^(HEAD|refs/heads/main|refs/pull/.*)$' || true)
  if [ -n "$refs" ]; then
    echo "wipe: $url still has refs after the wipe: $refs" >&2
    return 1
  fi
  echo "wipe: $name remote $url now holds one fresh commit on main (${#del[@]} old ref(s) deleted)" >&2
}

# ---------------------------------------------------------------------------
# Running commands as a person and recording checks
# ---------------------------------------------------------------------------

# home_of WHO: the HOME a person or agent runs with (agents run on dev1's machine).
home_of() { case $1 in agent-*) echo "$T/homes/dev1" ;; *) echo "$T/homes/$1" ;; esac; }

# as WHO DIR CMD: run the shell string CMD as WHO in $T/work/DIR. Sets LAST_OUT and
# LAST_RC and appends the command and its output to the transcript ($LOG).
as() {
  local who="$1" dir="$2" cmd="$3" h envs=()
  h=$(home_of "$who")
  envs=("HOME=$h")
  case $who in
    agent-*) envs+=("LORE_ACTOR=agent:$who" "LORE_SESSION=$(cat "$T/homes/dev1/.sess-$who" 2>/dev/null || echo s-none)") ;;
  esac
  LAST_OUT=$(cd "$T/work/$dir" && env "${envs[@]}" bash -c "$cmd" 2>&1)
  LAST_RC=$?
  {
    printf '\n# %s, in %s\n$ %s\n' "$who" "$dir" "$cmd"
    [ -n "$LAST_OUT" ] && printf '%s\n' "$LAST_OUT"
    printf '(exit %d)\n' "$LAST_RC"
  } >> "$LOG"
}

# check DESC OK: record one check of the current step; OK is 0 (pass) or anything else (fail).
check() {
  local desc="$1" ok="$2"
  STEP_CHECKS=$((STEP_CHECKS + 1))
  if [ "$ok" -eq 0 ]; then
    printf 'ok    %s\n' "$desc" >> "$LOG"
  else
    STEP_FAILS=$((STEP_FAILS + 1))
    printf 'FAIL  %s\n' "$desc" >> "$LOG"
    printf '  FAIL [%s] %s\n' "$STEP" "$desc" >&2
    if [ -n "${LAST_OUT:-}" ]; then printf '%s\n' "$LAST_OUT" | tail -n 8 | sed 's/^/    | /' >&2; fi
  fi
}

# ok WHO DIR CMD: run CMD; it must exit 0.
ok() {
  as "$@"
  check "$1: $3 (exit $LAST_RC, want 0)" "$([ "$LAST_RC" -eq 0 ] && echo 0 || echo 1)"
}

# refused CODE WHO DIR CMD: run CMD; it must fail, with exit CODE (or any non-zero when CODE is "any").
refused() {
  local code="$1"; shift
  as "$@"
  local good=1
  if [ "$code" = any ]; then
    [ "$LAST_RC" -ne 0 ] && good=0
  else
    [ "$LAST_RC" -eq "$code" ] && good=0
  fi
  check "$1: $3 (exit $LAST_RC, want ${code/any/non-zero})" "$good"
}

# has REGEX / lacks REGEX: the last command's output must (not) match REGEX.
has() {
  if grep -q -E -- "$1" <<< "$LAST_OUT"; then check "output has /$1/" 0; else check "output has /$1/" 1; fi
}
lacks() {
  if grep -q -E -- "$1" <<< "$LAST_OUT"; then check "output lacks /$1/" 1; else check "output lacks /$1/" 0; fi
}

# row_id WHO DIR LIST-CMD REGEX PREFIX: the id (PREFIX_…) on the first line of LIST-CMD's output matching REGEX.
row_id() {
  as "$1" "$2" "$3"
  grep -E -- "$4" <<< "$LAST_OUT" | grep -o -E "$5_[0-9a-f]+" | head -n 1
}

# ---------------------------------------------------------------------------
# The client-side leak scan
# ---------------------------------------------------------------------------

# client_scan: from the CLIENT's own clone, fetch every ref the client repo serves and
# look for LEAK_RE in the checkout, every ref's tree, git log --all -p, every object
# in the object store (dangling ones too) and the client's own lore view. Sets
# STEP_LEAK to "clean" or "LEAK" and writes the details to $T/logs/clientcheck.log.
client_scan() {
  local d="$T/work/client/app"
  echo 1 > "$T/logs/.scan"
  (
    hits=0
    printf '\n=== client scan after: %s ===\n' "$STEP"
    cd "$d" || { echo "no client clone"; exit 1; }
    HOME=$T/homes/client retry git fetch -q --prune origin '+refs/*:refs/remotes/all/*' 2>&1
    HOME=$T/homes/client retry git pull -q --ff-only 2>&1 | tail -n 2
    git for-each-ref --format='  %(refname) %(objectname:short)' | grep lore || echo "  (no lore refs)"
    if grep -rIn -E "$LEAK_RE" --exclude-dir=.git . ; then echo "!! marker in the checkout"; hits=1; fi
    for r in $(git for-each-ref --format='%(refname)'); do
      if git grep -I -n -E "$LEAK_RE" "$r" -- . 2>/dev/null | head -n 5 | grep . ; then echo "!! marker in the tree of $r"; hits=1; fi
    done
    if git log --all -p --format='commit %H %s%n%b' | grep -n -E "$LEAK_RE" | head -n 5 | grep . ; then echo "!! marker in git log --all -p"; hits=1; fi
    if git cat-file --batch-all-objects --batch 2>/dev/null | grep -a -q -E "$LEAK_RE"; then echo "!! marker in an object of the client's object store"; hits=1; fi
    if HOME=$T/homes/client bash -c 'lore task list 2>&1; lore memory list 2>&1; lore rule list 2>&1' | grep -n -E "$LEAK_RE"; then echo "!! marker in the client's lore view"; hits=1; fi
    if [ "$hits" = 0 ]; then echo "client scan clean ($STEP)"; else echo "CLIENT SCAN FOUND A MARKER ($STEP)"; fi
    echo "$hits" > "$T/logs/.scan"
  ) >> "$T/logs/clientcheck.log" 2>&1
  if [ "$(cat "$T/logs/.scan" 2>/dev/null)" = 0 ]; then STEP_LEAK=clean; else STEP_LEAK=LEAK; fi
}
