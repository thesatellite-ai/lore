package wire

// check_ci_secrets.go — the secret half of `lore check leaks --ci` (devco
// R-12): the CI backstop scans what the checkout carries, not only ids and
// structure.
//
//	base: ciScanBase (check_ci_range.go, devco D-330): --since, else the
//	      CI's push range, else the merge-base with origin/HEAD, else
//	      HEAD~50; none (every tracked file) when the clone has less
//	files: those the range base..HEAD adds or changes (every tracked file
//	      when there is no base), as checked out
//	  ─▶ content: the hook's scanner (built-in rules + the repo's
//	     .gitleaks.toml; no store, so no team rules) and the committed
//	     .lore/data/secret_allows entries (scanDocs: a row file field by
//	     field, any other file as text)
//	  ─▶ a value the same row or path already held at the base is not new
//	     (dropHistoryHits, the rule the commit and push hooks apply)
//	  ─▶ names: a file named after a key is a leak too
//	  ─▶ E_LEAK_BLOCKED naming file, line and rule with the match hash and
//	     the `lore secrets allow` line; never the value, and a file name
//	     that holds one is printed with the match replaced by its hash
//
// Why: `git commit --no-verify` / `git push --no-verify` skip every local
// check, and the docs name `lore check leaks --ci` as the backstop, yet it
// checked ids and structure only, so a key pushed past the hooks passed CI.
// The range follows the hooks' own history rule, so a value a repo already
// held before lore (let through by the hooks as not new) does not fail
// every CI run; with no base the whole tree is scanned (fail closed).

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
)

// ciWhere names the CI check in its refusal and in the allow hint.
const ciWhere = "lore check leaks --ci"

// ciNameHit is a secret found in a tracked file's name.
type ciNameHit struct {
	path string
	f    scan.Finding
}

// checkCISecrets scans the files of the checkout at root the range since
// base adds or changes (content and name) with the secret rules; nil when
// nothing is found or root is not a git checkout. since is --since, getenv
// the CI environment (ciScanBase). It needs no store and opens no App.
func checkCISecrets(ctx context.Context, root, since string, getenv func(string) string) error {
	top := gitTop(ctx, root)
	if top == "" {
		return nil
	}
	sc, err := newScanner(ctx, nil, withRepoGitleaks(top))
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, "cannot check for secrets: the scanner could not be built: "+err.Error(), err)
	}
	allows, err := committedAllows(ctx, top)
	if err != nil {
		return err
	}
	items, err := trackedItems(ctx, top)
	if err != nil {
		return err
	}
	base, err := ciScanBase(ctx, top, since, getenv)
	if err != nil {
		return err
	}
	changed, err := ciChanged(ctx, top, base)
	if err != nil {
		return err
	}
	docs := make(map[string][]byte, len(items))
	paths := make([]string, 0, len(items))
	for _, it := range items {
		if changed != nil && !changed[it.Path] {
			continue
		}
		docs[it.Path] = it.Content
		paths = append(paths, it.Path)
	}
	sort.Strings(paths)
	hits := scanDocs(sc, allows, paths, docs, false)
	if base != "" {
		hits, _ = dropHistoryHits(ctx, sc, top, false, paths, docs, hits, []string{base})
	}
	var names []ciNameHit
	for _, p := range paths {
		for _, f := range sc.Scan("", p) {
			names = append(names, ciNameHit{path: p, f: f})
		}
	}
	return ciSecretRefusal(paths, hits, names)
}

// ciChanged is the set of files base..HEAD adds or changes; nil (every
// file) when base is "".
func ciChanged(ctx context.Context, top, base string) (map[string]bool, error) {
	if base == "" {
		return nil, nil
	}
	out, err := liveGit(ctx, top, "diff", "--name-only", "-z", "--no-renames", "--diff-filter=ACMR", base, "HEAD")
	if err != nil {
		return nil, leakCannotCheckErr("the files the pushed commits change", err)
	}
	set := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	return set, nil
}

// ciSecretRefusal is the E_LEAK_BLOCKED for hits (per path, in paths
// order) and names, or nil when both are empty.
func ciSecretRefusal(paths []string, hits [][]leakcheck.SecretHit, names []ciNameHit) error {
	masks := map[string]string{} // a file name holding a secret → as printed
	for _, n := range names {
		masks[n.path] = maskedName(n.path, names)
	}
	shown := func(p string) string {
		if m, ok := masks[p]; ok {
			return m
		}
		return p
	}
	var lines []string
	count, unnamed := 0, false
	for i, p := range paths {
		for _, h := range hits[i] {
			count++
			at := fmt.Sprintf("%s:%d", shown(p), h.Line)
			if h.Field != "" {
				at += " (field " + h.Field + ")"
			}
			lines = append(lines, fmt.Sprintf("  %-40s secret: rule %s match %s", at, h.Rule, h.Preview))
			if _, named := masks[p]; named {
				continue // its allow line would repeat the name; the name is renamed first
			}
			if l := allowLineOf(p, h, false); l != "" {
				lines = append(lines, "      not a secret? "+l)
			} else {
				unnamed = true
			}
		}
	}
	for _, n := range names {
		count++
		lines = append(lines, fmt.Sprintf("  %-40s secret in the file name: rule %s match %s", shown(n.path), n.f.RuleID, n.f.Preview),
			"      fix: rename the file (git mv) so its name holds no key, and rotate the key")
	}
	if count == 0 {
		return nil
	}
	hint := "a commit or push skipped lore's hooks (--no-verify): remove each value from the files, rotate the credential, and commit and push the change; if a finding is not a secret, run the `not a secret?` line printed under it with your reason, then commit and push the new .lore/data/secret_allows file"
	if unnamed {
		hint += " (" + leakcheck.AllowRowTemplate() + ")"
	}
	return errs.New(errs.LeakBlocked, fmt.Sprintf("%s: the checkout carries %d secret(s):\n%s", ciWhere, count, strings.Join(lines, "\n"))).WithHint(hint)
}

// maskedName is p with every match the name findings found in it replaced
// by its hash preview, so the refusal never prints the value.
func maskedName(p string, names []ciNameHit) string {
	type span struct {
		lo, hi  int
		preview string
	}
	var spans []span
	for _, n := range names {
		if n.path == p && n.f.Offset >= 0 && n.f.Offset+n.f.Length <= len(p) && n.f.Length > 0 {
			spans = append(spans, span{n.f.Offset, n.f.Offset + n.f.Length, n.f.Preview})
		}
	}
	if len(spans) == 0 {
		return "<file name withheld>"
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	var b strings.Builder
	at := 0
	for _, s := range spans {
		if s.lo < at {
			continue // overlaps the previous match, already masked
		}
		b.WriteString(p[at:s.lo])
		b.WriteString("<" + s.preview + ">")
		at = s.hi
	}
	b.WriteString(p[at:])
	return b.String()
}
