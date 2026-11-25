#!/usr/bin/env bash
# denyhash.sh <file|-> — refuse text holding a denied word (devco D-981).
#
# tests/lib/denyhash.txt lists the sha256 of each word that must never reach
# this public repository (client, company and people names, private hosts),
# lowercased, hashes only. This script tokenizes the file (or stdin with -)
# the way internal/repoguard TestNoDeniedWordsInTrackedFiles does and exits 1
# naming each line that holds a denied word, never the word itself; clean
# text exits 0. The pre-push hook runs it on every commit being pushed, and
# the landing can run it on a diff or a commit message.
#
# A token is a run of letters, digits and _ . @ -, lowercased, leading and
# trailing . @ - trimmed; it is checked whole, as each part split at . @ - _,
# and as each run of adjacent parts split at . and @; two tokens with only
# whitespace between them are also checked as one phrase.
#
# DENYHASH_FILE overrides the list (tests use a made-up one).
#
# Examples:
#   git log -1 --format=%B | bash tests/lib/denyhash.sh -
#   bash tests/lib/denyhash.sh CHANGELOG.md
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
list=${DENYHASH_FILE:-$here/denyhash.txt}
src=${1:?usage: denyhash.sh <file|->}
[ -r "$list" ] || { echo "denyhash: the list $list is missing" >&2; exit 2; }
if [ "$src" != "-" ] && [ ! -r "$src" ]; then
	echo "denyhash: cannot read $src" >&2
	exit 2
fi

# candidates prints "<lowercased spelling>\t<line>" for every spelling checked.
candidates() {
	LC_ALL=C awk '
	function emit(w, n) { if (w != "") print w "\t" n }
	{
		line = $0; pos = 0
		if (line ~ /^[[:space:]]*$/) { next }
		while (match(line, /[A-Za-z0-9_.@-]+/)) {
			gap = substr(line, 1, RSTART - 1)
			if (gap ~ /[^[:space:]]/) prev = ""
			raw = substr(line, RSTART, RLENGTH)
			line = substr(line, RSTART + RLENGTH)
			tok = tolower(raw)
			sub(/^[.@-]+/, "", tok); sub(/[.@-]+$/, "", tok)
			if (tok == "") { prev = ""; continue }
			emit(tok, NR)
			k = split(tok, p, /[.@_-]/)
			for (i = 1; i <= k; i++) emit(p[i], NR)
			m = split(tok, r, /[.@]/); k = 0
			for (i = 1; i <= m; i++) if (r[i] != "") q[++k] = r[i]
			for (i = 1; i <= k; i++) {
				run = q[i]; emit(run, NR)
				for (j = i + 1; j <= k; j++) { run = run "." q[j]; emit(run, NR) }
			}
			if (prev != "") emit(prev " " tok, prevline)
			prev = tok; prevline = NR
		}
		if (line ~ /[^[:space:]]/) prev = ""
	}' "$@"
}

# hashes turns "<spelling>\t<line>" into "<sha256>\t<line>".
hashes() {
	if command -v perl >/dev/null 2>&1 && perl -MDigest::SHA -e1 2>/dev/null; then
		perl -MDigest::SHA=sha256_hex -ne 'chomp; my ($w, $n) = split /\t/; print sha256_hex($w), "\t$n\n"'
	else
		local sum="sha256sum"
		command -v sha256sum >/dev/null 2>&1 || sum="shasum -a 256"
		while IFS=$'\t' read -r w n; do
			printf '%s\t%s\n' "$(printf %s "$w" | $sum | cut -d' ' -f1)" "$n"
		done
	fi
}

if [ "$src" = "-" ]; then
	cands=$(candidates)
else
	cands=$(candidates "$src")
fi
hits=$(printf '%s\n' "$cands" | sed '/^$/d' | sort -u | hashes |
	LC_ALL=C awk -F'\t' 'NR == FNR { if ($0 !~ /^#/ && $0 != "") deny[$1] = 1; next } ($1 in deny) { print $2 }' "$list" - |
	sort -nu)
[ -z "$hits" ] && exit 0
for n in $hits; do
	echo "denyhash: ${src/#-/stdin} line $n: a denied word (its sha256 is in tests/lib/denyhash.txt); replace it with a made-up name"
done
exit 1
