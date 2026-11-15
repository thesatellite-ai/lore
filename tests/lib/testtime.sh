#!/usr/bin/env bash
# testtime.sh: scale a test time limit by this machine's factor (internal/testtime is the Go side).
#
#   tests/lib/testtime.sh 40m      prints the scaled limit in seconds, e.g. "2400s" (factor 1) or "17280s"
#   tests/lib/testtime.sh --factor prints the factor itself, e.g. "1" or "7.2"
#
# Why: every go test -timeout of the Taskfiles and gate steps was tuned on a 48-core Linux server;
# a 10-core Mac runs each step 3-5x slower and the steps died at their limits with nothing wrong.
# The factor is LORE_TEST_TIME_SCALE when set (a number >= 1), else max(1, 48/cores) capped at 6,
# times 1.5 on macOS, so it is 1 on the gate server and its limits are unchanged there.
# Only limits on a whole run go through it, never a timeout that is the behaviour under test.
# Input: a Go-style duration made of h, m and s parts (90s, 15m, 1h30m) or plain seconds.
# A bad LORE_TEST_TIME_SCALE or duration exits 2 naming it (the -timeout then fails loudly).
# LORE_TEST_TIME_NPROC and LORE_TEST_TIME_OS replace the core count and OS; only the agreement
# test (internal/testtime) sets them.
set -euo pipefail

usage() {
  echo "usage: $0 <duration, e.g. 40m> | --factor" >&2
  exit 2
}
[ "$#" -eq 1 ] || usage

factor() {
  local env="${LORE_TEST_TIME_SCALE:-}"
  if [ -n "$env" ]; then
    if ! awk -v v="$env" 'BEGIN { exit !(v ~ /^[0-9]+(\.[0-9]+)?$/ && v + 0 >= 1) }'; then
      echo "testtime: LORE_TEST_TIME_SCALE=\"$env\": want a number >= 1 (for example LORE_TEST_TIME_SCALE=4), or unset it to compute it from the machine" >&2
      exit 2
    fi
    awk -v v="$env" 'BEGIN { printf "%g\n", v + 0 }'
    return
  fi
  local n os
  n="${LORE_TEST_TIME_NPROC:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 48)}"
  os="${LORE_TEST_TIME_OS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
  awk -v n="$n" -v os="$os" 'BEGIN {
    f = 1
    if (n > 0 && n < 48) f = 48 / n
    if (f > 6) f = 6
    if (os == "darwin") f *= 1.5
    printf "%g\n", f
  }'
}

f="$(factor)"
if [ "$1" = "--factor" ]; then
  echo "$f"
  exit 0
fi
case "$1" in -*) usage ;; esac

# Duration to seconds: plain seconds, or h/m/s parts in that order.
secs="$(awk -v d="$1" 'BEGIN {
  if (d ~ /^[0-9]+$/) { print d; exit }
  if (d !~ /^([0-9]+h)?([0-9]+m)?([0-9]+s)?$/ || d == "") { exit 1 }
  t = 0; rest = d
  while (match(rest, /^[0-9]+[hms]/)) {
    part = substr(rest, 1, RLENGTH); unit = substr(part, RLENGTH, 1); v = substr(part, 1, RLENGTH - 1) + 0
    t += (unit == "h") ? v * 3600 : (unit == "m") ? v * 60 : v
    rest = substr(rest, RLENGTH + 1)
  }
  print t
}')" || { echo "testtime: \"$1\" is not a duration like 90s, 15m or 1h30m" >&2; exit 2; }

awk -v s="$secs" -v f="$f" 'BEGIN { printf "%ds\n", int(s * f + 0.5) }'
