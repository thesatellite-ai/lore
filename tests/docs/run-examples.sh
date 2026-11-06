#!/usr/bin/env bash
# run-examples.sh: run the command examples shown in docs/** (everything except
# docs/team, which has its own runner in tests/team/docs) against the real lore binary,
# in throwaway worlds (fake GitHub, one HOME per person), and write what each command
# printed to tests/docs/transcripts/<section>.txt.
#
# One file per section: tests/docs/sections/<name>.sh. Each is sourced inside a fresh
# world with the helpers of lib.sh (on, on!, quiet, note, id, person, solo_repo,
# lore_repo, config). Pages paste their examples from the transcripts (paste.py), so
# every example on a page is a real run.
#
#   tests/docs/run-examples.sh                  # build lore, run every section
#   tests/docs/run-examples.sh start search     # only these sections
#   LORE_BIN=/path/to/lore tests/docs/run-examples.sh start   # use a built binary
#   KEEP=1 tests/docs/run-examples.sh start     # keep the worlds to look around
#
# The binary is built with -tags loretest so LORE_NOW (a fixed clock) works; that tag
# changes nothing else in lore's behaviour or output. Exit 1 when any command that
# should succeed fails, or any command that should fail succeeds.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
OUT="$HERE/transcripts"
SECTIONS="$HERE/sections"
# The worlds live under /tmp, never under $TMPDIR: lore prints real paths (a file:// store url
# under ~) and aligns table columns to them, and clean() shortens the home folder to "~"
# only afterwards, so the padding left in a transcript follows the length of this path. A
# fixed-length root keeps every run (any TMPDIR, any gate step) byte-identical.
SCRATCH="$(mktemp -d "/tmp/lore-docs-core.XXXXXX")"
if [ -n "${KEEP:-}" ]; then echo "keeping worlds in $SCRATCH" >&2; else trap 'rm -rf "$SCRATCH"' EXIT; fi

if [ -z "${LORE_BIN:-}" ]; then
  LORE_BIN="$SCRATCH/bin/lore"
  (cd "$REPO" && CGO_ENABLED=0 go build -tags loretest -o "$LORE_BIN" ./saas/cmd/cli)
fi
export LORE_BIN

# shellcheck source=lib.sh
source "$HERE/lib.sh"

# note TEXT: a heading inside a transcript; a page's paste marker names it (start#init).
note() { printf '\n### %s\n' "$*" >> "$TRANSCRIPT"; }

# config NAME: write ~/.lore/config.yaml for NAME from stdin.
config() {
  mkdir -p "$HOMES/$1/.lore"
  cat > "$HOMES/$1/.lore/config.yaml"
}

# run_section NAME: a fresh world and transcript, then the section file's commands.
# The file is sourced inside this function, so it may use `local`.
run_section() {
  local name="$1" file="$SECTIONS/$1.sh"
  [ -f "$file" ] || { echo "run-examples: no section file $file" >&2; exit 2; }
  mkdir -p "$OUT"
  TRANSCRIPT="$OUT/$name.txt"
  : > "$TRANSCRIPT"
  new_world "$SCRATCH/world-$name"
  # Every section runs on the test clock (LORE_NOW and git dates), so times, ages and
  # commit dates are the same on every run; normalize.py then fixes the random ids.
  CLOCK=2026-11-02T10:00:00Z STEP=5
  echo "== $name" >&2
  # shellcheck disable=SC1090
  source "$file"
  python3 "$HERE/normalize.py" "$TRANSCRIPT"
}

main() {
  local want=("$@")
  if [ ${#want[@]} -eq 0 ]; then
    for f in "$SECTIONS"/*.sh; do want+=("$(basename "$f" .sh)"); done
  fi
  for s in "${want[@]}"; do run_section "$s"; done
  if [ ${#KNOWN[@]} -ne 0 ]; then
    echo "run-examples: ${#KNOWN[@]} known failure(s), each a reported lore bug (see the KNOWN FAILURE lines above):" >&2
    printf '  %s\n' "${KNOWN[@]}" >&2
  fi
  if [ "$FAILURES" -ne 0 ]; then
    echo "run-examples: $FAILURES example command(s) did not behave as documented" >&2
    exit 1
  fi
  echo "run-examples: every example ran as documented; transcripts in ${OUT#"$REPO"/}" >&2
}

main "$@"
