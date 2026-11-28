// Package factstale is the staleness signal of a fact about code
// (devco:D-941, bug hunt H-190). A row that states how the code works
// carries two metadata keys: fact_evidence (where it can be checked: a
// repo-relative `<path>:<line>`, a `<path>`, or a commit sha) and
// fact_verified_at (the day it was last checked). When an evidence file
// changed in git after that day, the row may no longer be true: lore lint
// reports it with the changed files and the command that re-verifies it,
// lore doctor counts such rows, and lore search and lore ask mark the row
// and rank it below a fresh row with the same match.
//
//	ClaimOf(row) / ClaimOfItems(evidence, verified) ─► Claim
//	Checker.Check(ctx, claim)
//	  ├─ a path item (only with fact_verified_at): one `git log -1` on the
//	  │  path; stale when its newest commit is after the verified day
//	  ├─ a commit item: its files (git diff-tree); stale when one of them
//	  │  changed after the commit (and after the verified day, when set)
//	  └─ Verdict{Stale, Since, Changed} ─► Note: "stale: evidence changed since …"
//
// Cheap and local: Prime asks git once for every path item of the rows a
// command is about to check (one git log over all their paths) and once
// for the commit items' files, so a command costs a fixed handful of git
// processes however many rows name evidence (bug hunt H-209: one git log
// per item made lore ask take ~15 s on a store whose every row names its
// evidence). Answers are cached for the life of the Checker (one command,
// so one HEAD) and, through a Memo, across commands at the same HEAD (bug
// hunt H-213: the one primed git log still cost ~90 ms of every warm lore
// ask): a warm call with HEAD unchanged asks git nothing. No network. Git
// and the memo are injected (the wire layer runs git in the code repo and
// keeps the memo in the lore home's cache), so this package does no I/O.
package factstale

import (
	"context"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
)

// Kind is what an evidence item names (a closed set).
type Kind string

// The evidence kinds.
const (
	KindPath   Kind = "path"
	KindCommit Kind = "commit"
)

// Evidence is one fact_evidence item.
type Evidence struct {
	Kind Kind
	// Path is the repo-relative file of a path item.
	Path string
	// Line is the line a path item names (0 when none).
	Line int
	// Commit is the sha of a commit item, lowercase.
	Commit string
	// Near is, for a path item naming a bare file (no folder), the folder
	// of the path item written before it: `a/b/x.go:12, y.go:4` names
	// a/b/y.go when the repo has no top-level y.go (bug hunt H-298).
	Near string
}

// Claim is what a row says about its own evidence.
type Claim struct {
	Evidence []Evidence
	// Verified is the instant after which a change makes the fact stale:
	// the end of the fact_verified_at day (UTC), or its exact time when
	// the value is a datetime; zero when unset or unreadable.
	Verified time.Time
	// VerifiedRaw is fact_verified_at as written (the day Note prints).
	VerifiedRaw string
}

// Checkable reports whether the claim can ever be stale: a commit item
// always can, a path item only once the fact was verified (an unverified
// fact is not stale, it was never checked).
func (c Claim) Checkable() bool {
	for _, e := range c.Evidence {
		if e.Kind == KindCommit || !c.Verified.IsZero() {
			return true
		}
	}
	return false
}

// reCommit is a commit item: 7 to 40 hex digits and nothing else.
var reCommit = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// reLine is the line part of a path item: 42 or a range 42-50.
var reLine = regexp.MustCompile(`^([0-9]+)(?:-[0-9]+)?$`)

// reLineOnly is a piece that is only a line or a line range: the rest of
// the item before it (`a.go:12-20,40-44` lists two ranges of a.go).
var reLineOnly = regexp.MustCompile(`^[0-9]{1,6}(?:-[0-9]+)?$`)

// reNote is a parenthesised note inside an item (`(origin/dev 7813f13)`,
// `(executeCore)`): words about the evidence, never evidence.
var reNote = regexp.MustCompile(`\([^()]*\)`)

