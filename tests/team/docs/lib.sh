#!/usr/bin/env bash
# lib.sh: a throwaway "team world" for the docs examples (docs/team/**).
#
# Everything lives under $WORLD (a fresh temp dir): a fake GitHub made of bare
# repos under $WORLD/hosts/github.com/<owner>/<repo>.git, and one HOME per
# person under $HOMES/<name>. Each HOME's ~/.gitconfig rewrites
# https://github.com/ and git@github.com: to the fake host with
# url.<path>.insteadOf, so lore sees the same remote URLs a real user has.
# Nothing touches the real HOME, the real ~/.lore or the network.
#
# Source it; it needs LORE_BIN (the lore binary under test) and WORLD.

set -euo pipefail

: "${LORE_BIN:?LORE_BIN must point at the lore binary}"

# new_world DIR: start a fresh, empty world in DIR (every example page gets its own).
new_world() {
  WORLD="$1"
  HOSTS="$WORLD/hosts/github.com"
  SHIM="$WORLD/shim"
  HOMES="$WORLD/home"  # one HOME per person: $HOMES/<name>
  mkdir -p "$HOSTS" "$SHIM" "$HOMES"
  # Hooks lore installs call plain `lore` from PATH: put the binary under test first.
  ln -sf "$LORE_BIN" "$SHIM/lore"
}
if [ -n "${WORLD:-}" ]; then new_world "$WORLD"; fi

# remote OWNER/REPO: create an empty bare repo on the fake GitHub (default branch main).
remote() {
  local slug="$1"
  git init -q --bare -b main "$HOSTS/$slug.git"
}

# protect OWNER/REPO: refuse direct pushes to main the way GitHub does (GH006),
# unless ADMIN_PUSH=1 is set (the merge button).
protect() {
  local hook="$HOSTS/$1.git/hooks/pre-receive"
  cat > "$hook" <<'HOOK'
#!/bin/sh
if [ -n "$ADMIN_PUSH" ]; then exit 0; fi
while read old new ref; do
  if [ "$ref" = "refs/heads/main" ]; then
    echo "error: GH006: Protected branch update failed for refs/heads/main." >&2
    echo "error: Changes must be made through a pull request." >&2
    exit 1
  fi
done
HOOK
  chmod +x "$hook"
}

# person NAME EMAIL: a machine (HOME) for one person, with git identity and the fake GitHub.
person() {
  local name="$1" email="$2" home="$HOMES/$1"
  mkdir -p "$home/work"
  cat > "$home/.gitconfig" <<CFG
[user]
	name = $name
	email = $email
[init]
	defaultBranch = main
[url "$HOSTS/"]
	insteadOf = https://github.com/
	insteadOf = git@github.com:
[advice]
	detachedHead = false
CFG
}

# envfor NAME: the environment one person's commands run with (no LORE_*/GIT_* leaks from the caller).
# LORE_HUMAN=1: the person is a human at a terminal, so a run started from a coding-agent session
# does not attribute every write to "<person> via <tool>" (agent detection walks the parent processes);
# an agent example sets LORE_ACTOR, which wins over it.
# LORE_NO_DETECT=1 (loretest builds only): agent detection sees nothing, so whoami lines do not depend on where the run starts.
envfor() {
  local home="$HOMES/$1"
  echo env -i "HOME=$home" "PATH=$SHIM:/usr/local/bin:/usr/bin:/bin" "USER=$1" "TERM=dumb" "NO_COLOR=1" \
    "GIT_CONFIG_NOSYSTEM=1" "GIT_TERMINAL_PROMPT=0" "LANG=C.UTF-8" "LORE_HUMAN=1" "LORE_NO_DETECT=1"
  # The gate's Go runtime settings for lore processes (LORE_E2E_GOENV, Taskfile.lore.yml; the
  # names tests/e2eenv allows): they change what a lore process costs, never what it prints.
  local kv
  for kv in ${LORE_E2E_GOENV:-}; do
    case "$kv" in GOMAXPROCS=?* | GOGC=?* | GOMEMLIMIT=?*) echo "$kv" ;; esac
  done
  return 0
}

