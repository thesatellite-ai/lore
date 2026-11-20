package wire

// storeadopt_move.go — the parts of `lore store adopt` that finish moving
// a repo's notes into a store folder (devco D-713, D-716), so the owner's
// job "move this repo's notes into the store" is one command:
//
//	--remove-source: the adopted row files leave .lore/data (storeadopt.go)
//	   + the adopted rows leave the repo's live ref (adoptLiveRemove):
//	     left there, the repo read each id twice once linked to the folder,
//	     "two live rows with one id", and every one took a `lore row move`
//	--move: --remove-source + the repo linked to the folder (adoptLink, the
//	   org host's `lore org link`, planned before anything is written)
//	--commit: the repo commit of the removal, exactly the command adopt
//	   otherwise prints (AdoptCommitCommand), through the repo's hooks
//
// Why in adopt and not a new verb: adopt already reads every source of
// the repo (row files, the live ref, a v0.1.17 lore.db), writes the folder
// in one store commit keeping ids and HLCs, removes the source and asks
// first; a second verb would repeat all of it. `lore move --from/--to`
// moves rows between stores; a repo into a store is adopt's grammar.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// adoptLiveStateDir is the live machine state folder under the git common
// dir, the one the repo's live session keeps (wire/host liveStateDirName):
// the pending list there tells the next push what left the ref.
const adoptLiveStateDir = "lore"

// adoptLoreData is the repo's row folder from its top, slash separated.
const adoptLoreData = ".lore/data"

// adoptLiveKeys are the keys of the source rows a folder takes (every
// table but the place rows): the ones --remove-source takes off the live
// ref when the local ref holds them, whichever copy won the adoption. A
// deleted, trashed or purged row is not one (adoptGone, D-737): its live
// tombstone stays, so no stale clone pushes the row back.
func adoptLiveKeys(rows []adoptRow) []store.Key {
	out := make([]store.Key, 0, len(rows))
	for _, x := range rows {
		if _, skip := adoptSkipped[x.r.Table]; skip || adoptGone(x.r) {
			continue
		}
		out = append(out, store.Key{Table: x.r.Table, ID: x.r.ID})
	}
	return out
}

// adoptLiveRef is the repo's local live ref at top and its project folder;
// ok false when the repo has no live ref (nothing on it to take off).
func adoptLiveRef(ctx context.Context, top string) (*live.Ref, string, bool, error) {
	if !liveRefExists(ctx, top, liveRefLocal) {
		return nil, "", false, nil
	}
	folder, err := repoLiveFolder(ctx, top)
	if err != nil {
		return nil, "", false, err
	}
	repo, err := gitref.Open(ctx, top, nil)
	if err != nil {
		return nil, "", false, errs.Wrap(errs.Internal, adoptVerb+": open "+top, err)
	}
	common, err := liveGit(ctx, top, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, "", false, err
	}
	sig, ok := repo.GitIdentity(ctx) // the repo's git identity, else the backend's last resort (D-714: one signer rule)
	if !ok {
		sig = gitref.Signature{Name: git.DefaultAuthor.Name, Email: git.DefaultAuthor.Email}
	}
	return &live.Ref{Repo: repo, Name: live.RefLive, Signature: sig,
		StateDir: filepath.Join(strings.TrimSpace(string(common)), adoptLiveStateDir)}, folder, true, nil
}

// adoptLivePresent is what the local live ref holds of keys: each row's
// file bytes, by live key (the compare-and-swap value of its removal).
func adoptLivePresent(ctx context.Context, ref *live.Ref, folder string, keys []store.Key) (map[live.RowKey][]byte, error) {
	_, tree, err := ref.LocalTree(ctx)
	if err != nil {
		return nil, errs.Prefix(adoptVerb+": read the repo's live ref", err)
	}
	out := map[live.RowKey][]byte{}
	for _, k := range keys {
		lk := live.RowKey{Table: k.Table.String(), ID: k.ID.String()}
		if b, ok := tree[live.RowPath(folder, lk.Table, lk.ID)]; ok {
			out[lk] = b
		}
	}
	return out, nil
}

