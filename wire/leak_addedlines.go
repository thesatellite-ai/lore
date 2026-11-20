package wire

// leak_addedlines.go — the commit and push leak checks read only what a
// commit adds (devco:D-720 commit, devco:D-728 push). The check used to read every staged file whole, so any
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
//	pre-push: the commits the push sends (wire/host live_prepush.go)
//	   ─▶ git diff-tree --stdin -U0 per commit and parent ─▶ PushAddedLines
//	   (per blob id) ─▶ per doc set, path → lines ─▶ WithAddedLines ─▶
//	   LeakCheckDocs narrows with the same addedLinesOnly
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
			addLines(added, p, lines)
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
	for _, f := range parseAddedFiles(patch) {
		addLines(out, f.path, f.lines)
	}
	return out
}

// addedFile is one file of a `git diff -U0` patch: its new-side path, its
// new blob id (the "index <old>..<new>" line; abbreviated unless the diff
// ran with --full-index) and the new-side line numbers its hunks add.
type addedFile struct {
	path, blob string
	lines      map[int]bool
}

// parseAddedFiles is the one hunk reader of both leak hooks: the commit
// check keys its result by path (parseAddedLines), the push check by blob
// id (PushAddedLines). A file with no hunk is left out (kept whole).
func parseAddedFiles(patch string) []addedFile {
	var out []addedFile
	var cur *addedFile
	blob := ""
	for _, ln := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			cur, blob = nil, ""
		case strings.HasPrefix(ln, "index "):
			if f := strings.Fields(ln); len(f) > 1 {
				_, blob, _ = strings.Cut(f[1], "..")
			}
		case strings.HasPrefix(ln, "+++ "):
			cur = nil
			if p, ok := strings.CutPrefix(ln, "+++ b/"); ok {
				out = append(out, addedFile{path: p, blob: blob, lines: map[int]bool{}})
				cur = &out[len(out)-1]
			}
		case cur != nil && strings.HasPrefix(ln, "@@ "):
			start, count, ok := hunkNewRange(ln)
			if !ok {
				continue
			}
			for n := start; n < start+count; n++ {
				cur.lines[n] = true
			}
		}
	}
	kept := out[:0]
	for _, f := range out {
		if len(f.lines) > 0 {
			kept = append(kept, f)
		}
	}
	return kept
}

// addLines merges lines into m[key].
func addLines(m map[string]map[int]bool, key string, lines map[int]bool) {
	if m[key] == nil {
		m[key] = map[int]bool{}
	}
	for n := range lines {
		m[key][n] = true
	}
}

// PushAddedLines reads the output of
//
//	git rev-list --parents <what the push sends> |
//	git diff-tree --stdin -p -U0 --full-index -m --root -r --no-renames --diff-filter=ACMR
//
// (parents: each pushed commit's parent count) and returns, per new blob
// id, the line numbers the pushed commits add to it (devco:D-728: the
// pre-push F16 check read every pushed file version whole, so a push was
// refused for lines committed and pushed long ago). diff-tree prints the
// commit id on a line of its own before each parent's diff. A merge adds
// only the lines that are new against every parent (a line one parent
// already had is checked in that parent's own commits, when the push
// sends them). A blob no pushed commit's diff describes is missing from
// the result, so the caller keeps it whole (fails closed).
func PushAddedLines(patch string, parents map[string]int) map[string]map[int]bool {
	type section struct {
		commit string
		body   strings.Builder
	}
	var secs []*section
	for _, ln := range strings.Split(patch, "\n") {
		if _, ok := parents[ln]; ok {
			secs = append(secs, &section{commit: ln})
			continue
		}
		if len(secs) > 0 {
			secs[len(secs)-1].body.WriteString(ln + "\n")
		}
	}
	// per commit: per blob, the lines of each parent's diff that add it
	perCommit := map[string]map[string][]map[int]bool{}
	for _, sec := range secs {
		if perCommit[sec.commit] == nil {
			perCommit[sec.commit] = map[string][]map[int]bool{}
		}
		for _, f := range parseAddedFiles(sec.body.String()) {
			perCommit[sec.commit][f.blob] = append(perCommit[sec.commit][f.blob], f.lines)
		}
	}
	out := map[string]map[int]bool{}
	for commit, blobs := range perCommit {
		need := max(parents[commit], 1)
		for blob, sets := range blobs {
			if len(sets) < need {
				addLines(out, blob, nil) // unchanged against a parent: known, adds nothing
				continue
			}
			lines := map[int]bool{}
			for n := range sets[0] {
				every := true
				for _, s := range sets[1:] {
					every = every && s[n]
				}
				if every {
					lines[n] = true
				}
			}
			addLines(out, blob, lines)
		}
	}
	return out
}

// addedLinesKey carries the lines a pushed doc set adds (WithAddedLines).
type addedLinesKey struct{}

// WithAddedLines returns ctx whose F16 check (LeakCheckDocsExplain) reads
// only the given lines of each path (path → 1-based line numbers); a path
// missing from lines is read whole. The pre-push hook sets it per doc set
// (devco:D-728); the secret check never reads it and sees whole files.
func WithAddedLines(ctx context.Context, lines map[string]map[int]bool) context.Context {
	return context.WithValue(ctx, addedLinesKey{}, lines)
}

// AddedLinesOnly is docs narrowed to the lines WithAddedLines set on ctx
// (every other line blanked, line numbers kept), or docs unchanged when
// ctx carries none. F16 reads docs through it (LeakCheckDocsExplain).
func AddedLinesOnly(ctx context.Context, docs map[string][]byte) map[string][]byte {
	if added := addedLinesOf(ctx); added != nil {
		return addedLinesOnly(docs, added)
	}
	return docs
}

// addedLinesOf is the lines WithAddedLines set (nil when none).
func addedLinesOf(ctx context.Context) map[string]map[int]bool {
	m, _ := ctx.Value(addedLinesKey{}).(map[string]map[int]bool)
	return m
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
