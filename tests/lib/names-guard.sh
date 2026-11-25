#!/usr/bin/env bash
# names-guard.sh — refuse listed words (client, company and people names,
# private hosts) in this public repository (devco D-981, D-982).
#
# The word list is private and never part of the repository: nothing derived
# from it (hashes, lengths, counts) is committed, since a hash of a short name
# is reversed in seconds. Each maintainer keeps it on their own machine, read
# from $NAMES_GUARD_FILE, else ~/.config/names-guard/terms.txt (mode 0600):
# one term per line, '#' starts a comment line, a term may hold . @ - and
# spaces (an email, a domain, a two-word name). When no list exists (GitHub
# Actions, another contributor) the guard prints one line and passes.
#
# A hit is a whole-word, case-insensitive match: letters, digits and _ are
# word characters, so "acme" hits "Acme," "acme-web" and "x@acme.io" but not
# "acmes" or "acme_app". The report names where (file:line, a commit), never
# the word. Terms in tests/lib/names-guard-allow.txt ($NAMES_GUARD_ALLOW
# overrides it) are dropped from the list: generic words that may be on
# someone's list but are fine here.
#
# Usage (exit 0 clean or no list, 1 on a hit, 2 on a usage error):
#   bash tests/lib/names-guard.sh --files              # tracked text files and their paths
#   bash tests/lib/names-guard.sh --messages <revs>    # author, committer, message of each commit
#   bash tests/lib/names-guard.sh --added <revs>       # lines each commit adds
#   bash tests/lib/names-guard.sh --stdin              # any text, e.g. a diff or a draft message
# <revs> is what `git rev-list` takes: origin/dev..HEAD, or HEAD --not --remotes.
set -euo pipefail
export LC_ALL=C # ASCII word characters and case folding on every machine

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
list=${NAMES_GUARD_FILE:-${HOME:-}/.config/names-guard/terms.txt}
allow=${NAMES_GUARD_ALLOW:-$here/names-guard-allow.txt}
mode=${1:-}
[ $# -gt 0 ] && shift
case "$mode" in
--files | --stdin) ;;
--messages | --added) [ $# -gt 0 ] || { echo "names-guard: $mode needs revisions, e.g. $mode origin/dev..HEAD" >&2; exit 2; } ;;
*)
	echo "usage: names-guard.sh --files | --messages <revs> | --added <revs> | --stdin" >&2
	exit 2
	;;
esac

if [ ! -r "$list" ]; then
	echo "names-guard: no list at $list, skipped"
	exit 0
fi

# terms: the list's terms, lowercased and trimmed, minus comments, blanks and
# allowed words. Written to a private temp file that grep -f reads (an empty
# pattern line would match every line, so blanks never reach it).
work=$(mktemp -d "${TMPDIR:-/tmp}/names-guard.XXXXXX")
trap 'rm -rf "$work"' EXIT
chmod 700 "$work"
norm() { tr -d '\r' <"$1" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e '/^#/d' -e '/^$/d' | tr '[:upper:]' '[:lower:]' | sort -u; }
norm "$list" >"$work/list"
if [ -r "$allow" ]; then norm "$allow" >"$work/allow"; else : >"$work/allow"; fi
comm -23 "$work/list" "$work/allow" >"$work/terms"
[ -s "$work/terms" ] || exit 0

# has_term: stdin holds a listed term; lines prints the numbers of the lines
# that do. Neither prints the matched text. has_term reads all its input (no
# -q): stopping early would kill the writer with SIGPIPE, and pipefail would
# then report a hit as clean.
has_term() { grep -i -w -F -f "$work/terms" >/dev/null; }
lines() { grep -n -i -w -F -f "$work/terms" | cut -d: -f1; }

found=0
case "$mode" in
--files)
	top=$(git rev-parse --show-toplevel)
	cd "$top"
	# -I skips binary files (a NUL byte, as git decides), -l names the files.
	while IFS= read -r -d '' f; do
		for n in $(lines <"$f"); do
			echo "names-guard: $f:$n: a listed word; replace it with a made-up name (acme, alice, example.com)"
			found=1
		done
	done < <(git grep -l -z -i -w -F -I -f "$work/terms" || true)
	# A path holding a term would print the word if named, so it is counted
	# by its place in `git ls-files`.
	for n in $(git ls-files | lines || true); do
		echo "names-guard: tracked path number $n of \`git ls-files\` holds a listed word in its name; rename it"
		found=1
	done
	;;
--messages)
	for c in $(git rev-list "$@"); do
		if git log -1 --format='%an <%ae>%n%cn <%ce>%n%B' "$c" | has_term; then
			echo "names-guard: commit $c: a listed word in its author, committer or message; reword or amend it"
			found=1
		fi
	done
	;;
--added)
	for c in $(git rev-list "$@"); do
		if git show --format= --no-color --unified=0 "$c" | grep '^+' | grep -v '^+++ ' | has_term; then
			echo "names-guard: commit $c: a listed word in a line it adds; replace it with a made-up name"
			found=1
		fi
	done
	;;
--stdin)
	for n in $(lines || true); do
		echo "names-guard: stdin line $n: a listed word; replace it with a made-up name"
		found=1
	done
	;;
esac
exit "$found"
