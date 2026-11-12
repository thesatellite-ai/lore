// Package mergewatch tells "the agent says it is finished" apart from "the
// code is on the default branch" (LORE_TEAM_SPEC.md F9, TEAM-017 / D7).
//
// Why a package of its own: a dependent task in another repo must start
// against merged code, so `task next`, `task list`, `mission status` and
// `inbox` all ask "did done task T merge?". The answer comes from three
// checks in a fixed order — ancestry of the recorded done_commit, then a
// `Lore-Task: <id>` trailer on the default branch (squash and rebase
// merges), then an optional host adapter — and the merge time recorded must
// be deterministic (the merge commit's committer date) so two clones that
// find the same merge write the same merged_at. Checking writes nothing;
// only a positive finding is written, once, by the caller.
//
// The package also owns the trailer mechanism the commit-msg hook uses
// while a claim is held (AddTrailer), so the writer and the reader of the
// trailer agree on one format.
//
// Moved from saas/pkg/aicoder/mergewatch (PASS3 p3-mergewatch): the old
// path is a forwarding shell. The lazy check both binaries run before a
// view (Sweep, the v0.1.17 lazyMergedCheck / storeRows.MergedCheck loop)
// lives here too, so a host only supplies the candidates (done rows without
// merged_at) and records each Found through its own write path.
//
//	host view (task list / task next / mission status)
//	  └► candidates: done tasks without merged_at, with the repo they belong to
//	      └► Checker.Sweep(here) ─► Check per candidate: ancestry → trailer → adapter
//	          └► []Found ─► host stamps merged_at = Finding.At (once, on the fresh row)
//	                     ─► Line(found, recordErr) on stderr
package mergewatch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TrailerKey is the commit trailer naming the task a commit belongs to.
const TrailerKey = "Lore-Task"

// Method is how a merge was found. Closed set (F9 check order + manual).
type Method string

const (
	// MethodAncestry means done_commit is an ancestor of the target branch.
	MethodAncestry Method = "ancestry"
	// MethodTrailer means a commit on the target carries `Lore-Task: <id>`.
	MethodTrailer Method = "trailer"
	// MethodAdapter means the forge adapter reports done_pr merged.
	MethodAdapter Method = "adapter"
	// MethodManual is `lore task merged <id>`.
	MethodManual Method = "manual"
)

// Candidate is one done task without merged_at.
type Candidate struct {
	TaskID string
	// DoneCommit is `task done --commit` ("" = none).
	DoneCommit string
	// DonePR is `task done --pr` (0 = none).
	DonePR int
	// Repo names the repo the task's work is in, in whatever form the host
	// compares (a mount name or a repo id); "" = unknown. Only Sweep reads
	// it: without an adapter a task of another repo is checked there.
	Repo string
}

// Finding is a positive result.
type Finding struct {
	Method Method
	// Commit is the commit on the default branch that carries the work
	// (the merge commit, the squash commit, or the commit itself).
	Commit string
	// At is Commit's committer date: the merged_at to record.
	At time.Time
}

// Step is one check and its outcome, for `lore explain` and --trace.
type Step struct {
	Method Method
	Result string
}

// Adapter asks a code host whether a pull request merged (F9 check 3).
// Implementations: GitHub (gh); nil = no adapter (merge_check.adapter none).
type Adapter interface {
	// MergedPR returns the merge commit and time of pull request n, or
	// ok=false while it is open or closed unmerged.
	MergedPR(ctx context.Context, n int) (commit string, at time.Time, ok bool, err error)
}

// Checker runs the checks against one repository.
type Checker struct {
	Repo *gitref.Repo
	// Target is the default branch as fetched, e.g. refs/remotes/origin/main.
	Target string
	// Adapter is optional.
	Adapter Adapter
}

// ErrNoTarget means the target ref does not exist (nothing fetched yet).
var ErrNoTarget = errors.New("mergewatch: default branch not fetched")

// Check runs the checks in order and returns the first finding, or ok=false
// ("done, not merged yet" / "merge unknown"). steps records every check.
func (c Checker) Check(ctx context.Context, cand Candidate) (Finding, bool, []Step, error) {
	tip, err := c.tip(ctx)
	if err != nil {
		return Finding{}, false, nil, err
	}
	return c.checkAt(ctx, cand, tip, func(id string) (Finding, bool, string, error) { return c.byTrailer(ctx, id, tip) })
}