// adoptLiveCount is how many of keys the repo's local live ref holds (the
// dry run's count).
func adoptLiveCount(ctx context.Context, top string, keys []store.Key) (int, error) {
	ref, folder, ok, err := adoptLiveRef(ctx, top)
	if err != nil || !ok {
		return 0, err
	}
	have, err := adoptLivePresent(ctx, ref, folder, keys)
	return len(have), err
}

// adoptLiveRemove takes keys off the repo's local live ref in one live
// commit, each only while its file is still the bytes read (a row edited
// meanwhile stays, named in kept). The removal is recorded as pending, so
// the repo's next push carries it to every clone, as a landing's does.
func (a *App) adoptLiveRemove(ctx context.Context, top string, keys []store.Key) (removed int, kept []string, err error) {
	ref, folder, ok, err := adoptLiveRef(ctx, top)
	if err != nil || !ok {
		return 0, nil, err
	}
	have, err := adoptLivePresent(ctx, ref, folder, keys)
	if err != nil || len(have) == 0 {
		return 0, nil, err
	}
	res, err := ref.Write(ctx, live.WriteRequest{Folder: folder, RemoveIf: have, BinaryVersion: a.opts.Version})
	if err != nil {
		return 0, nil, errs.Prefix(adoptVerb+": take the adopted rows off the repo's live ref", err)
	}
	gone := map[live.RowKey]bool{}
	for _, k := range res.Written {
		gone[k] = true
	}
	for k := range have {
		if gone[k] {
			removed++
		} else {
			kept = append(kept, k.String())
		}
	}
	return removed, kept, nil
}

// adoptLink is --move's link of the repo at top to folder of the store at
// storeURL through the org host (`lore org link`): its plan only when
// write is false (validated before anything is written), the link
// otherwise. It links the repo the command runs in, so --from must name
// that repo.
func (a *App) adoptLink(ctx context.Context, storeURL, label, top, folder string, own, write bool) ([]string, error) {
	here, err := git.RepoRoot(ctx, a.opts.RepoDir, a.opts.Home)
	if err != nil || !sameDir(here, top) {
		return nil, errs.New(errs.InvalidInput, adoptVerb+": --move links the repo it runs in to the folder, and "+top+" is not that repo").
			WithHint("run it inside " + top + ": lore store adopt --target " + label + "/" + folder + " --move")
	}
	h := a.opts.OrgCommands
	if h == nil {
		return nil, errs.New(errs.Unsupported, adoptVerb+": this build cannot link a repo to an org store's folder, so --move cannot finish").
			WithHint("lore store adopt --target " + label + "/" + folder + " --remove-source")
	}
	org := ""
	if sk, ok := storeKey(storeURL); ok {
		org = a.storeOrgs()[sk]
	}
	if org == "" {
		return nil, errs.New(errs.Refused, adoptVerb+": "+label+" is no org's store, and --move links the repo to an org store's folder").
			WithHint("adopt into it without the link: lore store adopt --target " + label + "/" + folder + " --remove-source")
	}
	rep, err := h.Link(ctx, opsorgcmd.LinkIn{Org: org, Project: folder, Own: own, Store: storeURL, DryRun: !write}, write)
	if err != nil {
		return nil, errs.Prefix(adoptVerb+" --move: link "+top+" to "+label+"/"+folder, err)
	}
	return rep.Lines, nil
}

// adoptStagedElsewhere refuses --commit while the repo at top has changes
// staged outside .lore/data: the commit would carry them under the
// adoption's message. Checked before anything is written.
func adoptStagedElsewhere(ctx context.Context, top string) error {
	out, err := liveGit(ctx, top, "-c", "core.quotePath=false", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil // no HEAD yet or git unreadable: the commit itself says
	}
	var other []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" && !strings.HasPrefix(p, adoptLoreData+"/") {
			other = append(other, p)
		}
	}
	if len(other) == 0 {
		return nil
	}
	return errs.New(errs.Refused, adoptVerb+": --commit would carry "+plural(len(other), "other staged file")+" (first: "+other[0]+") into the commit of the notes' move; nothing written").
		WithHint("commit or unstage them first (git -C " + top + " restore --staged -- " + other[0] + "), or drop --commit and commit the removal yourself")
}