// SplitEvidence cuts fact_evidence as people write it into items (bug
// hunt H-298: a team wrote `x/a.go:1-5,9; a.go:7 (origin/dev 1a2b3c4);
// b.go:3` as one text value, and the old comma split read most of it as
// one path git never knew, so no row was ever stale). Items are separated
// by commas, semicolons and line breaks; a parenthesised note is dropped;
// a piece holding a word that cannot be evidence (`Evidence: a.go`) keeps
// only its words that can (a folder or an extension, or a commit sha),
// else it is one item (a path with a space); a piece that is
// only a line range belongs to the item before it and is dropped.
func SplitEvidence(items []string) []string {
	var out []string
	for _, raw := range items {
		for _, piece := range strings.FieldsFunc(reNote.ReplaceAllString(raw, " "), func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		}) {
			words := strings.Fields(piece)
			if len(words) < 2 || !slices.ContainsFunc(words, func(w string) bool { return !evidenceWord(w) }) {
				out = appendPiece(out, strings.TrimSpace(piece)) // one item, a path with a space kept whole
				continue
			}
			for _, w := range words {
				if evidenceWord(w) {
					out = appendPiece(out, w)
				}
			}
		}
	}
	return out
}

// evidenceWord reports whether a word of a piece can be (part of) an
// evidence item: it has a folder or an extension, or it is a commit sha.
func evidenceWord(w string) bool {
	return strings.ContainsAny(strings.TrimRight(w, ".:"), "/.") || reCommit.MatchString(strings.ToLower(w))
}

// appendPiece appends one evidence piece, its trailing punctuation cut;
// an empty piece or a bare line range is left out.
func appendPiece(out []string, p string) []string {
	p = strings.TrimRight(strings.TrimSpace(p), ".:")
	if p == "" || reLineOnly.MatchString(p) {
		return out
	}
	return append(out, p)
}

// ParseEvidence reads fact_evidence items (as SplitEvidence cuts them); an
// item that names no safe repo-relative file and no commit (an absolute
// path, a `..` step, an empty item) is returned in bad and never handed
// to git.
func ParseEvidence(items []string) (ev []Evidence, bad []string) {
	near := ""
	for _, s := range SplitEvidence(items) {
		if low := strings.ToLower(s); reCommit.MatchString(low) {
			ev = append(ev, Evidence{Kind: KindCommit, Commit: low})
			continue
		}
		p, line := s, 0
		if i := strings.LastIndexByte(s, ':'); i > 0 {
			if m := reLine.FindStringSubmatch(s[i+1:]); m != nil {
				n, err := strconv.Atoi(m[1])
				if err == nil {
					p, line = s[:i], n
				}
			}
		}
		p = strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
		if !safePath(p) {
			bad = append(bad, s)
			continue
		}
		e := Evidence{Kind: KindPath, Path: p, Line: line}
		if i := strings.LastIndexByte(p, '/'); i > 0 {
			near = p[:i]
		} else {
			e.Near = near
		}
		ev = append(ev, e)
	}
	return ev, bad
}