// tip resolves the target branch (ErrNoTarget when it was never fetched).
func (c Checker) tip(ctx context.Context) (string, error) {
	tip, ok, err := c.Repo.ResolveRef(ctx, c.Target)
	if err != nil {
		return "", fmt.Errorf("mergewatch: resolve %s: %w", c.Target, err)
	}
	if !ok {
		return "", ErrNoTarget
	}
	return tip, nil
}

// checkAt is Check against a resolved tip, with the trailer check given
// (one git log per candidate for Check, one shared index for Sweep).
func (c Checker) checkAt(ctx context.Context, cand Candidate, tip string, trailer func(id string) (Finding, bool, string, error)) (Finding, bool, []Step, error) {
	var steps []Step
	if cand.DoneCommit != "" {
		f, found, why, aerr := c.byAncestry(ctx, cand.DoneCommit, tip)
		if aerr != nil {
			return Finding{}, false, steps, aerr
		}
		steps = append(steps, Step{Method: MethodAncestry, Result: why})
		if found {
			return f, true, steps, nil
		}
	} else {
		steps = append(steps, Step{Method: MethodAncestry, Result: "no done_commit recorded"})
	}
	f, ok, why, err := trailer(cand.TaskID)
	if err != nil {
		return Finding{}, false, steps, err
	}
	steps = append(steps, Step{Method: MethodTrailer, Result: why})
	if ok {
		return f, true, steps, nil
	}
	switch {
	case c.Adapter == nil:
		steps = append(steps, Step{Method: MethodAdapter, Result: "no adapter (merge_check.adapter: none)"})
	case cand.DonePR == 0:
		steps = append(steps, Step{Method: MethodAdapter, Result: "no done_pr recorded"})
	default:
		commit, at, ok, err := c.Adapter.MergedPR(ctx, cand.DonePR)
		if err != nil {
			// An adapter failure is a trace step, not a command failure:
			// the merge stays undetected and the next check retries.
			steps = append(steps, Step{Method: MethodAdapter, Result: "error: " + err.Error()})
			return Finding{}, false, steps, nil
		}
		if ok {
			steps = append(steps, Step{Method: MethodAdapter, Result: fmt.Sprintf("pr #%d merged as %s", cand.DonePR, short(commit))})
			return Finding{Method: MethodAdapter, Commit: commit, At: at.UTC()}, true, steps, nil
		}
		steps = append(steps, Step{Method: MethodAdapter, Result: fmt.Sprintf("pr #%d not merged", cand.DonePR)})
	}
	return Finding{}, false, steps, nil
}

// shortSHALen is the abbreviated commit length shown in trace steps
// (git's default abbreviation).
const shortSHALen = 7

func short(s string) string {
	if len(s) > shortSHALen {
		return s[:shortSHALen]
	}
	return s
}

// byAncestry is check 1: done_commit is an ancestor of the target. The
// recorded commit is the first commit on the target's first-parent line
// that contains it (the merge commit of a --no-ff merge), or done_commit
// itself when it sits on that line (fast-forward).
func (c Checker) byAncestry(ctx context.Context, done, tip string) (Finding, bool, string, error) {
	oid, ok, err := c.Repo.ResolveCommit(ctx, done)
	if err != nil {
		return Finding{}, false, "", fmt.Errorf("mergewatch: resolve %s: %w", done, err)
	}
	if !ok {
		return Finding{}, false, short(done) + " is not in this clone (not fetched or never pushed)", nil
	}
	anc, err := c.Repo.IsAncestor(ctx, oid, tip)
	if err != nil {
		return Finding{}, false, "", fmt.Errorf("mergewatch: ancestry: %w", err)
	}
	if !anc {
		return Finding{}, false, short(oid) + " is not on " + c.Target, nil
	}
	out, err := c.Repo.Run(ctx, gitref.Invocation{Args: []string{"rev-list", "--first-parent", "--ancestry-path", "--reverse", oid + ".." + tip}})
	if err != nil {
		return Finding{}, false, "", fmt.Errorf("mergewatch: rev-list: %w", err)
	}
	carrier := oid
	if lines := strings.Fields(string(out)); len(lines) > 0 && !onFirstParent(ctx, c.Repo, oid, tip) {
		carrier = lines[0]
	}
	at, err := committerDate(ctx, c.Repo, carrier)
	if err != nil {
		return Finding{}, false, "", err
	}
	return Finding{Method: MethodAncestry, Commit: carrier, At: at}, true, short(oid) + " reached " + c.Target + " in " + short(carrier), nil
}

