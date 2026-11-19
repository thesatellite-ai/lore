package wire

// leak_addedlines.go — the commit leak check reads only what a commit adds
// (devco:D-720). The check used to read every staged file whole, so any
// change to an already-committed CLAUDE.md in a client repo was refused:
// its old hand-written text matched notes that had since moved into the
// store, though the commit added none of it.
//
//	staged files (stagedFiles) ─▶ git diff --cached -U0 against the same
//	   history bases ─▶ per file, the line numbers the commit adds
//	─▶ addedLinesOnly: every other line blanked (line numbers kept, so a
//	   finding still names file:line); lore's own files (.lore/…) and any
//	   file the diff does not describe (binary, quoted path) stay whole
//	─▶ LeakCheckDocs (F16) over the narrowed docs
//
// The secret check still reads the whole staged file: it has its own
// history rule (hooksecrets_history.go) and must see a value split across
// an old and a new line.

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// stagedAddedLines is, per staged path, the 1-based line numbers of the
// staged file the commit adds or changes, against the same history bases
// stagedFiles reads (an amend counts the lines of the commit it
// replaces). A path missing from the map was not described by the diff.
func stagedAddedLines(ctx context.Context, root string) (map[string]map[int]bool, error) {
	top := gitTop(ctx, root)
	if top == "" {
		return nil, nil
	}
	env := hookIndexEnv(top)
	against := historyBases(ctx, top)
	if len(against) == 0 || (len(against) == 1 && against[0] == "HEAD") {
		against = []string{""}
	}
	added := map[string]map[int]bool{}
	for _, base := range against {
		args := []string{"-c", "core.quotePath=false", "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--diff-filter=ACMR"}
		if base != "" {
			args = append(args, base)
		}
		out, err := gitRun{dir: top, env: env, args: args}.output(ctx)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "list the lines the commit adds", err)
		}
		for p, lines := range parseAddedLines(string(out)) {
			if added[p] == nil {
				added[p] = map[int]bool{}
			}
			for n := range lines {
				added[p][n] = true
			}
		}
	}
	return added, nil
}

// parseAddedLines reads a `git diff -U0` patch: per new-side path (the
// "+++ b/<path>" line), the line numbers of its "@@ … +start[,count] @@"
// hunks. A file with a header but no hunk (binary, mode change only) is
// left out, so its staged content stays whole.
func parseAddedLines(patch string) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	cur := ""
	for _, ln := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			cur = ""
		case strings.HasPrefix(ln, "+++ "):
			cur = ""
			if p, ok := strings.CutPrefix(ln, "+++ b/"); ok {
				cur = p
			}
		case cur != "" && strings.HasPrefix(ln, "@@ "):
			start, count, ok := hunkNewRange(ln)
			if !ok {
				continue
			}
			if out[cur] == nil {
				out[cur] = map[int]bool{}
			}
			for n := start; n < start+count; n++ {
				out[cur][n] = true
			}
		}
	}
	return out
}

// hunkNewRange is the new-side start and count of a hunk header
// "@@ -a[,b] +c[,d] @@" (d defaults to 1; 0 means the hunk only removes).
func hunkNewRange(h string) (start, count int, ok bool) {
	fields := strings.Fields(h)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	s, c, hasCount := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
	start, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, false
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(c); err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// addedLinesOnly is docs with every line the commit does not add blanked
// (an empty line in its place, so line numbers stay true). lore's own
// files (.lore/…: row files, _meta.json, which the rules read as whole
// JSON documents), the commit message and paths the diff did not describe
// are kept whole: the check fails closed.
func addedLinesOnly(docs map[string][]byte, added map[string]map[int]bool) map[string][]byte {
	if added == nil {
		return docs
	}
	out := make(map[string][]byte, len(docs))
	for p, b := range docs {
		lines, ok := added[p]
		if !ok || p == checkWhereCommitMsg || loreOwnPath(p) {
			out[p] = b
			continue
		}
		src := strings.Split(string(b), "\n")
		for i := range src {
			if !lines[i+1] {
				src[i] = ""
			}
		}
		out[p] = []byte(strings.Join(src, "\n"))
	}
	return out
}

// loreOwnPath reports whether p is inside a .lore folder (any project
// folder of a monorepo).
func loreOwnPath(p string) bool {
	return strings.HasPrefix(p, rowfs.LoreDir+"/") || strings.Contains(p, "/"+rowfs.LoreDir+"/")
}
