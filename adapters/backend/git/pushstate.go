package git

// pushstate.go — where a store clone stands against its remote, read from
// the clone alone (no network): how many local commits the remote has not
// confirmed and how old the oldest is, how many remote commits the last
// fetch brought that are not merged, and when lore last pushed (the
// remote-tracking ref's reflog entries "update by push"). `lore store
// doctor` and `lore sync status` print it per store.
//
//	work ─▶ HEAD branch ─▶ rev-list --left-right --count HEAD...origin/<b>
//	     ─▶ log origin/<b>..HEAD (oldest unpushed commit time)
//	     ─▶ <git dir>/logs/refs/remotes/origin/<b> (newest "update by push")

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/fsreason"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// PushState is one clone's standing against its remote.
type PushState struct {
	// Branch is the worktree's branch.
	Branch string
	// Tracked is false when the clone has no remote-tracking ref for the
	// branch (never fetched or pushed): Ahead counts every local commit.
	Tracked bool
	// Ahead is the local commits the remote has not confirmed; Behind the
	// remote commits the last fetch saw that are not merged yet.
	Ahead, Behind int
	// OldestUnpushed is the commit time of the oldest of Ahead (zero when
	// Ahead is 0).
	OldestUnpushed time.Time
	// LastPush is when lore last pushed this branch (zero = no push on
	// record in this clone).
	LastPush time.Time
}

// pushReflogSubject is git's reflog subject of a remote-tracking ref
// moved by a push.
const pushReflogSubject = "update by push"

// ReadPushState reads work's PushState with the git program bin ("" =
// the default). It only reads.
func ReadPushState(ctx context.Context, bin, work string) (PushState, error) {
	if bin == "" {
		bin = DefaultBinary
	}
	p, err := lookGit(bin)
	if err != nil {
		return PushState{}, err
	}
	g := runner{bin: p}
	text := func(args ...string) (string, error) {
		out, err := g.run(ctx, call{dir: work, args: args})
		return strings.TrimSpace(string(out)), err
	}
	var st PushState
	if st.Branch, err = text("symbolic-ref", "--quiet", "--short", "HEAD"); err != nil {
		return PushState{}, errs.Wrap(errs.Internal, "git: the store clone's worktree is not on a branch", err)
	}
	tracking := "refs/remotes/" + RemoteName + "/" + st.Branch
	if _, err = text("rev-parse", "--verify", "--quiet", tracking); err == nil {
		st.Tracked = true
	}
	rangeOf := "HEAD"
	if st.Tracked {
		counts, cerr := text("rev-list", "--left-right", "--count", "HEAD..."+tracking)
		if cerr != nil {
			return PushState{}, errs.Wrap(errs.Internal, "git: count the store clone's unpushed commits", cerr)
		}
		a, b, _ := strings.Cut(counts, "\t")
		st.Ahead, _ = strconv.Atoi(strings.TrimSpace(a))
		st.Behind, _ = strconv.Atoi(strings.TrimSpace(b))
		rangeOf = tracking + "..HEAD"
	} else {
		n, cerr := text("rev-list", "--count", "HEAD")
		if cerr != nil {
			return PushState{}, errs.Wrap(errs.Internal, "git: count the store clone's commits", cerr)
		}
		st.Ahead, _ = strconv.Atoi(n)
	}
	if st.Ahead > 0 {
		times, lerr := text("log", "--format=%ct", rangeOf)
		if lerr != nil {
			return PushState{}, errs.Wrap(errs.Internal, "git: read the store clone's unpushed commits", lerr)
		}
		if lines := strings.Fields(times); len(lines) > 0 {
			if sec, perr := strconv.ParseInt(lines[len(lines)-1], 10, 64); perr == nil {
				st.OldestUnpushed = time.Unix(sec, 0)
			}
		}
	}
	common, err := text("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return PushState{}, errs.Wrap(errs.Internal, "git: locate the store clone's git data", err)
	}
	st.LastPush, err = lastPush(filepath.Join(common, "logs", filepath.FromSlash(tracking)))
	if err != nil {
		return PushState{}, errs.Wrap(errs.Internal, "git: read the store clone's push log", err)
	}
	return st, nil
}

// lastPush is the time of the newest "update by push" line of a reflog
// file (zero when there is none, or no file).
func lastPush(path string) (time.Time, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = f.Close() }()
	var last time.Time
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// <old> <new> <name> <<email>> <unix> <tz>\t<subject>
		head, subject, ok := strings.Cut(sc.Text(), "\t")
		if !ok || !strings.HasPrefix(subject, pushReflogSubject) {
			continue
		}
		fields := strings.Fields(head)
		if len(fields) < 2 {
			continue
		}
		sec, perr := strconv.ParseInt(fields[len(fields)-2], 10, 64)
		if perr != nil {
			continue
		}
		if t := time.Unix(sec, 0); t.After(last) {
			last = t
		}
	}
	return last, sc.Err()
}

