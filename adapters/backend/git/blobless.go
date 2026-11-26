package git

// blobless.go — blobless store clones (store growth SG-3b, devco:D-1001).
//
//	store.clone_filter: blob:none (WithCloneFilter) ─► a NEW clone gets the
//	    promisor config (initClone) and its first fetch --filter=blob:none
//	    (openFetch): every commit and tree, only the blobs it checks out
//	server ignores the filter ─► a tip blob came anyway: the promisor config is
//	    dropped again, the clone is a full one (noteFilterHonoured)
//	every later fetch in a partial clone ─► --no-filter (runOnce), so a
//	    clone only ever lacks blobs older than itself
//	readBlobs / Prefetch ─► the missing blobs of a read fetched in ONE
//	    request (fetchMissing), never one lazy fetch per blob; offline the
//	    read fails with E_OFFLINE (MissingObject)
//
// A clone made before the setting stays as it is: a full clone cannot drop
// its blobs in place, and nothing needs it to.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitfast"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Values of store.clone_filter (WithCloneFilter).
const (
	// CloneFilterNone makes full clones: every blob of the history (the
	// default until every machine runs a lore that reads partial clones).
	CloneFilterNone = "none"
	// CloneFilterBlobless makes blobless clones: every commit and tree,
	// blobs fetched when a read needs them.
	CloneFilterBlobless = "blob:none"
)

// ValidCloneFilter reports whether v is a value store.clone_filter takes.
func ValidCloneFilter(v string) bool { return v == CloneFilterNone || v == CloneFilterBlobless }

// WithCloneFilter sets the filter a NEW store clone's first fetch uses
// (store.clone_filter): CloneFilterBlobless makes a blobless clone, any
// other value a full one. An existing clone keeps what it is.
func WithCloneFilter(filter string) Option { return func(c *config) { c.cloneFilter = filter } }

// noFilterArg is the fetch option that overrides a partial clone's
// default filter, so new history comes with every blob.
const noFilterArg = "--no-filter"

// filterArg is the first fetch's option in a blobless clone.
const filterArg = "--filter=" + CloneFilterBlobless

// gitConfigFile is a clone's config file under its .git folder.
const gitConfigFile = "config"

// partialCloneAt reports whether the clone whose work tree (or .git
// folder's parent) is dir is a partial clone.
func partialCloneAt(dir string) bool {
	return gitfast.PartialClone(filepath.Join(dir, gitDirName, gitConfigFile))
}

// IsPartialClone reports whether the store clone at dir is blobless (a
// partial clone whose old blobs are fetched on demand). lore store show,
// doctor and prefetch name it.
func IsPartialClone(dir string) bool { return partialCloneAt(dir) }

// withNoFilter adds --no-filter right after the fetch subcommand of args
// unless args already name a filter. args may start with -c pairs.
func withNoFilter(args []string) []string {
	at := -1
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c":
			i++
		case strings.HasPrefix(a, "-"):
		case a == "fetch":
			at = i
		default:
			return args
		}
		if at >= 0 {
			break
		}
	}
	if at < 0 {
		return args
	}
	for _, a := range args[at+1:] {
		if a == "--" {
			break
		}
		if a == noFilterArg || strings.HasPrefix(a, "--filter") || a == "--stdin" {
			return args // a filter chosen on purpose, or a fetch of named objects
		}
	}
	return slices.Concat(args[:at+1], []string{noFilterArg}, args[at+1:])
}

// setPromisor writes what `git clone --filter=blob:none` writes into a
// new clone's config: origin is the promisor remote, blob:none its filter.
func (b *Backend) setPromisor(ctx context.Context) error {
	for _, kv := range [][2]string{
		{"core.repositoryformatversion", "1"},
		{"remote." + RemoteName + ".promisor", "true"},
		{"remote." + RemoteName + ".partialclonefilter", CloneFilterBlobless},
	} {
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--local", kv[0], kv[1]}}); err != nil {
			return errs.Wrap(errs.Internal, "git: make the store clone blobless", err)
		}
	}
	return nil
}

