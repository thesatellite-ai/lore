package wire

// metarebuild.go — the git checks `lore meta rebuild` runs before it
// recreates a key row (METADATA 3.18 steps 2 and 3; OV64).
//
//	ops/settings.metaRebuild ─ upgrade.As[settings.KeyRowProber](env)
//	  ─► (*App).ProbeRows(src, refs)
//	       ├─ concrete backend is not git → Git=false (the op refuses)
//	       ├─ rev-parse --is-shallow-repository → Shallow
//	       ├─ ls-files -t over meta_keys/ and meta_values/: an "S" entry
//	       │  (skip-worktree) → Sparse
//	       └─ each wanted row file: work tree, then HEAD and every local
//	          and remote-tracking branch (the default branch and every
//	          fetched ref); the first readable copy is Found[id] = where
//
//	pre-commit / landing ─► KeyLoosenings(dir, base, target) ─► changed
//	  meta_keys/*.json between base and target ("" = the index)
//	  ─► settings.CheckKeyLoosening per file (E_LEAK_BLOCKED, RX7)

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var _ settings.KeyRowProber = (*App)(nil)

// probeWorkTree names a copy found on disk.
const probeWorkTree = "the work tree"

// skipWorktreeTag starts an `ls-files -t` line of a file a sparse
// checkout leaves out.
const skipWorktreeTag = "S "

// ProbeRows implements settings.KeyRowProber.
func (a *App) ProbeRows(ctx context.Context, src store.Source, refs []engine.Ref) (settings.KeyRowProbe, error) {
	st, err := a.state(ctx)
	if err != nil {
		return settings.KeyRowProbe{}, err
	}
	b, ok := st.byName[src]
	if !ok {
		return settings.KeyRowProbe{}, errs.New(errs.NotFound, "wire: store "+string(src)+" is not attached")
	}
	gb, ok := concrete(b).(*git.Backend)
	if !ok {
		return settings.KeyRowProbe{Found: map[string]string{}}, nil
	}
	out := settings.KeyRowProbe{Git: true, Found: map[string]string{}}
	dir, data := gb.Dir(), gb.DataPath()
	if sh, serr := liveGit(ctx, dir, "rev-parse", "--is-shallow-repository"); serr == nil && strings.TrimSpace(string(sh)) == "true" {
		out.Shallow = true
	}
	ls, lerr := liveGit(ctx, dir, "ls-files", "-t", "--", "./"+data+"/"+string(schema.TableMetaKeys), "./"+data+"/"+string(schema.TableMetaValues))
	if lerr == nil {
		for line := range strings.SplitSeq(string(ls), "\n") {
			if strings.HasPrefix(line, skipWorktreeTag) {
				out.Sparse = true
				break
			}
		}
	}
	branches := []string{"HEAD"}
	if fr, ferr := liveGit(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes"); ferr == nil {
		for r := range strings.SplitSeq(strings.TrimSpace(string(fr)), "\n") {
			if r != "" && !strings.HasSuffix(r, "/HEAD") {
				branches = append(branches, r)
			}
		}
	}
	out.Refs = branches
	for _, ref := range refs {
		rel := rowfs.RowPath(ref.Table, ref.ID)
		path := data + "/" + rel
		if doc, rerr := os.ReadFile(filepath.Clean(filepath.Join(dir, filepath.FromSlash(path)))); rerr == nil {
			if _, derr := rowfs.DecodeRowFile(rel, doc); derr == nil {
				out.Found[ref.ID.String()] = probeWorkTree
				continue
			}
		}
		for _, br := range branches {
			doc, gerr := liveGit(ctx, dir, "cat-file", "blob", br+":./"+path)
			if gerr != nil {
				continue // absent on that ref
			}
			if _, derr := rowfs.DecodeRowFile(rel, doc); derr == nil {
				out.Found[ref.ID.String()] = br
				break
			}
		}
	}
	return out, nil
}

// keyRowGlob matches every key row file at any depth of a repo or store.
const keyRowGlob = ":(glob)**/" + string(schema.TableMetaKeys) + "/*.json"

// KeyLoosenings checks every meta_keys row file changed between base and
// target in the git checkout dir (target "" = the staged index, the
// pre-commit case; base..target is the landing / ci-merge case) and
// returns one E_LEAK_BLOCKED per file (none: empty), each naming the key
// and the command to use; err is a git failure. Files added or removed
// are not loosenings (a new key, a tombstone commit).
func KeyLoosenings(ctx context.Context, dir, base, target string) (found []error, err error) {
	args := []string{"diff", "--name-only", "--diff-filter=M"}
	if target == "" {
		args = append(args, "--cached", base)
	} else {
		args = append(args, base, target)
	}
	names, err := liveGit(ctx, dir, append(args, "--", keyRowGlob)...)
	if err != nil {
		return nil, err
	}
	for p := range strings.SplitSeq(strings.TrimSpace(string(names)), "\n") {
		if p == "" {
			continue
		}
		// diff names are relative to the top, and so is <rev>:<path>
		// (":<path>" is the index), from a monorepo subfolder too.
		head, herr := liveGit(ctx, dir, "show", base+":"+p)
		if herr != nil {
			continue
		}
		now, nerr := liveGit(ctx, dir, "show", target+":"+p)
		if nerr != nil {
			continue
		}
		if cerr := settings.CheckKeyLoosening(p, head, now); cerr != nil {
			found = append(found, cerr)
		}
	}
	return found, nil
}

// KeyLooseningsSince is KeyLoosenings over what HEAD added since it left
// base (the merge base of base and HEAD; the landing, ci-merge and
// `check --ci` case). base "" picks origin/HEAD, else HEAD~1; with
// neither (a first commit) there is nothing to compare and none is found.
// DECISION OV64-CIBASE: check --ci without a base compares with the
// default remote branch, else the previous commit.
func KeyLooseningsSince(ctx context.Context, dir, base string) ([]error, error) {
	if base == "" {
		for _, b := range []string{"origin/HEAD", "HEAD~1"} {
			if liveRefExists(ctx, dir, b) {
				base = b
				break
			}
		}
		if base == "" {
			return nil, nil
		}
	}
	mb, err := liveGit(ctx, dir, "merge-base", base, "HEAD")
	if err != nil {
		return nil, err
	}
	return KeyLoosenings(ctx, dir, strings.TrimSpace(string(mb)), "HEAD")
}
