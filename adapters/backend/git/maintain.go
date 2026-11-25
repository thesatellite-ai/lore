package git

// maintain.go — lore keeps its store clones packed (store growth SG-2,
// devco:D-996). A busy store gets one commit per write; git's own auto gc
// never reaches its thresholds there (6,700 loose objects, 50 packs), so a
// clone of a few MB of rows grew to tens of MB of loose trees, and
// path-limited logs (row history, the authority replay) ran without a
// commit-graph. Flow:
//
//	any command ─► (after its output) MaintenanceDue? ─► a detached
//	               `lore store maintain --all --due` child (wire)
//	MaintainClone ─► try maint.lock (busy: skip, the stamp stays)
//	              ─► under a try of the store lock (busy: skip this part):
//	                 gc.auto=0 + maintenance.auto=false, pack-refs,
//	                 bounded backup refs
//	              ─► repack (loose > 500 or packs > 5), prune of loose
//	                 objects older than two weeks, commit-graph with
//	                 changed-path filters
//	              ─► .git/lore/maint-stamp = now
//
// Why the object work is outside the store lock: a repack of a big clone
// takes seconds to minutes, and a writer waits for the store lock up to
// store.lock_timeout before it fails; git's repack, prune (with an expiry)
// and commit-graph are made to run beside writers (git's own auto gc runs
// in the middle of commands). Only the steps that rewrite refs or the
// config take the store lock, and only when it is free at once: the pass
// never makes a command wait. maint.lock keeps two passes on one clone
// from running together; the second one skips.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Maintenance files in a clone's .git/lore folder.
const (
	// MaintStampFile holds when the last finished pass ran (RFC 3339 UTC).
	MaintStampFile = "maint-stamp"
	// MaintTryFile holds when the last pass started; a pass that fails is
	// not started again by every command, only after maintRetry.
	MaintTryFile = "maint-try"
	// MaintLockName keeps two passes on one clone apart.
	MaintLockName = "maint.lock"
)

// Maintenance thresholds.
const (
	// DefaultMaintEvery is how often a clone gets a pass
	// (store.maintenance_every default).
	DefaultMaintEvery = 24 * time.Hour
	// MinMaintEvery is the smallest store.maintenance_every lore accepts.
	MinMaintEvery = time.Hour
	// maintRetry is how long after a started pass the next automatic one
	// may start when that one never finished (a crash or a git error).
	maintRetry = time.Hour
	// repackLoose and repackPacks decide the repack: more loose objects or
	// more packs than this.
	repackLoose = 500
	repackPacks = 5
	// pruneExpiry is git prune's expiry: never "now", so the fresh loose
	// objects of a write running beside the pass always survive.
	pruneExpiry = "2.weeks.ago"
	// maintLockWait is the try of both locks: taken at once or skipped.
	maintLockWait = time.Nanosecond
)

// gitAutoOff is the clone config that keeps git's own background gc and
// maintenance from starting inside one of lore's commands; run.go also
// passes it on every call, so clones made before this config was written
// get it too.
var gitAutoOff = [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}}

// CloneSize is what a clone's git directory holds on disk.
type CloneSize struct {
	// GitBytes is the whole .git folder.
	GitBytes int64 `json:"git_bytes"`
	// Packs and PackBytes are the pack files (.pack only).
	Packs     int   `json:"packs"`
	PackBytes int64 `json:"pack_bytes"`
	// Loose and LooseBytes are the loose objects.
	Loose      int   `json:"loose"`
	LooseBytes int64 `json:"loose_bytes"`
}

// NeedsRepack reports whether a pass would repack a clone of this size.
func (s CloneSize) NeedsRepack() bool { return s.Loose > repackLoose || s.Packs > repackPacks }

// MaintOptions configure one MaintainClone.
type MaintOptions struct {
	// Every is the interval; 0 = DefaultMaintEvery.
	Every time.Duration
	// Force runs the pass even when the last one is newer than Every
	// (`lore store maintain` without --due).
	Force bool
	// Gentle runs git with one pack thread (the automatic background pass,
	// so it never takes the cores a person's command needs).
	Gentle bool
	// Now is the clock (tests); zero = time.Now.
	Now time.Time
}

// Maint skip reasons.
const (
	// MaintSkipNotDue: the last pass is newer than the interval.
	MaintSkipNotDue = "not_due"
	// MaintSkipBusy: another pass holds maint.lock.
	MaintSkipBusy = "busy"
)