// safePath reports whether p is a repo-relative path git may be asked
// about: not absolute, no `..` step, not an option.
func safePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") || (len(p) > 1 && p[1] == ':') {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// ParseVerified reads a fact_verified_at value: a day (the fact holds
// through that day, UTC) or a datetime (exact). ok is false for anything
// else.
func ParseVerified(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if d, err := time.Parse(time.DateOnly, s); err == nil {
		return d.AddDate(0, 0, 1).Add(-time.Second), true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// ClaimOfItems is the claim of a row whose fact_evidence items and
// fact_verified_at value were read as text (ask reads them from the
// cache); ok is false when it names no evidence.
func ClaimOfItems(evidence []string, verified string) (Claim, bool) {
	ev, _ := ParseEvidence(evidence) // a text key holds the list joined (SplitEvidence cuts it)
	if len(ev) == 0 {
		return Claim{}, false
	}
	c := Claim{Evidence: ev, VerifiedRaw: strings.TrimSpace(verified)}
	if t, ok := ParseVerified(verified); ok {
		c.Verified = t
	} else {
		c.VerifiedRaw = ""
	}
	return c, true
}

// Raw is a row's fact_evidence items and fact_verified_at value, read
// from its metadata whatever type the keys were registered with (a list or
// comma-joined text; a date, datetime or text); ok is false when the row
// names no evidence.
func Raw(r row.Row) (evidence []string, verified string, ok bool) {
	for _, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil {
			continue
		}
		switch en.Name {
		case meta.KeyFactEvidence:
			evidence = strs(en.V)
		case meta.KeyFactVerifiedAt:
			if vs := strs(en.V); len(vs) > 0 {
				verified = vs[0]
			}
		}
	}
	return evidence, verified, len(evidence) > 0
}

// ClaimOf is the claim of a stored row; ok is false when it names no
// evidence.
func ClaimOf(r row.Row) (Claim, bool) {
	ev, verified, ok := Raw(r)
	if !ok {
		return Claim{}, false
	}
	return ClaimOfItems(ev, verified)
}

// strs reads a JSON string or list of strings ("" items dropped).
func strs(raw []byte) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	return many
}

// Git runs `git <args>` in the code repository the evidence paths are
// relative to and returns its standard output.
type Git func(ctx context.Context, args ...string) ([]byte, error)

// Change is one evidence file that changed after the fact was verified.
type Change struct {
	Path string `json:"path"`
	// Commit is the short sha of the newest commit that changed it.
	Commit string    `json:"commit"`
	At     time.Time `json:"at"`
}

// Verdict is the result of checking one claim.
type Verdict struct {
	Stale bool
	// Since is the day the changes are counted from (fact_verified_at as
	// written, else the evidence commit's day).
	Since   string
	Changed []Change
	// Unfound is each path item the code repo holds no file or folder for
	// under any reading while another item of the claim is there (bug hunt
	// H-298): the repo is the right one but the check could not run for
	// that file, so it is said, never taken as fresh. Empty when the
	// checker has no view of the work tree (WithFiles).
	Unfound []string
	// Unreachable is set when none of the claim's path items is in this
	// repo: the row is about code this repo does not hold (a command run
	// in another repo), so ask and search say nothing and lint and doctor
	// count it.
	Unreachable bool
}

// NoRepo is why no row's evidence is checked when the command runs
// outside a git work tree (bug hunt H-298: asked from the folder above the
// code repo, every stale row read as fresh); show prints it, lint and
// doctor count such rows.
const NoRepo = "evidence could not be checked: no git repo here (fact_evidence paths are relative to the code repo; run the command in it)"

// Unchecked is the line search, ask, show and lint print for evidence the
// check could not find, "" when every item was found:
// `stale check: evidence not found in this repo (billing/refund.go)`.
func (v Verdict) Unchecked() string {
	if len(v.Unfound) == 0 {
		return ""
	}
	return UnfoundLead + " (not in this repo: " + strings.Join(v.Unfound, ", ") + ")"
}

// UnfoundLead starts the Unchecked line of evidence the repo does not hold.
const UnfoundLead = "evidence could not be checked"

// Note is the mark search and ask print on a stale row, "" when fresh:
// `stale: evidence changed since 2026-11-22 (billing/refund.go)`.
func (v Verdict) Note() string {
	if !v.Stale {
		return ""
	}
	paths := make([]string, 0, len(v.Changed))
	for _, c := range v.Changed {
		paths = append(paths, c.Path)
	}
	return "stale: evidence changed since " + v.Since + " (" + strings.Join(paths, ", ") + ")"
}