// onFirstParent reports whether oid is on tip's first-parent line.
func onFirstParent(ctx context.Context, r *gitref.Repo, oid, tip string) bool {
	out, err := r.Run(ctx, gitref.Invocation{Args: []string{"rev-list", "--first-parent", tip}})
	if err != nil {
		return false
	}
	for _, l := range strings.Fields(string(out)) {
		if l == oid {
			return true
		}
	}
	return false
}

func committerDate(ctx context.Context, r *gitref.Repo, oid string) (time.Time, error) {
	out, err := r.Run(ctx, gitref.Invocation{Args: []string{"show", "-s", "--format=%cI", oid}})
	if err != nil {
		return time.Time{}, fmt.Errorf("mergewatch: committer date of %s: %w", oid, err)
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}, fmt.Errorf("mergewatch: parse date of %s: %w", oid, err)
	}
	return t.UTC(), nil
}

// byTrailer is check 2: a commit on the target whose message carries
// `Lore-Task: <id>`. The oldest such commit is the merge (later ones are
// follow-ups that mention the task again).
func (c Checker) byTrailer(ctx context.Context, id, tip string) (Finding, bool, string, error) {
	pattern := "^" + TrailerKey + ": " + regexp.QuoteMeta(id) + "$"
	out, err := c.Repo.Run(ctx, gitref.Invocation{Args: []string{"log", "--format=%H %cI", "--extended-regexp", "--grep", pattern, tip}})
	if err != nil {
		return Finding{}, false, "", fmt.Errorf("mergewatch: trailer search: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return Finding{}, false, "no " + TrailerKey + " trailer for " + id + " on " + c.Target, nil
	}
	parts := strings.Fields(last)
	if len(parts) != 2 {
		return Finding{}, false, "", fmt.Errorf("mergewatch: unexpected log line %q", last)
	}
	at, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return Finding{}, false, "", fmt.Errorf("mergewatch: parse %q: %w", parts[1], err)
	}
	return Finding{Method: MethodTrailer, Commit: parts[0], At: at.UTC()}, true, TrailerKey + " trailer in " + short(parts[0]), nil
}

// trailerIndex is every `Lore-Task: <id>` line on the target, read with
// one git log (B1-16: Sweep ran a git log over the whole history, plus a
// ref lookup, per done task: ~2500 git processes and a minute per command
// in a repo with 840 unmerged done tasks). Each id maps to its oldest
// commit, as byTrailer picks it.
type trailerIndex map[string]Finding

// trailerRecordSep starts each commit's record in the index's git log.
const trailerRecordSep = "\x1e"

// buildTrailerIndex reads the trailer lines of every commit on tip that
// has one: the same lines byTrailer's per-id --grep matches (a whole
// message line `Lore-Task: <id>`, the subject included).
func (c Checker) buildTrailerIndex(ctx context.Context, tip string) (trailerIndex, error) {
	out, err := c.Repo.Run(ctx, gitref.Invocation{Args: []string{"log", "--format=" + "%x1e%H %cI%n%B", "--extended-regexp", "--grep", "^" + TrailerKey + ": ", tip}})
	if err != nil {
		return nil, fmt.Errorf("mergewatch: trailer search: %w", err)
	}
	idx := trailerIndex{}
	prefix := TrailerKey + ": "
	for _, rec := range strings.Split(string(out), trailerRecordSep) {
		head, body, _ := strings.Cut(rec, "\n")
		parts := strings.Fields(head)
		if len(parts) != 2 {
			continue // the empty record before the first separator
		}
		var at time.Time
		parsed := false
		for _, line := range strings.Split(body, "\n") {
			id, ok := strings.CutPrefix(line, prefix)
			if !ok || id == "" {
				continue
			}
			if !parsed {
				if at, err = time.Parse(time.RFC3339, parts[1]); err != nil {
					return nil, fmt.Errorf("mergewatch: parse %q: %w", parts[1], err)
				}
				parsed = true
			}
			idx[id] = Finding{Method: MethodTrailer, Commit: parts[0], At: at.UTC()} // log is newest first: the oldest wins
		}
	}
	return idx, nil
}

