package git

// fastwrite.go — a history rewrite in two git processes (store growth
// SG-6 follow-up, devco D-1004):
//
//	metasOf    one `cat-file --batch` reads every commit's people, dates
//	           and message (metaOf ran one `git log` per commit)
//	fastWrite  one `fast-import` writes every new commit: its tree (the
//	           old tree with the held overlay applied in the same stream),
//	           people, dates, message and parents, earlier new commits
//	           named by mark (commit-tree plus read-tree, update-index and
//	           write-tree ran per commit: about 5 processes each, 66 ms a
//	           kept commit, so a 1M → 10k compaction spent minutes there)
//
// fast-import writes exactly the object commit-tree writes for the same
// tree, parents, people, dates and message (TestFastWriteMatchesCommitTree),
// so a clone re-doing the rewrite with either reproduces the same ids.

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fastCommit is one commit fastWrite makes.
type fastCommit struct {
	// tree is the commit's tree before edits; edits sets a path to a blob
	// ("" removes it), as overlaid does
	tree  string
	edits map[string]string
	// parents are commit ids, or marks (":<n>", the n-th commit of the
	// same fastWrite, from 1) of commits made earlier in it
	parents []string
	meta    commitMeta
}

// markOf names the i-th (from 0) commit of a fastWrite to a later one.
func markOf(i int) string { return ":" + strconv.Itoa(i+1) }

// fastWrite writes commits in order with one fast-import and returns
// their ids. A parent mark must name an earlier commit of the same call.
func (b *Backend) fastWrite(ctx context.Context, commits []fastCommit) ([]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	gitDir, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: locate the clone's git dir", err)
	}
	f, err := os.CreateTemp(trimmed(gitDir), "lore-rewrite-marks-")
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: make a marks file", err)
	}
	marks := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(marks) }()
	// a ref of its own per call (fast-import moves the branch it writes;
	// two lore processes never share it), deleted at the end
	ref := "refs/lore-tmp/rewrite-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	defer func() { _, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", "-d", ref}}) }()
	var in bytes.Buffer
	for i, c := range commits {
		author, committer, ok := c.meta.idents()
		if !ok {
			return nil, errs.New(errs.Internal, "git: a commit to re-make has no author or committer")
		}
		// reset: a commit with no parent starts a history of its own
		in.WriteString("reset " + ref + "\ncommit " + ref + "\nmark " + markOf(i) + "\n")
		in.WriteString("author " + author + "\ncommitter " + committer + "\n")
		in.WriteString("data " + strconv.Itoa(len(c.meta.msg)) + "\n" + c.meta.msg + "\n")
		// commit-tree drops a repeated parent ("duplicate parent ignored"):
		// so does this, for the same commit
		for j, p := range slices.Compact(slices.Clone(c.parents)) {
			if j == 0 {
				in.WriteString("from " + p + "\n")
			} else {
				in.WriteString("merge " + p + "\n")
			}
		}
		in.WriteString("M 040000 " + c.tree + " \"\"\n")
		for _, p := range sortedKeys(c.edits) {
			if blob := c.edits[p]; blob == "" {
				in.WriteString("D " + fastPath(p) + "\n")
			} else {
				in.WriteString("M 100644 " + blob + " " + fastPath(p) + "\n")
			}
		}
		in.WriteString("\n")
	}
	in.WriteString("done\n")
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"fast-import", "--quiet", "--done", "--date-format=raw", "--export-marks=" + marks}, stdin: in.Bytes()}); err != nil {
		return nil, errs.Wrap(errs.Internal, "git: write the rewritten history: "+stderrOf(err), err)
	}
	raw, err := os.ReadFile(marks)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the rewritten ids", err)
	}
	ids := make([]string, len(commits))
	for l := range strings.SplitSeq(string(raw), "\n") {
		m, id, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok || !commitIDPattern.MatchString(id) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(m, ":"))
		if err == nil && n >= 1 && n <= len(ids) {
			ids[n-1] = id
		}
	}
	for _, id := range ids {
		if id == "" {
			return nil, errs.New(errs.Internal, "git: fast-import did not name every rewritten commit")
		}
	}
	return ids, nil
}

