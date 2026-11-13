#!/usr/bin/env bash
# lib.sh: a throwaway world for the docs examples outside docs/team (docs/knowledge,
# docs/tasks, docs/sync, docs/operations, docs/reference).
#
# Everything lives under $WORLD (a fresh temp dir per section): a fake GitHub made of
# bare repos under $WORLD/hosts/github.com/<owner>/<repo>.git, and one HOME per person
# under $HOMES/<name>. Each HOME's ~/.gitconfig rewrites https://github.com/ and
# git@github.com: to the fake host with url.<path>.insteadOf, so lore sees the same
# remote URLs a real user has. Nothing touches the real HOME, the real ~/.lore or the
# network.
#
# This is a copy of tests/team/docs/lib.sh (same helpers, same transcript format), kept
# separate so the two runners can change on their own. Source it; it needs LORE_BIN.

set -euo pipefail

: "${LORE_BIN:?LORE_BIN must point at the lore binary}"

# new_world DIR: start a fresh, empty world in DIR (every section gets its own).
new_world() {
  WORLD="$1"
  HOSTS="$WORLD/hosts/github.com"
  SHIM="$WORLD/shim"
  HOMES="$WORLD/home"  # one HOME per person: $HOMES/<name>
  mkdir -p "$HOSTS" "$SHIM" "$HOMES"
  # Hooks lore installs call plain `lore` from PATH: put the binary under test first.
  ln -sf "$LORE_BIN" "$SHIM/lore"
}

