package probe

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// Git program and constants of the remote probes.
const (
	gitBin = "git"
	// ProbeRefPrefix is the ref the write test names (ONB 4.4): a dry run
	// never creates it; the name says what it was if a host logs it.
	ProbeRefPrefix = "refs/lore/probe/"
	// probeTmpPattern names the write and marker tests' temp bare repos.
	probeTmpPattern = "lore-probe-"
	// probeIdentity signs the write test's empty commit; it never leaves
	// the machine (dry run: no objects are sent).
	probeName  = "lore probe"
	probeEmail = "probe@lore.invalid"
	// headRef is the remote's default branch for the marker fetch.
	headRef = "HEAD"
)

// gitSpec builds a git child for route: the alias rewrite options first
// (Q5 a), then args; never a prompt (GIT_TERMINAL_PROMPT=0, ssh BatchMode),
// English messages for classification (LC_ALL=C).
func (p *Prober) gitSpec(route Route, dir string, args ...string) cmdSpec {
	return cmdSpec{
		bin:  gitBin,
		args: append(route.GitArgs(), args...),
		dir:  dir,
		env: []string{
			envGitPrompt + "=0",
			envGCMInteractive + "=never",
			envLCAll + "=C",
			envGitSSHCommand + "=" + route.GitSSHCommand(p.o.Home),
		},
	}
}

// ReadResult is the store.read probe (ONB 4.4).
type ReadResult struct {
	Outcome
	// Heads are the branch names the remote lists (refs/heads/ stripped).
	Heads []string `json:"heads,omitempty"`
	// Empty: the remote answered with no branch (a new, empty repo).
	Empty bool `json:"empty,omitempty"`
}

// ReadRemote tells whether url can be read via route and which branches
// it has: one fetch of the branch tips (depth 1, no blobs; snapshot.go),
// shared with StoreMarker and RootFiles when ctx carries a RemoteMemo.
// Classes: ok, no_access (not found or hidden), auth_denied, host_key,
// unreachable, timeout, tool_missing, bad_input, failed. A remote without
// a default branch is ok + Empty.
func (p *Prober) ReadRemote(ctx context.Context, rawURL string, route Route) ReadResult {
	u, bad := p.checkRemote(rawURL, route)
	if bad != nil {
		return ReadResult{Outcome: *bad}
	}
	s := p.snapshot(ctx, u, route)
	if s.Class != ClassOK {
		return ReadResult{Outcome: s.Outcome}
	}
	// The read detail is the caller's ("read ok"); the empty note is kept
	// only as the Empty flag.
	return ReadResult{Outcome: Outcome{Class: ClassOK, Took: s.Took}, Heads: s.heads, Empty: s.empty || len(s.heads) == 0}
}

// reMachineID is what a machine id may hold inside a ref name.
var reMachineID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// WriteResult is the store.write probe (ONB 4.4, Q6: the dry-run push is
// the truth; gh's permissions.push is only a hint, see GHRepo).
type WriteResult struct {
	Outcome
}

// WriteRemote is the write test that never writes (ONB 4.4): in a temp
// bare repo under Options.TmpDir it makes one empty commit and runs
// git push --dry-run url <commit>:refs/lore/probe/<machineID>. GitHub checks
// push rights when git-receive-pack connects, before the dry run stops, so
// a refusal means no write access; success sends no objects and makes no
// ref. The temp repo is removed before return. Classes: ok, no_write,
// no_access, auth_denied, host_key, unreachable, timeout, tool_missing,
// bad_input, failed.
func (p *Prober) WriteRemote(ctx context.Context, rawURL string, route Route, machineID string) WriteResult {
	start := time.Now()
	u, bad := p.checkRemote(rawURL, route)
	if bad != nil {
		return WriteResult{Outcome: *bad}
	}
	if !reMachineID.MatchString(machineID) {
		return WriteResult{Outcome: Outcome{Class: ClassBadInput, Detail: "machine id must be 1-64 letters, digits, '_' or '-'"}}
	}
	return p.sharedWrite(ctx, u, route, machineID, func() WriteResult { return p.dryRunPush(ctx, u, route, machineID, start) })
}

