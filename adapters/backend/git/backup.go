package git

// Backup refs of `lore migrate` (FORMAT_EVOLUTION rule 4, decision FQ5).
//
//	migrate run ─► Snapshot(dir, ".lore/data")      tree of the working files (temp index)
//	            ─► WriteBackup(dir, base, entries)  commit {branch/: tree} under refs/lore-backup/migrate/<date>[-n]
//	            ─► BackupAt(clone, base, tip)       a store clone's backup ref = its pre-migration tip
//	rollback    ─► DiffTrees(backup, post)          the paths the migration changed
//	            ─► RestoreFiles / ResetClone        put the backed-up bytes back
//
// Backup refs live outside refs/lore/*, which lore's fetch mirrors, so they
// are local only and never pushed (FQ5). Every helper takes an explicit
// work tree dir and touches nothing else; the caller decides what to back
// up and when.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/tmpdir"
)

// BackupRefPrefix is where migrate backups live (outside refs/lore/*).
const BackupRefPrefix = "refs/lore-backup/migrate/"

// Snapshots runs the backup helpers with one git binary.
type Snapshots struct{ g runner }

// NewSnapshots resolves git (WithGit overrides the binary).
func NewSnapshots(opts ...Option) (Snapshots, error) {
	bin, err := lookGit(build(opts).bin)
	if err != nil {
		return Snapshots{}, err
	}
	return Snapshots{g: runner{bin: bin}}, nil
}