# TRANSCRIPT is the file `on` appends to; examples set it per page section.
TRANSCRIPT="${TRANSCRIPT:-/dev/stdout}"
# FAILURES counts commands that failed when they were expected to succeed.
FAILURES=0

# clean TEXT: replace machine-specific paths so transcripts read like a real user's terminal.
# The machine's real hostname (lore records it in claims) becomes the fictional "devbox".
# Claim tokens are cut to the prefix lore itself prints (ct_7e165…): a full token in a
# committed file looks like a credential to secret scanners, and readers never need it.
# An invite line (lore-invite:v1:<base64 JSON>) is cut after 24 characters: its JSON holds the
# random invite id, so the full line would differ on every run.
clean() {
  sed -e "s#\b$(hostname)\b#devbox#g" -e 's#\b\(ct_[0-9a-f]\{5\}\)[0-9a-f]\{27\}\b#\1…#g' -e 's#\(lore-invite:v1:[A-Za-z0-9_-]\{24\}\)[A-Za-z0-9_=-]*#\1…#g' -e "s#$HOMES/[a-z0-9-]*#~#g" -e "s#$HOSTS#/srv/git/github.com#g" -e "s#$WORLD#/tmp/world#g"
}

# _answer feeds a prompt its answer (ANSWER) after a pause, so the terminal shows the
# question before the reply, as it does for a person; with no ANSWER it sends nothing.
_answer() {
  if [ -n "${ANSWER:-}" ]; then sleep 2; printf '%s\n' "$ANSWER"; sleep 1; fi
}