// dryRunPush is WriteRemote's push, run once per url, route and machine
// id per run (sharedWrite).
func (p *Prober) dryRunPush(ctx context.Context, u string, route Route, machineID string, start time.Time) WriteResult {
	cctx, done, ok := p.slot(ctx, p.o.GitTimeout)
	defer done()
	if !ok {
		return WriteResult{Outcome: timedOut(start)}
	}
	dir, o := p.tempBare(cctx)
	if o != nil {
		o.Took = time.Since(start)
		return WriteResult{Outcome: *o}
	}
	defer os.RemoveAll(dir)
	commit, o := p.emptyCommit(cctx, dir)
	if o != nil {
		o.Took = time.Since(start)
		return WriteResult{Outcome: *o}
	}
	r := p.run(cctx, p.gitSpec(route, dir, "push", "--dry-run", "--porcelain", u, commit+":"+ProbeRefPrefix+machineID))
	if !r.ok() {
		o := failure(r, gitBin, classifyRemote)
		o.Took = time.Since(start)
		return WriteResult{Outcome: o}
	}
	// --porcelain prints "!" for a rejected ref even with exit 0 on some
	// hosts' replies; treat it as a refusal.
	for l := range strings.SplitSeq(r.stdout, "\n") {
		if strings.HasPrefix(l, "!\t") {
			return WriteResult{Outcome: Outcome{Class: ClassNoWrite, Detail: Sanitize(l), Took: time.Since(start)}}
		}
	}
	return WriteResult{Outcome: Outcome{Class: ClassOK, Took: time.Since(start)}}
}

// MarkerResult is the store.marker probe (ONB 4.4).
type MarkerResult struct {
	Outcome
	// Present: lore-store.yaml is at the root of the default branch.
	Present bool `json:"present"`
	// EmptyRepo: the remote has no default branch yet (Present is false).
	EmptyRepo bool `json:"empty_repo,omitempty"`
}

// StoreMarker is RootFiles for rowfs.MarkerFile (lore-store.yaml).
func (p *Prober) StoreMarker(ctx context.Context, rawURL string, route Route) MarkerResult {
	r := p.RootFiles(ctx, rawURL, route, rowfs.MarkerFile)
	return MarkerResult{Outcome: r.Outcome, Present: len(r.Found) == 1, EmptyRepo: r.EmptyRepo}
}

// RootFilesResult is which of the asked names sit at the root of a
// remote's default branch.
type RootFilesResult struct {
	Outcome
	// Found are the asked names present, in the order asked.
	Found []string `json:"found,omitempty"`
	// EmptyRepo: the remote has no default branch yet (Found is empty).
	EmptyRepo bool `json:"empty_repo,omitempty"`
}

// RootFiles lists which of names are in the root tree of the remote's
// default branch (store.marker: lore-store.yaml, or an org store's
// lore-org.yaml). It reads the same one-fetch snapshot as ReadRemote
// (snapshot.go), shared when ctx carries a RemoteMemo. Classes: as
// ReadRemote; an empty remote is ok + EmptyRepo.
func (p *Prober) RootFiles(ctx context.Context, rawURL string, route Route, names ...string) RootFilesResult {
	u, bad := p.checkRemote(rawURL, route)
	if bad != nil {
		return RootFilesResult{Outcome: *bad}
	}
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "/\\") || strings.HasPrefix(n, "-") {
			return RootFilesResult{Outcome: Outcome{Class: ClassBadInput, Detail: "a root file name may not be empty, hold a slash or start with '-'"}}
		}
	}
	snap := p.snapshot(ctx, u, route)
	out := RootFilesResult{Outcome: snap.Outcome, EmptyRepo: snap.empty}
	if snap.Class != ClassOK {
		return out
	}
	for _, n := range names {
		if snap.root[n] {
			out.Found = append(out.Found, n)
		}
	}
	return out
}

// checkRemote validates a remote probe's inputs.
func (p *Prober) checkRemote(rawURL string, route Route) (string, *Outcome) {
	u, bad := cleanURL(rawURL)
	if bad != nil {
		return "", bad
	}
	if err := route.Validate(); err != nil {
		return "", &Outcome{Class: ClassBadInput, Detail: err.Error()}
	}
	return u, nil
}