// MaintResult is what one MaintainClone did.
type MaintResult struct {
	// Skipped is "" when the pass ran, else MaintSkipNotDue or MaintSkipBusy.
	Skipped string
	Before  CloneSize
	After   CloneSize
	// Repacked, CommitGraph and Refs say which steps ran (Refs: the store
	// lock was free, so pack-refs, the config and the backup refs ran).
	Repacked    bool
	CommitGraph bool
	Refs        bool
	// Took is the wall time of the pass.
	Took time.Duration
}

func (o MaintOptions) now() time.Time {
	if o.Now.IsZero() {
		return time.Now()
	}
	return o.Now
}

func (o MaintOptions) every() time.Duration {
	if o.Every <= 0 {
		return DefaultMaintEvery
	}
	return o.Every
}

// LastMaintenance is when the clone at dir last finished a pass; false
// when it never did (a clone made before passes existed).
func LastMaintenance(dir string) (time.Time, bool) {
	return readStamp(filepath.Join(StateDirOf(dir), MaintStampFile))
}

// MaintenanceTried is when the last pass on the clone at dir started;
// false when none did.
func MaintenanceTried(dir string) (time.Time, bool) {
	return readStamp(filepath.Join(StateDirOf(dir), MaintTryFile))
}

// MaintenanceDue reports whether the clone at dir wants a pass now: its
// last pass is older than every (or there was none), and no pass started
// within the last hour without finishing. It only reads two small files,
// so every command can ask.
func MaintenanceDue(dir string, every time.Duration, now time.Time) bool {
	if every <= 0 {
		every = DefaultMaintEvery
	}
	if last, ok := LastMaintenance(dir); ok && now.Sub(last) < every {
		return false
	}
	if tried, ok := readStamp(filepath.Join(StateDirOf(dir), MaintTryFile)); ok && now.Sub(tried) < min(every, maintRetry) {
		return false
	}
	return true
}

// MaintainClone runs one maintenance pass on the store clone at dir (see
// the file comment). It never waits for a lock: a pass already running
// makes it return Skipped busy, and a held store lock only skips the ref
// and config steps. Errors are git failures of the object steps; the
// stamp then stays, and the next pass tries again after maintRetry.
func MaintainClone(ctx context.Context, dir string, opt MaintOptions) (MaintResult, error) {
	start := time.Now()
	now := opt.now()
	p := PathsOf(dir)
	if !isFile(filepath.Join(p.GitDir, headFileName)) {
		return MaintResult{}, errs.New(errs.NotFound, "git: "+dir+" is not a store clone (no .git/HEAD)")
	}
	if !opt.Force && !MaintenanceDue(dir, opt.every(), now) {
		return MaintResult{Skipped: MaintSkipNotDue}, nil
	}
	if err := os.MkdirAll(p.StateDir, cloneDirMode); err != nil {
		return MaintResult{}, errs.FileIO("create the clone state folder", p.StateDir, err)
	}
	release, err := rowfs.LockFile(ctx, filepath.Join(p.StateDir, MaintLockName), maintLockWait)
	if err != nil {
		if errs.CodeOf(err) == errs.StoreLocked {
			return MaintResult{Skipped: MaintSkipBusy}, nil
		}
		return MaintResult{}, err
	}
	defer func() { _ = release() }() // the kernel drops it at exit anyway
	writeStamp(filepath.Join(p.StateDir, MaintTryFile), now)
	b, err := sharedBackend(dir, false)
	if err != nil {
		return MaintResult{}, err
	}
	res := MaintResult{}
	if res.Before, err = SizeOf(dir); err != nil {
		return res, err
	}
	res.Refs = b.maintainRefs(ctx, dir)
	prefix := []string{}
	if opt.Gentle {
		prefix = []string{"-c", "pack.threads=1"}
	}
	if res.Before.NeedsRepack() {
		if err := b.repack(ctx, dir, prefix); err != nil {
			return res, err
		}
		res.Repacked = true
		if _, err := b.git.run(ctx, call{dir: dir, args: []string{"prune", "--expire=" + pruneExpiry}}); err != nil {
			return res, errs.Wrap(errs.Internal, "git: prune old loose objects", err)
		}
	}
	if _, err := b.git.run(ctx, call{dir: dir, args: append(prefix, "commit-graph", "write", "--reachable", "--changed-paths", "--split")}); err != nil {
		return res, errs.Wrap(errs.Internal, "git: write the commit-graph", err)
	}
	res.CommitGraph = true
	writeStamp(filepath.Join(p.StateDir, MaintStampFile), now)
	if res.After, err = SizeOf(dir); err != nil {
		return res, err
	}
	res.Took = time.Since(start)
	return res, nil
}

