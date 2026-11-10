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
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if _, err = g.run(ctx, call{dir: work, args: []string{"ls-remote", "--heads", "--", RemoteName}}); err != nil {
		if msg := strings.TrimSpace(stderrOf(err)); msg != "" {
			return errs.New(errs.StoreUnreachable, lastLine(msg))
		}
		return errs.Wrap(errs.StoreUnreachable, "the store's remote did not answer", err)
	}
	return nil
}

// lastLine is the last non-empty line of s (git's error is its last line).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