// lookup is byTrailer's answer from the index.
func (idx trailerIndex) lookup(id, target string) (Finding, bool, string, error) {
	f, ok := idx[id]
	if !ok {
		return Finding{}, false, "no " + TrailerKey + " trailer for " + id + " on " + target, nil
	}
	return f, true, TrailerKey + " trailer in " + short(f.Commit), nil
}

// trailerLine matches one RFC-822-style trailer line ("Key: value").
var trailerLine = regexp.MustCompile(`^[A-Za-z0-9-]+: .+$`)

// AddTrailer returns msg with `Lore-Task: <id>` appended as a trailer,
// unchanged when it already carries exactly that trailer. Comment lines
// (git's "# …" template) stay at the end, where git strips them.
func AddTrailer(msg []byte, id string) []byte {
	want := TrailerKey + ": " + id
	var body, comments []string
	sc := bufio.NewScanner(bytes.NewReader(msg))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "#") {
			comments = append(comments, l)
			continue
		}
		if strings.TrimSpace(l) == want {
			return msg
		}
		body = append(body, l)
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return msg // an empty message aborts the commit; never make it non-empty
	}
	// Join an existing trailer block, else start one after a blank line.
	i := len(body) - 1
	for i >= 0 && trailerLine.MatchString(body[i]) {
		i--
	}
	hasBlock := i < len(body)-1 && i >= 0 && strings.TrimSpace(body[i]) == "" && i > 0
	if !hasBlock {
		body = append(body, "")
	}
	body = append(body, want)
	out := strings.Join(body, "\n") + "\n"
	if len(comments) > 0 {
		out += strings.Join(comments, "\n") + "\n"
	}
	return []byte(out)
}

// Runner runs an external program (gh) in a directory; a seam so the GitHub
// adapter is testable without gh.
type Runner func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

// ExecRunner runs the program for real: stdout is the result; a failure
// names the command and carries the program's stderr; a done ctx stops it.
func ExecRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// The Cmd is built directly rather than through exec.CommandContext:
	// path is the absolute program exec.LookPath resolved, every argument
	// is its own argv entry (no shell parses them), and the goroutine below
	// kills it when ctx ends, as CommandContext did (adapters/gitref).
	var out, stderr bytes.Buffer
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, args...), Dir: dir, Stdout: &out, Stderr: &stderr}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// A failed kill means the process already exited; Wait reports it.
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out.Bytes(), nil
}

// GitHub is the `merge_check.adapter: github` adapter: it asks `gh` (which
// must be installed and logged in) about a pull request of the repository
// in Dir.
type GitHub struct {
	Dir string
	Run Runner
}

// ghBinary is the GitHub CLI program.
const ghBinary = "gh"