// probeTimeout bounds ProbeRemote, so a remote that never answers is
// reported unreachable instead of hanging `lore store doctor`.
const probeTimeout = 20 * time.Second

// ProbeRemote asks the store clone's remote for its branches (git
// ls-remote, through the WithRoute route of remote, the store url): nil
// when it answered. It writes nothing.
func ProbeRemote(ctx context.Context, work, remote string, opts ...Option) error {
	cfg := build(opts)
	p, err := lookGit(cfg.bin)
	if err != nil {
		return err
	}
	g := runner{bin: p, remote: remote}
	if cfg.route != nil {
		g.routeArgs, g.routeEnv = cfg.route(ctx, remote)
	}
	if cfg.accessNote != nil {
		g.accessNote = cfg.accessNote(remote)
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if _, err = g.run(ctx, call{dir: work, args: []string{"ls-remote", "--heads", "--", RemoteName}}); err != nil {
		return probeError(remote, stderrOf(err), g.accessNote, FetchedOnce(work), err)
	}
	return nil
}

// probeError is ProbeRemote's typed answer: git's whole telling line, never
// the tail of its advice (B3-12 printed "and the repository exists."),
// the url with its credentials masked, and the next step for that kind of
// failure (gitfault: no answer, no repository there, credentials refused).
// fetchedBefore (the clone fetched from remote once) turns "no repository"
// and "denied" into the lost-access text (LostAccessText, F-11).
func probeError(remote, stderr, accessNote string, fetchedBefore bool, cause error) error {
	shown := repourl.Mask(remote)
	kind := gitfault.ClassifyAt(stderr, fsreason.GitPathState)
	msg := gitfault.Describe(stderr, shown, fsreason.GitPathState)
	if strings.TrimSpace(stderr) == "" {
		msg = gitfault.Summary(kind, shown) + " (" + repourl.MaskText(cause.Error()) + ")"
	}
	if accessNote != "" {
		msg += " — " + accessNote
	}
	hint := ProbeHint(kind, shown)
	if fetchedBefore && (kind == gitfault.Missing || kind == gitfault.Denied) {
		msg = LostAccessText(shown) + " (" + msg + ")"
		hint = LostAccessHint(storeNamePlaceholder)
	}
	return errs.Wrap(errs.StoreUnreachable, msg, &FaultError{Kind: kind, Text: msg}).WithHint(hint)
}

// FaultError is the cause of ProbeRemote's error: the kind of failure, so
// a caller (`lore store doctor`) can tell "no answer" from "no repository
// there" without reading text. Text is the message already shown.
type FaultError struct {
	Kind gitfault.Kind
	Text string
}

// Error is the message the wrapping error already carries.
func (e *FaultError) Error() string { return e.Text }

// ProbeFault is the kind of a ProbeRemote error ("" for nil or another
// error).
func ProbeFault(err error) gitfault.Kind {
	var f *FaultError
	if errors.As(err, &f) {
		return f.Kind
	}
	return ""
}

// LostAccessText is what a store that served this clone before and now
// answers "no repository there" or "access denied" most likely means:
// hosts such as GitHub answer "not found" for a private repo an account
// may no longer read, so "check the address" would send the person after
// a typo that is not there (F-11). It names the url (masked).
func LostAccessText(shown string) string {
	return "the store refused this machine: " + shown + " served this store before and now answers that there is no repository or no access, which is what a host says once your access was removed (or the store was moved or deleted)"
}

// storeNamePlaceholder stands for the store's name in a hint written
// where only its url is known.
const storeNamePlaceholder = "<store>"

// LostAccessHint is the next step after LostAccessText: ask, and the way
// for a store that only moved.
func LostAccessHint(storeName string) string {
	return "ask an admin of the store whether you still have access; nothing written here is lost (it stays in the local copy, but cannot be delivered without access); if the store moved: lore store set-url --name " + storeName + " --url <url>"
}

// ProbeHint is the next step for a store remote that failed a probe, by
// kind: wait (the commits stay in the local clone), fix the address, or
// fix the credentials.
func ProbeHint(k gitfault.Kind, shown string) string {
	switch k {
	case gitfault.Missing:
		return "check the store's address (" + shown + "): `lore store set-url --name <store> --url <url>` fixes a wrong one; local commits stay in the clone meanwhile"
	case gitfault.Unreadable:
		return "the address is right; check the permissions of the folder it names (`ls -ld`); local commits stay in the clone meanwhile"
	case gitfault.Denied:
		return "check which account this machine's key or token logs in as and that it may read " + shown + "; local commits stay in the clone meanwhile"
	}
	return "lore keeps working offline: local commits stay in the clone and the next lore command that reaches " + shown + " pushes them, or run `lore sync`"
}