// adoptCommit runs the repo commit of the removal, the command adopt
// prints (opsstores.AdoptCommitCommand), through the repo's hooks, and
// returns the new commit's short id.
func adoptCommit(ctx context.Context, top, place string, tidied []string) (string, error) {
	add := append([]string{"add", "-A"}, opsstores.AdoptCommitPaths(tidied)...)
	if _, err := liveGit(ctx, top, add...); err != nil {
		return "", errs.Prefix(adoptVerb+": stage the removal", err)
	}
	if _, err := liveGit(ctx, top, "commit", "-q", "-m", opsstores.AdoptCommitMessage(place)); err != nil {
		return "", errs.Prefix(adoptVerb+": the rows are in "+place+" and the files removed, but the repo commit failed", err).
			WithHint("fix what it names, then commit it: " + opsstores.AdoptCommitCommand(top, place, tidied...))
	}
	sha, err := liveGit(ctx, top, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(sha)), nil
}

// adoptTidy is what --move removes from the repo at top beside the
// adopted row files (devco D-719), as slash paths from top: a .lore/LORE.md
// lore generated (a repo linked to a store folder renders none, so it only
// went stale, and a client repo's leak guard refused each commit touching
// it), and, when oldFormat (the repo was on the lore 0.1.17 format before
// the move), the place rows adopt leaves (adoptSkipped) and _meta.json,
// once nothing else is left in .lore/data: the repo is then no 0.1.17
// project any more, which no write there refuses as one, and it was read,
// never migrated. gone are the adopted row paths (removed, or about to be
// on a dry run). It removes them (pruning emptied folders) only when
// write is true; the dry run lists the same paths.
func adoptTidy(top string, oldFormat bool, gone []string, write bool) ([]string, error) {
	var out []string
	if b, err := os.ReadFile(filepath.Join(top, filepath.FromSlash(opsstores.AdoptLoreMD))); err == nil && strings.Contains(string(b), adoptGeneratedMark) {
		out = append(out, opsstores.AdoptLoreMD)
	}
	if oldFormat {
		rest, err := adoptOldLeftovers(top, gone)
		if err != nil {
			return nil, err
		}
		out = append(out, rest...)
	}
	if !write {
		return out, nil
	}
	for _, rel := range out {
		if err := os.Remove(filepath.Join(top, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, errs.Wrap(errs.Internal, adoptVerb+": remove "+rel, err)
		}
	}
	pruneEmptyDirs(filepath.Join(top, filepath.FromSlash(opsstores.AdoptDataDir)))
	return out, nil
}

// adoptGeneratedMark is in every LORE.md lore rendered, 0.1.17's spelling
// ("This file is generated by lore render.") and today's alike; a file
// without it is the person's and stays.
const adoptGeneratedMark = "generated by lore render"

// adoptOldLeftovers are the place rows and _meta.json of an old-format
// repo's .lore/data once every other file there is in gone: nil while any
// other file (an unreadable row, a table adopt did not take) is left, so
// the repo keeps a coherent project around what stays.
func adoptOldLeftovers(top string, gone []string) ([]string, error) {
	data := filepath.Join(top, filepath.FromSlash(opsstores.AdoptDataDir))
	skip := map[string]bool{}
	for _, g := range gone {
		skip[g] = true
	}
	var out []string
	other := false
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(top, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		inData := strings.TrimPrefix(rel, opsstores.AdoptDataDir+"/")
		table, _, nested := strings.Cut(inData, "/")
		_, place := adoptSkipped[row.Table(table)]
		switch {
		case skip[rel]:
		case inData == rowfs.MetaFileName, nested && place:
			out = append(out, rel)
		case rowfs.IsOSJunk(d.Name()):
		default:
			other = true
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, errs.Wrap(errs.Internal, adoptVerb+": read "+data, err)
	}
	if other {
		return nil, nil
	}
	return out, nil
}

// pruneEmptyDirs removes the empty folders under dir, dir included,
// deepest first; a folder holding anything stays.
func pruneEmptyDirs(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // fails on a non-empty folder, which stays
	}
}