// dropPromisor turns a clone whose remote ignored the filter back into a
// full clone: without a promisor pack every object was fetched, and a
// promisor config would only make later reads ask a remote for objects it
// will never have.
func (b *Backend) dropPromisor(ctx context.Context) error {
	for _, k := range []string{"remote." + RemoteName + ".promisor", "remote." + RemoteName + ".partialclonefilter"} {
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--local", "--unset-all", k}}); err != nil {
			if _, ran := exitCode(err); !ran {
				return errs.Wrap(errs.Internal, "git: make the store clone a full clone", err)
			}
		}
	}
	return nil
}

// noteFilterHonoured runs after a blobless clone's first fetch, before
// the checkout: a remote that honoured the filter sent no blob at all, so
// a blob of the fetched tip already in the clone means the remote ignored
// it (a server without filter support: git prints "filtering not
// recognized by server, ignoring" and sends everything). The clone then
// becomes a full one again, so nothing later takes it for blobless. An
// empty store, or a tip with no file, keeps the config: what it fetches
// later comes with --no-filter.
func (b *Backend) noteFilterHonoured(ctx context.Context) error {
	if !partialCloneAt(b.work) {
		return nil
	}
	heads, err := b.git.run(ctx, call{dir: b.work, args: []string{"for-each-ref", "--count=1", "--format=%(objectname)", trackingRefNS}})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: list the fetched branches", err)
	}
	tip := trimmed(heads)
	if tip == "" {
		return nil
	}
	files, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-tree", "-r", "--format=%(objecttype) %(objectname)", tip}})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: list the fetched files", err)
	}
	for line := range strings.SplitSeq(string(files), "\n") {
		oid, ok := strings.CutPrefix(line, typeBlob+" ")
		if !ok {
			continue
		}
		missing, merr := b.missingBlobs(ctx, []string{oid})
		if merr != nil {
			return merr
		}
		if len(missing) == 0 {
			return b.dropPromisor(ctx) // the remote sent the blob: it ignored the filter
		}
		return nil
	}
	return nil
}

// missingBlobs lists, among oids, the objects this clone does not hold,
// without fetching any (GIT_NO_LAZY_FETCH): one cat-file run.
func (b *Backend) missingBlobs(ctx context.Context, oids []string) ([]string, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"cat-file", "--batch-check=%(objectname)"}, env: []string{noLazyFetchEnv},
		stdin: []byte(strings.Join(oids, "\n") + "\n")})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: check which blobs the clone holds", err)
	}
	var missing []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if oid, ok := strings.CutSuffix(strings.TrimSpace(line), " "+catFileMissing); ok {
			missing = append(missing, oid)
		}
	}
	return missing, nil
}

// fetchMissing fetches the objects oids from the promisor remote in one
// request (what git's own lazy fetch sends, for all of them at once).
// Offline (LORE_OFFLINE / LORE_NO_FETCH) it fetches nothing and returns
// nil: the read that follows reports each missing blob as E_OFFLINE.
func (b *Backend) fetchMissing(ctx context.Context, oids []string) error {
	if len(oids) == 0 || gitenv.NoFetch(os.Environ()) || NoFetch(ctx) {
		return nil
	}
	_, err := b.git.run(ctx, call{dir: b.work, args: []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no", filterArg, "--stdin", RemoteName},
		stdin: []byte(strings.Join(oids, "\n") + "\n")})
	if err != nil {
		if _, ran := exitCode(err); !ran {
			return err
		}
		return errs.Wrap(errs.Offline, "git: fetch "+strconv.Itoa(len(oids))+" old version(s) the blobless store clone lacks", err).
			WithHint("run it again online; `lore sync` checks the store can be reached")
	}
	return nil
}

// PrefetchResult is what Prefetch did.
type PrefetchResult struct {
	// Partial is false for a full clone: nothing to fetch.
	Partial bool
	// Missing is how many blobs of the history the clone lacked; Fetched
	// how many it holds now of those.
	Missing int
	Fetched int
}