// Reverify is what clears a stale verdict once the row and the code were
// read again, "" when v is fresh. Lint, show, ask and context print the
// same words (devco:D-953, bug hunt H-212: ask and show printed no next
// step, so an agent had to know lint's). store is the --store the row's
// write needs ("" for the repo store). The day is today, or the newest
// change's day when that is later (a skewed clock, a rebase), so the
// command always clears the verdict.
func Reverify(id, store string, v Verdict, now time.Time) string {
	if !v.Stale {
		return ""
	}
	day := now.UTC().Format(time.DateOnly)
	for _, c := range v.Changed {
		day = max(day, c.At.UTC().Format(time.DateOnly))
	}
	where := ""
	if store != "" {
		where = " --store " + store
	}
	return "when it still holds: lore meta set --id " + id + where + " --pair " + meta.KeyFactVerifiedAt + "=" + day + "; when it does not: edit the row and its " + meta.KeyFactEvidence
}

// Checker checks claims against one repository at one HEAD, each git
// question asked once.
type Checker struct {
	git   Git
	cache map[string][]Change
	times map[string]time.Time
	// bound is, for an answer Prime read from a git log limited by
	// --since, that limit: the answer says nothing of older commits, so it
	// serves only a claim verified at or after it (no entry: the answer is
	// the path's newest commit of all, good for any claim).
	bound map[string]time.Time
	// heads is each primed commit item's time and files (Prime), so
	// readCommit skips its own git log and diff-tree.
	heads map[string]commitHead
	// memo keeps the answers across commands at one HEAD (nil: none).
	memo  Memo
	dirty bool
	// exists reports whether the work tree holds a repo-relative path (nil:
	// paths are asked of git as written).
	exists   func(path string) bool
	resolved map[string]string
}

// WithFiles lets the checker read evidence paths as people write them
// (bug hunt H-298): exists reports whether the work tree holds a
// repo-relative file or folder. A path item the tree does not hold is
// read, in order, as a bare file in the folder of the item before it, and
// with its first folder cut (`myrepo/billing/refund.go` written from the
// folder above the repo is billing/refund.go); the first reading the tree
// holds is the one git is asked about. No reading found: the path is asked
// as written (a file deleted since is still a change) and, when git knows
// no change either, the verdict lists it in Unfound.
func (c *Checker) WithFiles(exists func(path string) bool) *Checker {
	c.exists = exists
	return c
}

// path is the repo path git is asked about for a path item (see WithFiles).
func (c *Checker) path(e Evidence) string {
	if c.exists == nil {
		return e.Path
	}
	key := e.Near + "\x00" + e.Path
	if p, ok := c.resolved[key]; ok {
		return p
	}
	cands := []string{e.Path}
	if e.Near != "" {
		cands = append(cands, e.Near+"/"+e.Path)
	}
	got := ""
	for _, cand := range cands {
		if c.exists(cand) {
			got = cand
			break
		}
	}
	for _, cand := range cands {
		// one folder only (the repo's own, written from the folder above
		// it): cutting more would match an unrelated README.md
		if _, rest, ok := strings.Cut(cand, "/"); ok && got == "" && c.exists(rest) {
			got = rest
		}
	}
	if got == "" {
		got = e.Path
	}
	if c.resolved == nil {
		c.resolved = map[string]string{}
	}
	c.resolved[key] = got
	return got
}

// found reports whether the work tree holds p (true without WithFiles).
func (c *Checker) found(p string) bool {
	return c.exists == nil || c.exists(p)
}

// Memo keeps a Checker's answers between commands (bug hunt H-213). The
// answers hold while the repository's HEAD does: Load returns the snapshot
// kept for the current HEAD (ok false for none, or one of another HEAD),
// Save keeps one. The wire layer keys it by the code repo and its HEAD
// commit; a failing Save only costs the next command its git log.
type Memo interface {
	Load() (Snapshot, bool)
	Save(Snapshot)
}

// Snapshot is a Checker's answers, as a Memo keeps them.
type Snapshot struct {
	// Answers is the cached changes by item key (a path or commit item).
	Answers map[string][]Change `json:"answers"`
	// Times is each commit item's own time.
	Times map[string]time.Time `json:"times,omitempty"`
	// Bounds is each --since limited answer's limit (Checker.bound).
	Bounds map[string]time.Time `json:"bounds,omitempty"`
}