// tempBare makes an empty bare repo under Options.TmpDir.
func (p *Prober) tempBare(ctx context.Context) (string, *Outcome) {
	if err := os.MkdirAll(p.o.TmpDir, 0o700); err != nil {
		return "", &Outcome{Class: ClassFailed, Detail: "cannot create the probe folder " + p.o.TmpDir}
	}
	dir, err := os.MkdirTemp(p.o.TmpDir, probeTmpPattern)
	if err != nil {
		return "", &Outcome{Class: ClassFailed, Detail: "cannot create a temp repo in " + p.o.TmpDir}
	}
	r := p.run(ctx, p.gitSpec(Route{}, dir, "init", "--quiet", "--bare", "."))
	if !r.ok() {
		_ = os.RemoveAll(dir)
		o := failure(r, gitBin, func(string) Class { return ClassFailed })
		return "", &o
	}
	return dir, nil
}

// emptyCommit writes the empty tree and one commit on it into dir.
func (p *Prober) emptyCommit(ctx context.Context, dir string) (string, *Outcome) {
	spec := p.gitSpec(Route{}, dir, "mktree")
	tree := p.run(ctx, spec)
	if !tree.ok() {
		o := failure(tree, gitBin, func(string) Class { return ClassFailed })
		return "", &o
	}
	spec = p.gitSpec(Route{}, dir, "commit-tree", strings.TrimSpace(tree.stdout), "-m", "lore write probe")
	spec.env = append(spec.env,
		"GIT_AUTHOR_NAME="+probeName, "GIT_AUTHOR_EMAIL="+probeEmail,
		"GIT_COMMITTER_NAME="+probeName, "GIT_COMMITTER_EMAIL="+probeEmail)
	c := p.run(ctx, spec)
	if !c.ok() {
		o := failure(c, gitBin, func(string) Class { return ClassFailed })
		return "", &o
	}
	return strings.TrimSpace(c.stdout), nil
}

// timedOut is the outcome of a probe whose limiter wait outlived ctx.
func timedOut(start time.Time) Outcome {
	return Outcome{Class: ClassTimeout, Detail: "waited too long for a free probe slot", Took: time.Since(start)}
}

// GitIdentity is git's user.name and user.email (ONB 4.2 prefill,
// identity.git_email).
type GitIdentity struct {
	Outcome
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// GlobalIdentity reads git config --global user.name and user.email
// (an unset key is "", not an error). Classes: ok, timeout, tool_missing, failed.
func (p *Prober) GlobalIdentity(ctx context.Context) GitIdentity {
	return p.identity(ctx, "", "--global")
}

// RepoIdentity reads the user.name and user.email git uses in dir (every
// scope: the repo's own config, includeIf, global), for the
// identity.git_email check (ONB 6.1).
func (p *Prober) RepoIdentity(ctx context.Context, dir string) GitIdentity {
	if dir == "" {
		return GitIdentity{Outcome: Outcome{Class: ClassBadInput, Detail: "no repo folder given"}}
	}
	return p.identity(ctx, dir)
}

// git config keys read by the identity probes.
const (
	keyUserName  = "user.name"
	keyUserEmail = "user.email"
)

func (p *Prober) identity(ctx context.Context, dir string, scope ...string) GitIdentity {
	start := time.Now()
	cctx, done, ok := p.slot(ctx, p.o.GitTimeout)
	defer done()
	if !ok {
		return GitIdentity{Outcome: timedOut(start)}
	}
	out := GitIdentity{}
	for _, key := range []string{keyUserName, keyUserEmail} {
		args := append(append([]string{"config"}, scope...), "--get", key)
		r := p.run(cctx, p.gitSpec(Route{}, dir, args...))
		switch {
		case r.ok():
		case !r.timedOut && !r.missing && r.exit == 1:
			continue // git config: exit 1 is "key not set"
		default:
			o := failure(r, gitBin, func(string) Class { return ClassFailed })
			o.Took = time.Since(start)
			return GitIdentity{Outcome: o}
		}
		v := strings.TrimSpace(r.stdout)
		if key == keyUserName {
			out.Name = v
		} else {
			out.Email = v
		}
	}
	out.Outcome = Outcome{Class: ClassOK, Took: time.Since(start)}
	return out
}