// fastPath is p as a fast-import path: as is, or C-quoted when it starts
// with a quote or holds a newline or backslash.
func fastPath(p string) string {
	if !strings.HasPrefix(p, `"`) && !strings.ContainsAny(p, "\n\\") {
		return p
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(p) + `"`
}

// sortedKeys is m's keys in order.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// metasOf reads the people, dates and message of every commit in ids
// with one `cat-file --batch`, as metaOf reads one.
func (b *Backend) metasOf(ctx context.Context, ids []string) (map[string]commitMeta, error) {
	out := make(map[string]commitMeta, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var in strings.Builder
	for _, id := range ids {
		in.WriteString(id + "\n")
	}
	res, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "--batch"}, stdin: []byte(in.String())})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the commits to re-make", err)
	}
	r := bufio.NewReader(bytes.NewReader(res))
	for _, id := range ids {
		head, err := r.ReadString('\n')
		if err != nil {
			return nil, errs.New(errs.Internal, "git: cat-file answered fewer commits than asked")
		}
		f := strings.Fields(head)
		if len(f) != 3 || f[1] != "commit" {
			return nil, errs.New(errs.Internal, "git: "+short(id)+" is not a commit this clone holds")
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, errs.New(errs.Internal, "git: cat-file answered a size it cannot read")
		}
		body := make([]byte, n+1) // the object and its newline
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, errs.New(errs.Internal, "git: cat-file answered a short commit")
		}
		m, ok := parseCommitObject(body[:n])
		if !ok {
			// an unusual header (an encoding git log re-encodes): the one
			// commit is read the way metaOf reads it
			if m, err = b.metaOf(ctx, id); err != nil {
				return nil, err
			}
		}
		out[id] = m
	}
	return out, nil
}

// parseCommitObject is a raw commit's people, raw dates and message as
// metaOf reads them (the message without its trailing newlines); ok is
// false for a header metaOf would read otherwise (an encoding header).
func parseCommitObject(raw []byte) (commitMeta, bool) {
	hdr, msg, _ := bytes.Cut(raw, []byte("\n\n"))
	var author, committer string
	for l := range strings.SplitSeq(string(hdr), "\n") {
		switch {
		case strings.HasPrefix(l, "author "):
			author = strings.TrimPrefix(l, "author ")
		case strings.HasPrefix(l, "committer "):
			committer = strings.TrimPrefix(l, "committer ")
		case strings.HasPrefix(l, "encoding "):
			return commitMeta{}, false
		}
	}
	an, ae, ad, ok1 := splitIdent(author)
	cn, ce, cd, ok2 := splitIdent(committer)
	if !ok1 || !ok2 {
		return commitMeta{}, false
	}
	return commitMeta{env: []string{
		envAuthorName + "=" + an, envAuthorEmail + "=" + ae, "GIT_AUTHOR_DATE=" + ad,
		envCommitterName + "=" + cn, envCommitterEmail + "=" + ce, "GIT_COMMITTER_DATE=" + cd,
	}, msg: strings.TrimRight(string(msg), "\n")}, true
}

// splitIdent is "Name <email> 1700000000 +0000" as name, email and raw date.
func splitIdent(s string) (name, email, date string, ok bool) {
	lt := strings.Index(s, "<")
	gt := strings.LastIndex(s, ">")
	if lt < 0 || gt < lt {
		return "", "", "", false
	}
	return strings.TrimSpace(s[:lt]), s[lt+1 : gt], strings.TrimSpace(s[gt+1:]), true
}

// idents is the author and committer of m as fast-import takes them
// ("Name <email> <raw date>").
func (m commitMeta) idents() (author, committer string, ok bool) {
	v := map[string]string{}
	for _, e := range m.env {
		if k, val, found := strings.Cut(e, "="); found {
			v[k] = val
		}
	}
	ad, cd := v["GIT_AUTHOR_DATE"], v["GIT_COMMITTER_DATE"]
	if ad == "" || cd == "" {
		return "", "", false
	}
	return identLine(v[envAuthorName], v[envAuthorEmail], ad), identLine(v[envCommitterName], v[envCommitterEmail], cd), true
}

// identLine is one fast-import person line (a date commit-tree takes as
// "@<seconds> <zone>" is written without the @, as git stores it).
func identLine(name, email, date string) string {
	date = strings.TrimPrefix(date, "@")
	if name == "" {
		return "<" + email + "> " + date
	}
	return name + " <" + email + "> " + date
}