// Prefetch fetches every blob of the store's history the clone lacks, in
// one request, so every history read works offline afterwards (`lore
// store prefetch`, and before `lore store set-url` pushes the whole
// history to another remote). A full clone does nothing.
func (b *Backend) Prefetch(ctx context.Context) (PrefetchResult, error) {
	if !partialCloneAt(b.work) {
		return PrefetchResult{}, nil
	}
	if gitenv.NoFetch(os.Environ()) || NoFetch(ctx) {
		return PrefetchResult{Partial: true}, errs.New(errs.Offline, "git: fetching the versions a blobless store clone lacks needs the network, and "+gitenv.EnvOffline+" / "+gitenv.EnvNoFetch+" is set").
			WithHint("run it again online")
	}
	missing, err := b.missingInHistory(ctx)
	if err != nil {
		return PrefetchResult{Partial: true}, err
	}
	res := PrefetchResult{Partial: true, Missing: len(missing)}
	if err := b.fetchMissing(ctx, missing); err != nil {
		return res, err
	}
	left, err := b.missingInHistory(ctx)
	if err != nil {
		return res, err
	}
	res.Fetched = res.Missing - len(left)
	if len(left) > 0 {
		return res, errs.New(errs.StoreUnreachable, "git: the store's remote did not send "+strconv.Itoa(len(left))+" old version(s) this blobless clone lacks").
			WithHint("check the remote still has the history (`lore doctor`), then run it again")
	}
	return res, nil
}

// missingInHistory lists every object reachable from the clone's refs
// that the clone does not hold (rev-list prints them as "?<oid>").
func (b *Backend) missingInHistory(ctx context.Context) ([]string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-list", "--objects", "--missing=print", "--all"}, env: []string{noLazyFetchEnv}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: list what the blobless store clone lacks", err)
	}
	var missing []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if oid, ok := strings.CutPrefix(strings.TrimSpace(line), "?"); ok && oid != "" {
			missing = append(missing, oid)
		}
	}
	return missing, nil
}

// cloneFilterKey is WithCloneFilterOverride's context key.
type cloneFilterKey struct{}

// WithCloneFilterOverride makes a clone that ctx's open creates use
// filter instead of store.clone_filter (`lore store reclone --filter`).
func WithCloneFilterOverride(ctx context.Context, filter string) context.Context {
	return context.WithValue(ctx, cloneFilterKey{}, filter)
}

// cloneFilterFor is the filter a new clone gets: the override ctx
// carries, else the opener's (WithCloneFilter), else store.clone_filter of
// the lore home the clone is made in.
func (b *Backend) cloneFilterFor(ctx context.Context) string {
	if f, ok := ctx.Value(cloneFilterKey{}).(string); ok && f != "" {
		return f
	}
	if b.cfg.cloneFilter != "" {
		return b.cfg.cloneFilter
	}
	return cloneFilterOfHome(b.work)
}

// LocalOnlyCommits counts the commits a store clone holds that its remote
// does not: on any local branch or layer ref, not reachable from what was
// fetched. `lore store reclone` refuses a clone where it is not 0, since
// a new clone would not have them.
func (b *Backend) LocalOnlyCommits(ctx context.Context) (int, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-list", "--count",
		"--branches", "--glob=" + branchlayer.RefPrefix + "*", "--glob=" + branchlayer.TrashPrefix + "*",
		"--not", "--glob=" + trackingRefNS + "*", "--glob=" + LayerTrackingPrefix + "*", "--glob=" + LayerTrashTrackingPrefix + "*"}})
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "git: count the clone's unpushed commits", err)
	}
	n, perr := strconv.Atoi(trimmed(out))
	if perr != nil {
		return 0, errs.Wrap(errs.Internal, "git: count the clone's unpushed commits", perr)
	}
	return n, nil
}

// OwnClone reports whether b is a person's own clone (`lore store use`):
// lore never removes or remakes it.
func (b *Backend) OwnClone() bool { return b.cfg.own }

// RecloneCarried are the files a remade clone takes over from the clone it
// replaces (`lore store reclone`): under .git/lore the remote tip lore saw
// (so a store deleted and made again is still noticed), this machine's
// claims and when it first saw each teammate's; in .git the saved roster
// replay, so the new clone judges the same history without reading every
// old version again.
var RecloneCarried = []string{
	filepath.Join(gitDirName, StateDirName, remoteTipName),
	filepath.Join(gitDirName, StateDirName, "held.json"),
	filepath.Join(gitDirName, StateDirName, "signals-seen.json"),
	filepath.Join(gitDirName, authorityFile),
	filepath.Join(gitDirName, authorityFile+authorityRowsSuffix),
}