// repack packs the loose objects and rolls small packs into bigger ones
// (--geometric: a big pack is not rewritten for a few new objects). A git
// older than 2.33 has no --geometric; it gets a full repack instead.
func (b *Backend) repack(ctx context.Context, dir string, prefix []string) error {
	if _, err := b.git.run(ctx, call{dir: dir, args: append(append([]string(nil), prefix...), "repack", "-d", "-l", "-q", "--geometric=2")}); err == nil {
		return nil
	}
	if _, err := b.git.run(ctx, call{dir: dir, args: append(append([]string(nil), prefix...), "repack", "-a", "-d", "-l", "-q")}); err != nil {
		return errs.Wrap(errs.Internal, "git: repack the store clone", err)
	}
	return nil
}

// maintainRefs runs the steps that rewrite refs or the config, under the
// store lock when it is free at once; false when it was held (a command
// is writing: these steps wait for the next pass).
func (b *Backend) maintainRefs(ctx context.Context, dir string) bool {
	held, release, err := TryLockClone(ctx, dir, maintLockWait)
	if err != nil {
		return false
	}
	defer release()
	setGitAutoOff(held, b.git, dir)
	_, _ = b.git.run(held, call{dir: dir, args: []string{"pack-refs", "--all"}}) // best effort: loose refs work as well
	pruneRefs(held, b.git, dir, BackupRefPrefix, MigrateBackupsKept)
	pruneRefs(held, b.git, dir, compactBackupPrefix, CompactBackupsKept)
	return true
}

// setGitAutoOff writes gc.auto=0 and maintenance.auto=false into the
// clone's config when they are not there yet (a config write takes git's
// config lock, so it is not repeated on every pass). Best effort: run.go
// passes both on every call anyway.
func setGitAutoOff(ctx context.Context, g runner, dir string) {
	for _, kv := range gitAutoOff {
		out, err := g.run(ctx, call{dir: dir, args: []string{"config", "--local", "--get", kv[0]}})
		if err == nil && trimmed(out) == kv[1] {
			continue
		}
		_, _ = g.run(ctx, call{dir: dir, args: []string{"config", "--local", kv[0], kv[1]}}) // best effort, see doc
	}
}

// SizeOf measures the clone's git directory without running git.
func SizeOf(dir string) (CloneSize, error) {
	gitDir := PathsOf(dir).GitDir
	objects := filepath.Join(gitDir, "objects")
	var s CloneSize
	err := filepath.WalkDir(gitDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // a temp file git removed while we walked
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // gone since the listing (a repack beside us)
		}
		n := info.Size()
		s.GitBytes += n
		rel, rerr := filepath.Rel(objects, path)
		if rerr != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		switch {
		case len(parts) == 2 && parts[0] == "pack" && strings.HasSuffix(parts[1], ".pack"):
			s.Packs++
			s.PackBytes += n
		case len(parts) == 2 && len(parts[0]) == 2 && isHex(parts[0]) && isHex(parts[1]):
			s.Loose++
			s.LooseBytes += n
		}
		return nil
	})
	if err != nil {
		return s, errs.FileIO("measure the clone", gitDir, err)
	}
	return s, nil
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return s != ""
}

// readStamp parses an RFC 3339 stamp file; false when absent or bad.
func readStamp(path string) (time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	return t, err == nil
}

// writeStamp records t at path. Best effort: a stamp that cannot be
// written only makes the next command start another pass, which skips
// or finds nothing to do.
func writeStamp(path string, t time.Time) {
	_ = os.WriteFile(path, []byte(t.UTC().Format(time.RFC3339Nano)+"\n"), 0o600) // best effort, see doc
}

// MarkMaintained stamps a clone with nothing to tidy as maintained at now:
// one lore just made (its first fetch left one pack), or a small one wire
// finds due with no repack to do. Its next pass is a day away.
func MarkMaintained(dir string, now time.Time) {
	if err := os.MkdirAll(StateDirOf(dir), cloneDirMode); err != nil {
		return // best effort: the next command asks again
	}
	writeStamp(filepath.Join(StateDirOf(dir), MaintStampFile), now)
}