// MergedPR implements Adapter.
func (g GitHub) MergedPR(ctx context.Context, n int) (string, time.Time, bool, error) {
	run := g.Run
	if run == nil {
		run = ExecRunner
	}
	out, err := run(ctx, g.Dir, ghBinary, "pr", "view", strconv.Itoa(n), "--json", "mergedAt,mergeCommit")
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("mergewatch: github adapter: %w", err)
	}
	var v struct {
		MergedAt    *time.Time `json:"mergedAt"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", time.Time{}, false, fmt.Errorf("mergewatch: github adapter: decode: %w", err)
	}
	if v.MergedAt == nil || v.MergedAt.IsZero() || v.MergeCommit == nil || v.MergeCommit.Oid == "" {
		return "", time.Time{}, false, nil
	}
	return v.MergeCommit.Oid, v.MergedAt.UTC(), true, nil
}

// ---- the lazy check (Sweep) ----

// remoteRefPrefix is where git keeps a remote's fetched branches.
const remoteRefPrefix = "refs/remotes/"

// Target is the fetched default branch the check reads, e.g.
// Target("origin", "main") = "refs/remotes/origin/main".
func Target(remote, branch string) string {
	return remoteRefPrefix + remote + "/" + branch
}

// AdapterFor is the merge_check.adapter of the resolved config: GitHub
// (`gh`, run in dir) for MergeCheckGitHub, nil (no adapter) for none or
// unset. The bool says whether an adapter is set.
func AdapterFor(a orgconf.MergeCheckAdapter, dir string) (Adapter, bool) {
	if a == orgconf.MergeCheckGitHub {
		return GitHub{Dir: dir}, true
	}
	return nil, false
}

// Found is one positive Sweep result: the task and how its merge was found.
type Found struct {
	TaskID  string
	Finding Finding
}

// Problem is one candidate whose check failed (a git error): the host
// prints it as a warning and the next command retries. It never fails the
// command that ran the check (F9).
type Problem struct {
	TaskID string
	Err    error
}

// Sweep checks every candidate and returns the merges found, in input
// order. here is this checkout's repo in the form Candidate.Repo uses
// ("" = unknown: every candidate is checked). Without an adapter a
// candidate whose Repo is set and differs from here is skipped (its merge
// is found in its own repo's checkout); with an adapter every candidate is
// asked, since the code host knows every pull request. A target branch
// that was never fetched ends the sweep with nothing found and no problem
// (nothing can be known yet). Sweep writes nothing.
func (c Checker) Sweep(ctx context.Context, here string, cands []Candidate) ([]Found, []Problem) {
	var found []Found
	var probs []Problem
	// The target and the trailer index are read once for every candidate
	// (B1-16), when the first candidate needs them; a failure is each
	// candidate's problem, as a per-candidate Check reported it.
	var (
		tip, tipDone = "", false
		tipErr       error
		idx          trailerIndex
		idxErr       error
		idxDone      bool
	)
	trailer := func(id string) (Finding, bool, string, error) {
		if !idxDone {
			idx, idxErr = c.buildTrailerIndex(ctx, tip)
			idxDone = true
		}
		if idxErr != nil {
			return Finding{}, false, "", idxErr
		}
		return idx.lookup(id, c.Target)
	}
	for _, cand := range cands {
		if c.Adapter == nil && cand.Repo != "" && here != "" && cand.Repo != here {
			continue
		}
		if !tipDone {
			tip, tipErr = c.tip(ctx)
			tipDone = true
		}
		var (
			f   Finding
			ok  bool
			err = tipErr
		)
		if err == nil {
			f, ok, _, err = c.checkAt(ctx, cand, tip, trailer)
		}
		if errors.Is(err, ErrNoTarget) {
			return nil, nil
		}
		if err != nil {
			probs = append(probs, Problem{TaskID: cand.TaskID, Err: err})
			continue
		}
		if ok {
			found = append(found, Found{TaskID: cand.TaskID, Finding: f})
		}
	}
	return found, probs
}

// lineClock is the time format of the merged line (v0.1.17 reportMerged).
const lineClock = "15:04"

// Line is the one line a host prints for a found merge: "✓ <id> merged
// <time> (found by: <method>, <commit>)" when recording it succeeded
// (recErr nil), else that it was found but not recorded and the next
// command retries (v0.1.17 reportMerged, byte for byte).
func Line(f Found, recErr error) string {
	at, how, commit := f.Finding.At.UTC().Format(lineClock), f.Finding.Method, short(f.Finding.Commit)
	if recErr != nil {
		return fmt.Sprintf("lore: %s merged %s (found by: %s, %s) but it could not be recorded: %v; the next lore command retries", f.TaskID, at, how, commit, recErr)
	}
	return fmt.Sprintf("✓ %s merged %s (found by: %s, %s)", f.TaskID, at, how, commit)
}

// ProblemLine is the warning line for a failed check (v0.1.17 printed
// "lore merged check: <err>" on stderr).
func ProblemLine(p Problem) string {
	return "lore merged check: " + p.TaskID + ": " + p.Err.Error()
}

// MergedUpdatedAt is the updated_at a merged stamp on a branch row writes:
// the later of the row's own and the merge time, a pure function of the row
// and the finding so two clones that find the same merge write the same
// bytes.
func MergedUpdatedAt(old, merged time.Time) time.Time {
	if merged.After(old) {
		return merged.UTC()
	}
	return old.UTC()
}
