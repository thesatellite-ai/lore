#!/usr/bin/env bash
# e2e-part.sh N I DIR [FILTER] — a go test -run pattern for part I (0-based)
# of N of the end-to-end suite in DIR: its top-level Test functions (read
# from the source, so nothing is built), those matching the extended regex
# FILTER when given, sorted and dealt in turn. A test in a file another
# system's build tags leave out is named but matches nothing there; a part
# with no tests matches nothing. Run from the repo root.
set -euo pipefail
n=$1 i=$2 dir=$3 filter=${4:-.}
names=$(grep -ho '^func Test[A-Za-z0-9_]*([A-Za-z_][A-Za-z0-9_]* \*testing\.T)' "$dir"/*_test.go |
	sed 's/^func \(Test[A-Za-z0-9_]*\)(.*/\1/' | grep -E "$filter" | sort -u |
	awk -v n="$n" -v i="$i" '(NR - 1) % n == i' | paste -sd'|' -)
printf '^(%s)$\n' "${names:-NoTestInThisPart}"
