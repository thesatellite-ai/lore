package wire

// storeclonefilter.go — blobless store clones (store growth SG-3b,
// devco:D-1001; the git side is adapters/backend/git/blobless.go).
//
//	open ─► storeOpeners ─► readCloneFilter(home) ─► git.WithCloneFilter:
//	        a NEW clone is blobless when store.clone_filter is blob:none
//	store.prefetch ─► PrefetchStore ─► git.Backend.Prefetch: every old
//	        version the clone lacks, one request
//	store.reclone  ─► RecloneStore: refuse a clone with commits the remote
//	        lacks, uncommitted files, or a reset mark; move it to
//	        <home>/cache/replaced-clones, open the store again with the
//	        filter (a fresh clone), carry the state files over
//	store maintain ─► git.SweepOldClones: replaced clones older than a week
//
// Setting: `store: {clone_filter: none|blob:none}` in ~/.lore/config.yaml
// (default none). Like store.lock_timeout it is about this machine, not a
// team rule. It stays none by default until every machine of a team runs
// a lore that reads partial clones (SG-3a, v0.1.54) and the roster replay
// of a fresh clone no longer reads every old version (store growth §11
// Q3): until then a blobless clone's first read fetches those versions
// anyway, in one request.

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// cloneFilterDoc is the part of config.yaml this file reads.
type cloneFilterDoc struct {
	Store struct {
		CloneFilter string `yaml:"clone_filter"`
	} `yaml:"store"`
}

// readCloneFilter is store.clone_filter from <home>/config.yaml:
// git.CloneFilterNone when unset. Any other value than none or blob:none
// fails closed with E_CONFIG_INVALID.
func readCloneFilter(home string) (string, error) {
	b, err := readConfigRaw(home)
	if err != nil || b == nil {
		return git.CloneFilterNone, err
	}
	var doc cloneFilterDoc
	if err = yamlerr.Decode(b, &doc, yamlerr.Lenient); err != nil {
		return git.CloneFilterNone, configYAMLErr(err)
	}
	v := strings.TrimSpace(doc.Store.CloneFilter)
	if v == "" {
		return git.CloneFilterNone, nil
	}
	if !git.ValidCloneFilter(v) {
		return git.CloneFilterNone, errs.New(errs.ConfigInvalid, lorehome.Shown()+"/"+ConfigFile+": store.clone_filter "+v+" is not none or blob:none")
	}
	return v, nil
}

// gitStoreOf opens the git store e names (its clone, made on first use),
// with ctx's options.
func (a *App) gitStoreOf(ctx context.Context, cmd string, e opsstores.Entry) (*git.Backend, error) {
	canon, err := canonical(attach.Store{URL: e.URL, Branch: e.Branch})
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(canon)
	if err != nil || u.Scheme == folder.Scheme {
		return nil, errs.New(errs.StoreScheme, "store "+cmd+": "+e.URL+" is a folder store; only a git store has a clone").
			WithHint("lore store list shows each store's url")
	}
	b, err := a.openStore(ctx, canon, e.URL)
	if err != nil {
		return nil, err
	}
	gb, ok := b.(*git.Backend)
	if !ok || !gb.CloneMode() {
		_ = b.Close()
		return nil, errs.New(errs.StoreScheme, "store "+cmd+": "+e.URL+" has no store clone on this machine")
	}
	return gb, nil
}

// PrefetchStore implements opsstores.CloneFilterer.
func (a *App) PrefetchStore(ctx context.Context, e opsstores.Entry) (opsstores.PrefetchOut, error) {
	gb, err := a.gitStoreOf(ctx, "prefetch", e)
	if err != nil {
		return opsstores.PrefetchOut{}, err
	}
	defer func() { _ = gb.Close() }() // read-only use: objects added, no row written
	out := opsstores.PrefetchOut{Store: cloneStoreLabel(e), Clone: gb.Dir()}
	res, err := gb.Prefetch(ctx)
	out.Blobless, out.Missing, out.Fetched = res.Partial, res.Missing, res.Fetched
	return out, err
}

// cloneStoreLabel is how a result names the store: its name, else its url.
func cloneStoreLabel(e opsstores.Entry) string {
	if e.Name != "" {
		return e.Name
	}
	return e.URL
}

