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
// Cheap and local: one git log per evidence item, cached for the life of
// the Checker (one command, so one HEAD); no network. Git is injected (the
// wire layer runs it in the code repo), so this package does no I/O.
package factstale

import (
	"context"
	"encoding/json"
	"regexp"
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

// ParseEvidence reads fact_evidence items; an item that names no safe
// repo-relative file and no commit (an absolute path, a `..` step, an
// empty item) is returned in bad and never handed to git.
func ParseEvidence(items []string) (ev []Evidence, bad []string) {
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
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
		ev = append(ev, Evidence{Kind: KindPath, Path: p, Line: line})
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
	var items []string
	for _, e := range evidence {
		items = append(items, strings.Split(e, ",")...) // a text key holds the list comma-joined
	}
	ev, _ := ParseEvidence(items)
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
}

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
}

// NewChecker is a checker asking git through run.
func NewChecker(run Git) *Checker {
	return &Checker{git: run, cache: map[string][]Change{}, times: map[string]time.Time{}}
}

// shortSHA is how many hex digits a printed commit keeps.
const shortSHA = 7

// Check reports whether an evidence file of claim changed after it was
// verified. Evidence git cannot answer for (a commit this clone does not
// have, a path outside the repo) is left out: never stale on a guess.
func (c *Checker) Check(ctx context.Context, cl Claim) Verdict {
	v := Verdict{Since: cl.VerifiedRaw}
	seen := map[string]bool{}
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
			for _, ch := range c.pathChanges(ctx, e.Path, cl.Verified) {
				add(ch)
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
	v.Stale = len(v.Changed) > 0
	return v
}

// pathChanges is the newest commit that changed path after cutoff (none
// when it did not change, or git cannot tell).
func (c *Checker) pathChanges(ctx context.Context, path string, cutoff time.Time) []Change {
	key := "p\x00" + path
	if _, ok := c.cache[key]; !ok {
		out, err := c.git(ctx, "--literal-pathspecs", "log", "-1", "--format=%H%x09%ct", "--", path)
		var got []Change
		if err == nil {
			if sha, at, ok := commitLine(string(out)); ok {
				got = []Change{{Path: path, Commit: sha[:min(shortSHA, len(sha))], At: at}}
			}
		}
		c.cache[key] = got
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
		c.cache[key], c.times[key] = changes, at
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
	// one git log, not rev-parse: lore's in-process git answers
	// (adapters/gitfast) refuse rev-parse's peel syntax
	head, err := c.git(ctx, "log", "-1", "--no-walk", "--format=%H%x09%ct", sha)
	if err != nil {
		return time.Time{}, nil // not a commit of this clone
	}
	id, at, ok := commitLine(string(head))
	if !ok {
		return time.Time{}, nil
	}
	files, err := c.git(ctx, "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", id)
	if err != nil {
		return at, nil
	}
	paths := strings.Fields(string(files))
	if len(paths) == 0 {
		return at, nil
	}
	args := append([]string{"--literal-pathspecs", "log", "--format=%x00%H%x09%ct", "--name-only", id + "..HEAD", "--"}, paths...)
	out, err := c.git(ctx, args...)
	if err != nil {
		return at, nil
	}
	var changes []Change
	seen := map[string]bool{}
	for block := range strings.SplitSeq(string(out), "\x00") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		sha, when, ok := commitLine(lines[0])
		if !ok {
			continue
		}
		for _, p := range lines[1:] {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] || !contains(paths, p) {
				continue
			}
			seen[p] = true // git log is newest first: the first is the newest change
			changes = append(changes, Change{Path: p, Commit: sha[:min(shortSHA, len(sha))], At: when})
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