// commitHead is a commit's id, time and the files it touched.
type commitHead struct {
	id    string
	at    time.Time
	files []string
}

// NewChecker is a checker asking git through run.
func NewChecker(run Git) *Checker {
	return &Checker{git: run, cache: map[string][]Change{}, times: map[string]time.Time{}, bound: map[string]time.Time{}, heads: map[string]commitHead{}}
}

// NewMemoChecker is a checker asking git through run that starts from the
// answers memo kept for this HEAD and keeps what it learns there (Prime
// saves once it asked git), so a command repeated at one HEAD runs no git
// for its evidence (bug hunt H-213).
func NewMemoChecker(run Git, memo Memo) *Checker {
	c := NewChecker(run)
	c.memo = memo
	if snap, ok := memo.Load(); ok {
		maps.Copy(c.cache, snap.Answers)
		maps.Copy(c.times, snap.Times)
		maps.Copy(c.bound, snap.Bounds)
	}
	return c
}

// known reports whether the cached answer for key serves a claim verified
// at cutoff: an exact answer always does, a --since limited one only when
// the claim is not older than its limit.
func (c *Checker) known(key string, cutoff time.Time) bool {
	if _, ok := c.cache[key]; !ok {
		return false
	}
	b, limited := c.bound[key]
	return !limited || !cutoff.Before(b)
}

// save hands the answers to the memo when Prime or Check asked git.
func (c *Checker) save() {
	if c.memo == nil || !c.dirty {
		return
	}
	c.dirty = false
	c.memo.Save(Snapshot{Answers: maps.Clone(c.cache), Times: maps.Clone(c.times), Bounds: maps.Clone(c.bound)})
}

// primeChunk is how many paths one primed git log names, so the command
// line stays far below the system's argument limit.
const primeChunk = 256

// Prime answers ahead, in a few git processes, what Check would ask git
// one evidence item at a time for every claim (bug hunt H-209): the newest
// change of every path item, from one `git log --name-only` over all the
// paths limited to commits after the oldest verified day (an older commit
// can make no claim stale), and each commit item's time and files from one
// `git log --no-walk`. A path no listed commit touched has no change after
// any of the claims' days. When git refuses a primed call (a commit this
// clone does not have), nothing of it is cached and Check asks per item as
// before. Calling Check without Prime gives the same verdicts.
func (c *Checker) Prime(ctx context.Context, claims []Claim) {
	var paths, shas []string
	var since time.Time
	want := map[string]bool{}
	for _, cl := range claims {
		for _, e := range cl.Evidence {
			switch e.Kind {
			case KindPath:
				p := c.path(e)
				if cl.Verified.IsZero() || c.known("p\x00"+p, cl.Verified) {
					continue
				}
				if want["p\x00"+p] {
					if cl.Verified.Before(since) {
						since = cl.Verified
					}
					continue
				}
				want["p\x00"+p] = true
				paths = append(paths, p)
				if since.IsZero() || cl.Verified.Before(since) {
					since = cl.Verified
				}
			case KindCommit:
				if _, done := c.heads[e.Commit]; done || want["c\x00"+e.Commit] {
					continue
				}
				if _, done := c.cache["c\x00"+e.Commit]; done {
					continue
				}
				want["c\x00"+e.Commit] = true
				shas = append(shas, e.Commit)
			}
		}
	}
	for chunk := range slices.Chunk(paths, primeChunk) {
		c.primePaths(ctx, chunk, since)
	}
	for chunk := range slices.Chunk(shas, primeChunk) {
		c.primeCommits(ctx, chunk)
	}
	for _, sha := range shas {
		// a commit item's verdict needs its later changes too: ask them
		// now (from the primed heads), so the memo keeps the whole answer
		c.commitChanges(ctx, sha, time.Time{})
	}
	c.save()
}

// Save hands every answer asked since the last save to the memo; a
// command that called Check without Prime calls it once at the end.
func (c *Checker) Save() { c.save() }

// logMark starts each commit's header line in a primed git log's output,
// so a file name can never be read as a commit.
const logMark = "\x01"