// RecloneStore implements opsstores.CloneFilterer.
func (a *App) RecloneStore(ctx context.Context, e opsstores.Entry, filter string) (opsstores.RecloneOut, error) {
	home := a.opts.Home
	if filter == "" {
		f, err := readCloneFilter(home)
		if err != nil {
			return opsstores.RecloneOut{}, err
		}
		filter = f
	}
	if gitenv.NoFetch(os.Environ()) {
		return opsstores.RecloneOut{}, errs.New(errs.Offline, "store reclone: a new clone needs the network, and "+gitenv.EnvOffline+" / "+gitenv.EnvNoFetch+" is set; nothing was changed").
			WithHint("run it again online")
	}
	gb, err := a.gitStoreOf(ctx, "reclone", e)
	if err != nil {
		return opsstores.RecloneOut{}, err
	}
	dir := gb.Dir()
	shown := cloneStoreLabel(e)
	refuse := func(why, hint string) (opsstores.RecloneOut, error) {
		_ = gb.Close()
		return opsstores.RecloneOut{}, errs.New(errs.Refused, "store reclone: "+why+"; nothing was changed").WithHint(hint)
	}
	switch {
	case gb.OwnClone():
		return refuse(dir+" is your own clone (lore store use), which lore never remakes", "make it again with git yourself, or go back to lore's own clone: lore store use --default "+shown)
	case git.RemoteResetMarked(dir):
		return refuse("lore marked this clone's store as deleted and made again", "settle it first: lore store push --store "+shown+" --restore, or lore store forget --url "+e.URL)
	}
	if n, cerr := gb.LocalOnlyCommits(ctx); cerr != nil {
		_ = gb.Close()
		return opsstores.RecloneOut{}, cerr
	} else if n > 0 {
		return refuse(dir+" holds "+strconv.Itoa(n)+" commit(s) the remote does not have, and a new clone would not", "push them first: lore sync, or lore store push --store "+shown)
	}
	if st, serr := liveGit(ctx, dir, "status", "--porcelain", "--untracked-files=normal"); serr != nil {
		_ = gb.Close()
		return opsstores.RecloneOut{}, errs.Wrap(errs.Internal, "store reclone: read the clone's status", serr)
	} else if strings.TrimSpace(string(st)) != "" {
		return refuse(dir+" has files changed by hand that are not committed", "commit or remove them (git -C "+dir+" status), then run it again")
	}
	_ = gb.Close()
	before, _ := git.SizeOf(dir)
	old, err := moveCloneAside(ctx, home, dir, a.wall())
	if err != nil {
		return opsstores.RecloneOut{}, err
	}
	nb, err := a.gitStoreOf(git.WithCloneFilterOverride(ctx, filter), "reclone", opsstores.Entry{Name: e.Name, URL: e.URL, Branch: e.Branch})
	if err != nil {
		// put the old clone back: the store keeps working as it did
		if rerr := os.RemoveAll(dir); rerr == nil {
			_ = os.Rename(old, dir)
		}
		return opsstores.RecloneOut{}, err
	}
	_ = nb.Close()
	if err := carryCloneState(old, dir); err != nil {
		return opsstores.RecloneOut{}, err
	}
	after, _ := git.SizeOf(dir)
	return opsstores.RecloneOut{Store: shown, Clone: dir, Old: old, Blobless: git.IsPartialClone(dir), BeforeBytes: before.GitBytes, AfterBytes: after.GitBytes}, nil
}

// moveCloneAside moves the clone at dir under git.ReplacedClonesDir,
// holding its store lock so no command writes it meanwhile.
func moveCloneAside(ctx context.Context, home, dir string, now time.Time) (string, error) {
	root0, err := rowfs.Open(dir, rowfs.WithLockPath(git.LockPathOf(dir)))
	if err != nil {
		return "", err
	}
	lock, err := root0.Lock(ctx)
	if err != nil {
		return "", err
	}
	// released after the move: the lock file moved with the folder, and a
	// command that waited on it opens the store again (a new clone)
	defer func() { _ = lock.Release() }()
	root := git.ReplacedClonesDir(home)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", errs.FileIO("create the folder for replaced store clones", root, err)
	}
	base := filepath.Join(root, filepath.Base(dir)+"-"+now.UTC().Format("20060102-150405"))
	old := base
	for n := 2; exists(old); n++ { // two reclones in one second
		old = base + "-" + strconv.Itoa(n)
	}
	if err := os.Rename(dir, old); err != nil {
		return "", errs.FileIO("move the store clone aside", dir, err)
	}
	// the folder's time is when it was replaced: SweepOldClones counts from it
	_ = os.Chtimes(old, now, now)
	return old, nil
}

// carryCloneState copies git.RecloneCarried from the replaced clone old
// into the new clone at dir; a file the old clone lacks is skipped.
func carryCloneState(old, dir string) error {
	for _, rel := range git.RecloneCarried {
		src, dst := filepath.Join(old, rel), filepath.Join(dir, rel)
		in, err := os.Open(src) // #nosec G304 -- a file of lore's own clone state
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return errs.FileIO("read the replaced clone's state", src, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			_ = in.Close()
			return errs.FileIO("create the clone's state folder", filepath.Dir(dst), err)
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- lore's own clone state
		if err != nil {
			_ = in.Close()
			return errs.FileIO("write the clone's state", dst, err)
		}
		_, cerr := io.Copy(out, in)
		_ = in.Close()
		if err := out.Close(); cerr == nil {
			cerr = err
		}
		if cerr != nil {
			return errs.FileIO("write the clone's state", dst, cerr)
		}
	}
	return nil
}