// RemoteResetMarked reports whether lore marked the clone at dir as
// pointing at a store deleted and made again: such a clone is never
// remade, its state is what keeps the old rows from being pushed.
func RemoteResetMarked(dir string) bool {
	return exists(filepath.Join(dir, gitDirName, StateDirName, remoteResetName))
}

// ReplacedClonesName is ReplacedClonesDir under the lore home's cache folder.
const ReplacedClonesName = replacedClonesDir

// ReplacedClonesDir is where `lore store reclone` keeps a replaced clone
// (<home>/cache/replaced-clones), outside the stores folder so no command takes
// it for a clone; SweepOldClones removes each a week later.
func ReplacedClonesDir(home string) string {
	return filepath.Join(home, homeCacheDir, replacedClonesDir)
}

// replacedClonesDir is ReplacedClonesDir's folder name under the cache folder.
const replacedClonesDir = "replaced-clones"

// OldCloneKeep is how long a replaced clone is kept.
const OldCloneKeep = 7 * 24 * time.Hour

// SweepOldClones removes the replaced clones under ReplacedClonesDir(home)
// older than OldCloneKeep (by the folder's modification time, which the
// move set). It returns the folders removed.
func SweepOldClones(home string, now time.Time) ([]string, error) {
	root := ReplacedClonesDir(home)
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.FileIO("list the replaced store clones", root, err)
	}
	var gone []string
	for _, e := range ents {
		info, ierr := e.Info()
		if ierr != nil || !e.IsDir() || now.Sub(info.ModTime()) < OldCloneKeep {
			continue
		}
		p := filepath.Join(root, e.Name())
		if rerr := os.RemoveAll(p); rerr != nil {
			return gone, errs.FileIO("remove a replaced store clone", p, rerr)
		}
		gone = append(gone, p)
	}
	return gone, nil
}

// homeConfigFile is the lore home's machine settings file.
const homeConfigFile = "config.yaml"

// cloneFilterDoc is the part of config.yaml cloneFilterOfHome reads.
type cloneFilterDoc struct {
	Store struct {
		CloneFilter string `yaml:"clone_filter"`
	} `yaml:"store"`
}

// cloneFilterOfHome is store.clone_filter of the lore home a clone at dir
// lives in (<home>/stores/...): what every path that makes a clone without
// an explicit filter uses, the org link code's PrepareClone included. A
// clone outside a stores folder, an unreadable file or a value lore does
// not take is a full clone (lore config check reports a bad value; wire
// refuses it before any open).
func cloneFilterOfHome(dir string) string {
	for d := filepath.Dir(dir); ; {
		parent := filepath.Dir(d)
		if filepath.Base(d) == StoresDir {
			raw, err := os.ReadFile(filepath.Join(parent, homeConfigFile)) // #nosec G304 -- the lore home's own settings file
			if err != nil {
				return CloneFilterNone
			}
			var doc cloneFilterDoc
			if yamlerr.Decode(raw, &doc, yamlerr.Lenient) != nil {
				return CloneFilterNone
			}
			if v := strings.TrimSpace(doc.Store.CloneFilter); ValidCloneFilter(v) {
				return v
			}
			return CloneFilterNone
		}
		if parent == d {
			return CloneFilterNone
		}
		d = parent
	}
}

// FetchArgsIn is args (a git fetch command line) as a fetch in the clone
// at dir must run: in a blobless clone with --no-filter, so new history
// comes with every blob. Every fetch into a store clone that does not go
// through a Backend (the org link code, wire's live-ref fetch) passes its
// arguments through it; a fetch without them would bring commits whose
// blobs a later --no-filter fetch never asks for again.
func FetchArgsIn(dir string, args []string) []string {
	if !partialCloneAt(dir) {
		return args
	}
	return withNoFilter(args)
}