# on NAME DIR CMD: run CMD (a shell string) as person NAME in DIR (relative to their ~),
# print it as "NAME (DIR)$ CMD" with its real output, and count a non-zero exit as a failure.
# Use `on!` for a command that is expected to fail: its exit code is printed instead.
_on() {
  local expect="$1" name="$2" dir="$3" cmd="$4" home="$HOMES/$2" out code
  local label="${AS:-$name}"
  [ "$dir" != "." ] && label="${AS:-$name} ($dir)"
  mkdir -p "$home/$dir"
  local EXTRA_ENV="${EXTRA_ENV:-}"
  if [ -n "${CLOCK:-}" ]; then
    # Test clock (loretest builds only): every command runs STEP seconds after the last
    # (5 s by default, like a person typing; a section that needs lore's 60 s fetch cache
    # to expire between steps, such as the agents example, sets a larger STEP).
    CLOCK=$(date -u -d "$CLOCK + ${STEP:-5} seconds" +%Y-%m-%dT%H:%M:%SZ)
    # git commits made in the same step carry the same time, so merge times match lore's clock.
    EXTRA_ENV="$EXTRA_ENV LORE_NOW=$CLOCK GIT_AUTHOR_DATE=$CLOCK GIT_COMMITTER_DATE=$CLOCK"
  fi
  set +e
  if [ -n "${TTY:-}" ]; then
    # A real terminal (script(1) gives lore a TTY): unredacted output and prompts,
    # answered from ANSWER (e.g. ANSWER=y). Carriage returns are dropped.
    # shellcheck disable=SC2046
    out=$(cd "$home/$dir" && _answer | $(envfor "$name") ${EXTRA_ENV:-} script -qefc "bash -c $(printf '%q' "$cmd")" /dev/null 2>&1 | tr -d '\r'; exit "${PIPESTATUS[1]}")
    code=$?
  else
    # shellcheck disable=SC2046
    out=$(cd "$home/$dir" && $(envfor "$name") ${EXTRA_ENV:-} bash -c "$cmd" 2>&1)
    code=$?
  fi
  set -e
  LAST_OUT="$out"
  {
    printf '%s$ %s\n' "$label" "$cmd"
    [ -n "$out" ] && printf '%s\n' "$out" | clean
    if [ "$expect" = fail ]; then printf '(exit %d)\n' "$code"; fi
  } >> "$TRANSCRIPT"
  if [ "$expect" = ok ] && [ "$code" -ne 0 ]; then
    FAILURES=$((FAILURES + 1))
    printf 'FAILED (exit %d): %s (%s) $ %s\n%s\n' "$code" "$name" "$dir" "$cmd" "$out" >&2
  fi
  if [ "$expect" = fail ] && [ "$code" -eq 0 ]; then
    FAILURES=$((FAILURES + 1))
    printf 'UNEXPECTED SUCCESS: %s (%s) $ %s\n%s\n' "$name" "$dir" "$cmd" "$out" >&2
  fi
}
on()  { _on ok "$@"; }
# KNOWN lists the known-failing examples seen in this run ("BUG-D1 …"); they are
# reported at the end of every run and do not fail it until their bug is fixed.
KNOWN=()
# on_known BUG NAME DIR CMD: an example that should succeed but fails today because of
# a reported bug. The failure is printed (never hidden); when the command starts to
# succeed, the run says so, so the mark is removed as soon as the bug is fixed.
on_known() {
  local bug="$1"; shift
  local before=$FAILURES
  _on ok "$@" 2>/dev/null || true
  if [ "$FAILURES" -gt "$before" ]; then
    FAILURES=$before
    KNOWN+=("$bug: $1 ($2) \$ $3")
    printf 'KNOWN FAILURE (%s): %s (%s) $ %s\n%s\n' "$bug" "$1" "$2" "$3" "$LAST_OUT" >&2
  else
    printf 'KNOWN FAILURE NOW PASSES (%s): remove on_known from this example: %s\n' "$bug" "$3" >&2
  fi
}
on!() { _on fail "$@"; }
# on_known_fail BUG NAME DIR CMD: an example that should FAIL (a refusal) but succeeds today
# because of a reported bug; reported like on_known, and flagged once it refuses again.
on_known_fail() {
  local bug="$1"; shift
  local before=$FAILURES
  _on fail "$@" 2>/dev/null || true
  if [ "$FAILURES" -gt "$before" ]; then
    FAILURES=$before
    KNOWN+=("$bug: $1 ($2) \$ $3")
    printf 'KNOWN FAILURE (%s, expected a refusal): %s (%s) $ %s\n%s\n' "$bug" "$1" "$2" "$3" "$LAST_OUT" >&2
  else
    printf 'KNOWN FAILURE NOW PASSES (%s): remove on_known_fail from this example: %s\n' "$bug" "$3" >&2
  fi
}

# quiet NAME DIR CMD: setup step, not shown in transcripts; fails the run on error.
quiet() {
  local name="$1" dir="$2" cmd="$3" home="$HOMES/$1"
  mkdir -p "$home/$dir"
  local extra="${EXTRA_ENV:-}"
  # Setup steps share the examples' clock (without moving it), so rows they create
  # never look newer than the examples that follow.
  [ -n "${CLOCK:-}" ] && extra="$extra LORE_NOW=$CLOCK GIT_AUTHOR_DATE=$CLOCK GIT_COMMITTER_DATE=$CLOCK"
  # shellcheck disable=SC2046
  if ! (cd "$home/$dir" && $(envfor "$name") $extra bash -c "$cmd") > "$WORLD/quiet.log" 2>&1; then
    FAILURES=$((FAILURES + 1))
    echo "SETUP FAILED: $name ($dir) \$ $cmd" >&2
    cat "$WORLD/quiet.log" >&2
    return 1
  fi
}

# id PREFIX: the first row id with PREFIX (tsk, mem, rul, mis…) in the last command's output.
# A missing id is a failed example (the command did not print what the docs show).
id() {
  local v
  v=$(grep -o "$1_[0-9a-f]\{32\}" <<<"$LAST_OUT" | head -1 || true)
  if [ -z "$v" ]; then
    echo "NO $1_ ID in the last output:" >&2; printf '%s\n' "$LAST_OUT" >&2
    echo "$1_missing"; return 0
  fi
  echo "$v"
}