// Snapshot returns the tree id of the files under rel (slash path
// relative to work) and stores their objects in the repository at repo
// (any folder inside the code repo). work may be any folder: the code
// repo's lore root, a folder store or a store clone, so one backup commit
// in the code repo can hold every place a migration touches. The files are
// staged into a temporary index (GIT_DIR + GIT_WORK_TREE for that one
// call), so no index is touched; uncommitted and untracked files count. A
// missing folder is the empty tree.
func (s Snapshots) Snapshot(ctx context.Context, repo, work, rel string) (string, error) {
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(rel))); os.IsNotExist(err) {
		return s.emptyTreeIn(ctx, repo)
	}
	gd, err := s.g.run(ctx, call{dir: repo, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: find the repository of the backup", err)
	}
	tmp, err := tmpdir.Dir("lore-migrate-index-", trimmed(gd)) // the git folder when TMPDIR cannot be used
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: temp index", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }() // a temp folder only this call uses
	env := []string{"GIT_DIR=" + trimmed(gd), "GIT_WORK_TREE=" + work, "GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	specs := []string{rel}
	whole := strings.Trim(rel, "/") == "." || rel == ""
	if whole {
		// a store's root folder: its rows sit at the top beside the other
		// folders; lore's own state folder is never part of a backup
		specs = []string{".", ":(exclude)" + rowfs.StoreStateDir}
	}
	if _, err = s.g.run(ctx, call{dir: work, env: env, args: append([]string{"add", "-A", "-f", "--"}, specs...)}); err != nil {
		return "", errs.Wrap(errs.Internal, "git: stage "+rel+" into the backup index", err)
	}
	out, err := s.g.run(ctx, call{dir: work, env: env, args: []string{"write-tree"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write the backup tree", err)
	}
	if whole {
		return trimmed(out), nil
	}
	sub := s.Resolve(ctx, repo, trimmed(out)+":"+strings.Trim(rel, "/"))
	if sub == "" {
		return s.emptyTreeIn(ctx, repo)
	}
	return sub, nil
}

// emptyTreeIn writes git's empty tree into repo (it is virtual until
// written, and a backup commit may name it) and returns its id.
func (s Snapshots) emptyTreeIn(ctx context.Context, repo string) (string, error) {
	out, err := s.g.run(ctx, call{dir: repo, stdin: []byte{}, args: []string{"hash-object", "-w", "-t", "tree", "--stdin"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write the empty tree", err)
	}
	return trimmed(out), nil
}

// Head is the commit HEAD names ("" in a repo with no commit yet).
func (s Snapshots) Head(ctx context.Context, dir string) string {
	out, err := s.g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--verify", "-q", "HEAD^{commit}"}})
	if err != nil {
		return ""
	}
	return trimmed(out)
}

// TreeEntry is one folder of a backup commit's tree ("branch" → a tree id).
type TreeEntry struct {
	Name string
	Tree string
}

// WriteBackup makes a commit whose tree holds entries (one folder each) and
// whose parents are parents (so nothing they point at is collected), and
// points a NEW ref BackupRefPrefix+date (then date-2, date-3, …) at it. It
// returns the ref and the commit.
func (s Snapshots) WriteBackup(ctx context.Context, dir, date string, entries []TreeEntry, parents []string, msg string) (string, string, error) {
	var mk strings.Builder
	for _, e := range entries {
		mk.WriteString("040000 tree " + e.Tree + "\t" + e.Name + "\n")
	}
	out, err := s.g.run(ctx, call{dir: dir, stdin: []byte(mk.String()), args: []string{"mktree"}})
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "git: write the backup tree", err)
	}
	args := []string{"-c", "user.name=lore", "-c", "user.email=lore@localhost", "commit-tree", trimmed(out), "-m", msg}
	for _, p := range parents {
		if p != "" {
			args = append(args, "-p", p)
		}
	}
	out, err = s.g.run(ctx, call{dir: dir, args: args})
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "git: write the backup commit", err)
	}
	commit := trimmed(out)
	ref, err := s.BackupAt(ctx, dir, date, commit)
	return ref, commit, err
}

// BackupAt points a new backup ref (BackupRefPrefix+date, -2, … when taken)
// at commit and returns its name. The ref is created, never moved.
func (s Snapshots) BackupAt(ctx context.Context, dir, date, commit string) (string, error) {
	for n := 1; n < maxBackupsPerDay; n++ {
		ref := BackupRefPrefix + date
		if n > 1 {
			ref += "-" + strconv.Itoa(n)
		}
		// The zero old value makes update-ref refuse an existing ref.
		if _, err := s.g.run(ctx, call{dir: dir, args: []string{"update-ref", ref, commit, zeroOID}}); err == nil {
			pruneRefs(ctx, s.g, dir, BackupRefPrefix, MigrateBackupsKept) // bounded (backupprune.go)
			return ref, nil
		}
	}
	return "", errs.New(errs.Internal, "git: too many migrate backups today under "+BackupRefPrefix+date)
}

// maxBackupsPerDay bounds the -n suffix search.
const maxBackupsPerDay = 1000

// zeroOID is "the ref must not exist" for update-ref.
const zeroOID = "0000000000000000000000000000000000000000"

// Resolve is the object rev names ("" when it does not exist).
func (s Snapshots) Resolve(ctx context.Context, dir, rev string) string {
	out, err := s.g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--verify", "-q", rev}})
	if err != nil {
		return ""
	}
	return trimmed(out)
}

// TreeChange is one path whose blob differs between two trees; an empty
// blob means the path is absent on that side.
type TreeChange struct {
	Path    string
	OldBlob string
	NewBlob string
}

// DiffTrees lists the paths (relative to the trees) that differ from a to b.
func (s Snapshots) DiffTrees(ctx context.Context, dir, a, b string) ([]TreeChange, error) {
	out, err := s.g.run(ctx, call{dir: dir, args: []string{"diff-tree", "-r", "-z", "--no-renames", "--no-commit-id", a, b}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: compare backup trees", err)
	}
	parts := splitZ(out)
	var res []TreeChange
	for i := 0; i+1 < len(parts); i += 2 {
		// ":<mode> <mode> <old> <new> <status>" then the path.
		f := strings.Fields(strings.TrimPrefix(parts[i], ":"))
		if len(f) < 5 {
			continue
		}
		c := TreeChange{Path: parts[i+1], OldBlob: f[2], NewBlob: f[3]}
		if c.OldBlob == zeroOID {
			c.OldBlob = ""
		}
		if c.NewBlob == zeroOID {
			c.NewBlob = ""
		}
		res = append(res, c)
	}
	return res, nil
}

// BlobOf is the blob id of path inside tree-ish ("" when absent).
func (s Snapshots) BlobOf(ctx context.Context, dir, treeish, path string) string {
	return s.Resolve(ctx, dir, treeish+":"+path)
}

// HashFile is the blob id the file at full would get ("" when absent).
func (s Snapshots) HashFile(ctx context.Context, dir, full string) string {
	if _, err := os.Stat(full); err != nil {
		return ""
	}
	out, err := s.g.run(ctx, call{dir: dir, args: []string{"hash-object", "--no-filters", "--", full}})
	if err != nil {
		return ""
	}
	return trimmed(out)
}

// Blob returns the bytes of a blob.
func (s Snapshots) Blob(ctx context.Context, dir, blob string) ([]byte, error) {
	out, err := s.g.run(ctx, call{dir: dir, args: []string{"cat-file", "blob", blob}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read backup blob", err)
	}
	return out, nil
}

// ResetClone moves a lore-owned store clone's branch and files back to
// commit (a personal undo before push; never used on a person's repo).
func (s Snapshots) ResetClone(ctx context.Context, dir, commit string) error {
	if _, err := s.g.run(ctx, call{dir: dir, args: []string{"reset", "-q", "--hard", commit}}); err != nil {
		return errs.Wrap(errs.Internal, "git: reset the store clone to its backup", err)
	}
	return nil
}

// CloneMode reports whether b is a lore-owned store clone (it commits
// every write) rather than the code repo (files only).
func (b *Backend) CloneMode() bool { return b.mode == modeClone }

// IsAncestor reports whether commit a is reachable from b.
func (s Snapshots) IsAncestor(ctx context.Context, dir, a, b string) bool {
	_, err := s.g.run(ctx, call{dir: dir, args: []string{"merge-base", "--is-ancestor", a, b}})
	return err == nil
}

// Upstream is the commit of HEAD's upstream tracking ref ("" without one).
func (s Snapshots) Upstream(ctx context.Context, dir string) string {
	return s.Resolve(ctx, dir, "@{upstream}")
}

// DataPath is the data folder's slash path under Dir (".lore/data" or
// "<folder>/.lore/data"); lore migrate backs it up and restores it.
func (b *Backend) DataPath() string { return b.folder.DataPath() }

// DeleteRef removes a backup ref (a run that changed nothing keeps none).
// Only refs under BackupRefPrefix may be deleted here.
func (s Snapshots) DeleteRef(ctx context.Context, dir, ref string) error {
	if ref == "" {
		return nil
	}
	if !strings.HasPrefix(ref, BackupRefPrefix) {
		return errs.New(errs.Internal, "git: refuse to delete "+ref+" (not a migrate backup)")
	}
	if _, err := s.g.run(ctx, call{dir: dir, args: []string{"update-ref", "-d", ref}}); err != nil {
		return errs.Wrap(errs.Internal, "git: delete the backup ref", err)
	}
	return nil
}