# remote OWNER/REPO: create an empty bare repo on the fake GitHub (default branch main).
remote() {
  git init -q --bare -b main "$HOSTS/$1.git"
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

# TRANSCRIPT is the file `on` appends to; run-examples.sh sets it per section.
TRANSCRIPT="${TRANSCRIPT:-/dev/stdout}"
# FAILURES counts commands that did not behave as documented.
FAILURES=0

# clean: replace machine-specific paths so transcripts read like a real user's terminal.
# The machine's real hostname becomes the fictional "devbox".
clean() {
  # The lore binary under test (lore where names the program itself) reads as an installed lore.
  sed -e "s#$LORE_BIN#/usr/local/bin/lore#g" -e "s#$SHIM/lore#/usr/local/bin/lore#g" -e "s#\b$(hostname)\b#devbox#g" -e 's#\b\(ct_[0-9a-f]\{5\}\)[0-9a-f]\{27\}\b#\1…#g' -e "s#$HOMES/[a-z0-9-]*#~#g" -e "s#$HOSTS#/srv/git/github.com#g" -e "s#$WORLD#/tmp/world#g"
}

# _answer OUT feeds a prompt its answer (ANSWER) once the prompt is on the terminal: it
# waits until the command's output so far (the file OUT, written as the command prints)
# matches PROMPT (a grep -E pattern; default a "[y/N]"-style question), then types the
# answer, so the question always comes before the reply whatever the machine's load. A
# prompt that never shows within ANSWER_TIMEOUT seconds (default 120) is reported in
# OUT.noprompt and answered anyway, so the command still ends; _on counts it as a failure.
# With no ANSWER it sends nothing.
_answer() {
  [ -n "${ANSWER:-}" ] || return 0
  local out="$1" waited=0 limit=$(( ${ANSWER_TIMEOUT:-120} * 10 ))
  until tr -d '\r' < "$out" 2>/dev/null | grep -qE "${PROMPT:-\[[yYnN]/[yYnN]\]}"; do
    if [ "$waited" -ge "$limit" ]; then
      printf 'no prompt matching %s within %ss\n' "${PROMPT:-[y/N]}" "${ANSWER_TIMEOUT:-120}" > "$out.noprompt"
      break
    fi
    sleep 0.1; waited=$((waited + 1))
  done
  printf '%s\n' "$ANSWER"
  sleep 1
}

# _on EXPECT NAME DIR CMD: run CMD (a shell string) as person NAME in DIR (relative to
# their ~), record "NAME (DIR)$ CMD" and its real output in the transcript, and count a
# wrong exit status as a failure. Set TTY=1 to give lore a real terminal (script(1)).
_on() {
  local expect="$1" name="$2" dir="$3" cmd="$4" home="$HOMES/$2" out code
  local label="${AS:-$name}"
  [ "$dir" != "." ] && label="${AS:-$name} ($dir)"
  mkdir -p "$home/$dir"
  local EXTRA_ENV="${EXTRA_ENV:-}"
  if [ -n "${CLOCK:-}" ]; then
    # Test clock (loretest builds only): every command runs STEP seconds after the last.
    CLOCK=$(date -u -d "$CLOCK + ${STEP:-5} seconds" +%Y-%m-%dT%H:%M:%SZ)
    EXTRA_ENV="$EXTRA_ENV LORE_NOW=$CLOCK GIT_AUTHOR_DATE=$CLOCK GIT_COMMITTER_DATE=$CLOCK"
  fi
  set +e
  if [ -n "${TTY:-}" ]; then
    # shellcheck disable=SC2046
    local tty_out; tty_out=$(mktemp)
    out=$(cd "$home/$dir" && _answer "$tty_out" | $(envfor "$name") ${EXTRA_ENV:-} script -qefc "bash -c $(printf '%q' "$cmd")" /dev/null 2>&1 | tee "$tty_out" | tr -d '\r'; exit "${PIPESTATUS[1]}")
    code=$?
    if [ -e "$tty_out.noprompt" ]; then
      FAILURES=$((FAILURES + 1))
      printf 'NO PROMPT (%s): %s (%s) $ %s\n%s\n' "$(cat "$tty_out.noprompt")" "$name" "$dir" "$cmd" "$out" >&2
    fi
    rm -f "$tty_out" "$tty_out.noprompt"
  else
    # shellcheck disable=SC2046
    # stdin is /dev/null: examples are non-interactive by design, so a prompt (the
    # secret menu, a confirm) fails at once instead of waiting on the terminal.
    out=$(cd "$home/$dir" && $(envfor "$name") ${EXTRA_ENV:-} bash -c "$cmd" 2>&1 </dev/null)
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
# on NAME DIR CMD: a command that must succeed.
on()  { _on ok "$@"; }
# on! NAME DIR CMD: a command that must fail; its exit status is printed in the transcript.
on!() { _on fail "$@"; }

# KNOWN lists the known-failing examples seen in this run; they are reported at the end
# and do not fail it until their bug is fixed.
KNOWN=()
# on_known BUG NAME DIR CMD: an example that should succeed but fails today because of a
# reported bug. The failure is printed, never hidden; once it passes, the run says so.
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

# quiet NAME DIR CMD: a setup step, not shown in the transcript; fails the run on error.
quiet() {
  local name="$1" dir="$2" cmd="$3" home="$HOMES/$1"
  mkdir -p "$home/$dir"
  local extra="${EXTRA_ENV:-}"
  [ -n "${CLOCK:-}" ] && extra="$extra LORE_NOW=$CLOCK GIT_AUTHOR_DATE=$CLOCK GIT_COMMITTER_DATE=$CLOCK"
  # shellcheck disable=SC2046
  if ! (cd "$home/$dir" && $(envfor "$name") $extra bash -c "$cmd") > "$WORLD/quiet.log" 2>&1 </dev/null; then
    FAILURES=$((FAILURES + 1))
    echo "SETUP FAILED: $name ($dir) \$ $cmd" >&2
    cat "$WORLD/quiet.log" >&2
    return 1
  fi
}

# id PREFIX: the first row id with PREFIX (tsk, mem, rul, mis…) in the last command's output.
id() {
  local v
  v=$(grep -o "$1_[0-9a-f]\{32\}" <<<"$LAST_OUT" | head -1 || true)
  if [ -z "$v" ]; then
    echo "NO $1_ ID in the last output:" >&2; printf '%s\n' "$LAST_OUT" >&2
    echo "$1_missing"; return 0
  fi
  echo "$v"
}

# solo_repo PERSON OWNER/REPO [DIR]: a repo on the fake GitHub with one commit, cloned into
# PERSON's ~/work/DIR (DIR defaults to the repo name). lore is NOT set up in it yet.
solo_repo() {
  local who="$1" slug="$2" dir="${3:-${2##*/}}"
  remote "$slug"
  [ -d "$HOMES/$who" ] || person "$who" "$who@agency.example"
  quiet "$who" work "git clone -q https://github.com/$slug $dir && cd $dir && echo '# $dir' > README.md && git add . && git commit -qm init && git push -q origin main"
}

# lore_repo PERSON OWNER/REPO [DIR]: like solo_repo, then `lore init` and commit what it wrote.
lore_repo() {
  local who="$1" slug="$2" dir="${3:-${2##*/}}"
  solo_repo "$who" "$slug" "$dir"
  quiet "$who" "work/$dir" "lore init --name=$dir && git add -A && git commit -qm 'lore: init' && git push -q origin main"
}