// primePaths caches the newest change after since of each of paths.
func (c *Checker) primePaths(ctx context.Context, paths []string, since time.Time) {
	args := append([]string{"--literal-pathspecs", "log", "-z", "--format=" + "%x01%H%x09%ct", "--name-only", "--since=" + since.UTC().Format(time.RFC3339), "--"}, paths...)
	out, err := c.git(ctx, args...)
	if err != nil {
		return // Check asks per path
	}
	asked := map[string]bool{}
	for _, p := range paths {
		asked[p] = true
	}
	got := map[string][]Change{}
	for _, l := range logEntries(out) {
		for _, f := range l.files {
			// a path item may name a folder: a change to a file in it is
			// a change to it (as `git log -1 -- <folder>` reports)
			for p := f; p != "" && p != "."; p = parentOf(p) {
				if asked[p] && got[p] == nil { // newest first: keep the first
					got[p] = []Change{{Path: p, Commit: l.id[:min(shortSHA, len(l.id))], At: l.at}}
				}
			}
		}
	}
	for _, p := range paths {
		key := "p\x00" + p
		c.cache[key] = got[p] // nil: not changed after since
		if got[p] == nil {
			c.bound[key] = since
		} else {
			delete(c.bound, key) // the newest commit of all: exact
		}
	}
	c.dirty = true
}

// primeCommits caches each commit item's time and files from one git log;
// nothing when git refuses one of them (Check then asks per item).
func (c *Checker) primeCommits(ctx context.Context, shas []string) {
	args := append([]string{"log", "-z", "--no-walk=unsorted", "--root", "--format=" + "%x01%H%x09%ct", "--name-only"}, shas...)
	out, err := c.git(ctx, args...)
	if err != nil {
		return
	}
	entries := logEntries(out)
	if len(entries) != len(shas) {
		return // git folded two items naming one commit: ask per item
	}
	for i, l := range entries {
		c.heads[shas[i]] = l
	}
}

// logEntries reads a primed git log's -z output: per commit a header
// "\x01<sha>\t<unix seconds>" then its files, every item NUL-ended.
func logEntries(out []byte) []commitHead {
	var all []commitHead
	for tok := range strings.SplitSeq(string(out), "\x00") {
		tok = strings.TrimPrefix(tok, "\n")
		if tok == "" {
			continue
		}
		if head, ok := strings.CutPrefix(tok, logMark); ok {
			if sha, at, ok := commitLine(head); ok {
				all = append(all, commitHead{id: sha, at: at})
			}
			continue
		}
		if n := len(all); n > 0 {
			all[n-1].files = append(all[n-1].files, tok)
		}
	}
	return all
}

// parentOf is p's folder ("" at the top).
func parentOf(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

// shortSHA is how many hex digits a printed commit keeps.
const shortSHA = 7

// Check reports whether an evidence file of claim changed after it was
// verified. Evidence git cannot answer for (a commit this clone does not
// have, a path outside the repo) is left out: never stale on a guess.
func (c *Checker) Check(ctx context.Context, cl Claim) Verdict {
	v := Verdict{Since: cl.VerifiedRaw}
	seen := map[string]bool{}
	paths, foundAny := 0, false
	add := func(ch Change) {
		if !seen[ch.Path] {
			seen[ch.Path] = true
			v.Changed = append(v.Changed, ch)
		}
	}
	for _, e := range cl.Evidence {
		switch e.Kind {
		case KindPath:
			if cl.Verified.IsZero() {
				continue // never verified: not stale, unchecked
			}
			p := c.path(e)
			changes := c.pathChanges(ctx, p, cl.Verified)
			for _, ch := range changes {
				add(ch)
			}
			paths++
			if len(changes) == 0 && !c.found(p) {
				v.Unfound = append(v.Unfound, e.Path)
			} else {
				foundAny = true
			}
		case KindCommit:
			at, changes := c.commitChanges(ctx, e.Commit, cl.Verified)
			if len(changes) > 0 && v.Since == "" {
				v.Since = at.UTC().Format(time.DateOnly)
			}
			for _, ch := range changes {
				add(ch)
			}
		}
	}
	if paths > 0 && !foundAny {
		v.Unfound, v.Unreachable = nil, true // code this repo does not hold
	}
	v.Stale = len(v.Changed) > 0
	return v
}

// pathChanges is the newest commit that changed path after cutoff (none
// when it did not change, or git cannot tell).
func (c *Checker) pathChanges(ctx context.Context, path string, cutoff time.Time) []Change {
	key := "p\x00" + path
	if !c.known(key, cutoff) {
		out, err := c.git(ctx, "--literal-pathspecs", "log", "-1", "--format=%H%x09%ct", "--", path)
		var got []Change
		if err == nil {
			if sha, at, ok := commitLine(string(out)); ok {
				got = []Change{{Path: path, Commit: sha[:min(shortSHA, len(sha))], At: at}}
			}
		}
		c.cache[key], c.dirty = got, true
		delete(c.bound, key)
	}
	var out []Change
	for _, ch := range c.cache[key] {
		if ch.At.After(cutoff) {
			out = append(out, ch)
		}
	}
	return out
}

// commitChanges is the commit's time and, for each file it touched, the
// newest later commit that changed it after the commit and after cutoff.
func (c *Checker) commitChanges(ctx context.Context, sha string, cutoff time.Time) (time.Time, []Change) {
	key := "c\x00" + sha
	if _, ok := c.cache[key]; !ok {
		at, changes := c.readCommit(ctx, sha)
		c.cache[key], c.times[key], c.dirty = changes, at, true
	}
	at := c.times[key]
	var out []Change
	for _, ch := range c.cache[key] {
		if ch.At.After(cutoff) {
			out = append(out, ch)
		}
	}
	return at, out
}

// readCommit asks git for sha's time and its files' later changes.
func (c *Checker) readCommit(ctx context.Context, sha string) (time.Time, []Change) {
	h, primed := c.heads[sha]
	if !primed {
		// one git log, not rev-parse: lore's in-process git answers
		// (adapters/gitfast) refuse rev-parse's peel syntax
		head, err := c.git(ctx, "log", "-1", "--no-walk", "--format=%H%x09%ct", sha)
		if err != nil {
			return time.Time{}, nil // not a commit of this clone
		}
		var ok bool
		if h.id, h.at, ok = commitLine(string(head)); !ok {
			return time.Time{}, nil
		}
		// -z: a file name with a space or a non-ASCII letter is read whole
		files, err := c.git(ctx, "diff-tree", "-z", "--root", "--no-commit-id", "--name-only", "-r", h.id)
		if err != nil {
			return h.at, nil
		}
		for f := range strings.SplitSeq(string(files), "\x00") {
			if f != "" {
				h.files = append(h.files, f)
			}
		}
	}
	id, at, paths := h.id, h.at, h.files
	if len(paths) == 0 {
		return at, nil
	}
	args := append([]string{"--literal-pathspecs", "log", "-z", "--format=%x01%H%x09%ct", "--name-only", id + "..HEAD", "--"}, paths...)
	out, err := c.git(ctx, args...)
	if err != nil {
		return at, nil
	}
	var changes []Change
	seen := map[string]bool{}
	for _, l := range logEntries(out) { // git log is newest first: the first is the newest change
		for _, p := range l.files {
			if seen[p] || !contains(paths, p) {
				continue
			}
			seen[p] = true
			changes = append(changes, Change{Path: p, Commit: l.id[:min(shortSHA, len(l.id))], At: l.at})
		}
	}
	return at, changes
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// commitLine reads "<sha>\t<unix seconds>".
func commitLine(s string) (string, time.Time, bool) {
	sha, ts, ok := strings.Cut(strings.TrimSpace(s), "\t")
	if !ok || sha == "" {
		return "", time.Time{}, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	return sha, time.Unix(n, 0).UTC(), true
}
