package orgcfg

// linkwrite.go — the store side of `lore org link` (LORE_TEAM_SPEC F11
// steps 2-5, F13) for cmd/lore (P3-CMDLORE-4): set-union one link into a
// store folder's _meta.json in the store's clone (the same clone the store
// backend and Links read, so the new link is seen at once), commit as the org
// identity and push with retry. Ported from v0.1.17's stores.WriteLink and
// the CLI's pickLinkStore.
//
//	WriteLink ─▶ pick the store (the one already holding the folder, else
//	             --store / the entry's first) ─▶ loop: fetch ─▶ tip ─▶
//	             marker check ─▶ read _meta.json (or a new one) ─▶ add the
//	             link ─▶ commit on the tip ─▶ advance the local branch
//	             (the checkout follows when it is that branch) ─▶ push
//	             (a lost race fetches and builds again on the new tip)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// Link write defaults.
const (
	// linkPushRetries is how often a push that lost a race is rebuilt on
	// the new tip (v0.1.17 stores.DefaultPushRetries).
	linkPushRetries = 5
	// linkMessage is the commit subject (v0.1.17 stores msgLink).
	linkMessage = "lore: link %s"
	// metaVersionKey / metaProjectKey / metaLinksKey are _meta.json keys.
	metaVersionKey = "_v"
	metaProjectKey = "project_id"
	metaLinksKey   = "links"
	// metaFormatVersion is the `_v` a new _meta.json gets (v0.1.17's).
	metaFormatVersion = "1"
	// storeMarkerFile is the store marker (rowfs.MarkerFile; not imported:
	// orgcfg reads clones with git only).
	storeMarkerFile = "lore-store.yaml"
	// envAutopush=0 turns pushing off (v0.1.17 LORE_LIVE_AUTOPUSH).
	envAutopush    = "LORE_LIVE_AUTOPUSH"
	envAutopushOff = "0"
)

// Errors of WriteLink; the caller maps them onto lore error codes.
var (
	// ErrStoreOffline: the store could not be fetched and no copy exists.
	ErrStoreOffline = errors.New("orgcfg: store is offline")
	// ErrNoStoreBranch: the store has no such branch (never created here).
	ErrNoStoreBranch = errors.New("orgcfg: store branch does not exist")
	// ErrNotAStore: the branch has no lore-store.yaml (a code repo).
	ErrNotAStore = errors.New("orgcfg: not a lore store")
	// ErrFolderElsewhere: --store names one store, the folder is in another.
	ErrFolderElsewhere = errors.New("orgcfg: folder exists in another store")
	// ErrLinkRace: every push lost a race.
	ErrLinkRace = errors.New("orgcfg: store push still rejected")
	// ErrStoreRefused: the store answered and refused the push for a
	// reason other than a lost race (a hook decline, branch protection).
	ErrStoreRefused = errors.New("orgcfg: the store refused the push")
)

// LinkWrite is one link to write.
type LinkWrite struct {
	// Plan is the validated link (orgresolve.PlanLink).
	Plan orgresolve.LinkPlan
	// Signature is the commit author (the org identity); When is set.
	Signature gitref.Signature
	// FolderProjectID names the project of a folder the link creates
	// (normalised store url, branch, folder).
	FolderProjectID func(store, branch, folder string) (string, error)
	// NewMarker encodes a new lore-store.yaml for a store whose branch has
	// lore files but no marker yet (its first lore write predates it): the
	// link commit adds it, as `lore store init` would have. nil refuses
	// such a store (ErrNotAStore).
	NewMarker func() ([]byte, error)
}

// LinkWriteResult says what happened.
type LinkWriteResult struct {
	// StoreURL is the normalised store the link went to.
	StoreURL string
	// CreatedFolder is true when the folder's _meta.json was made.
	CreatedFolder bool
	// Changed is true when the link was new or different.
	Changed bool
	// Pushed is true when the commit reached the remote.
	Pushed bool
	// Offline is true when it was committed locally only (remote down).
	Offline bool
	// NoPush is true when autopush is off.
	NoPush bool
}

// WriteLink writes w.Plan into the store (see the file comment).
func (g *GitLinks) WriteLink(ctx context.Context, w LinkWrite) (LinkWriteResult, error) {
	o := g.Config.FindOrg(w.Plan.Entry)
	if o == nil {
		return LinkWriteResult{}, fmt.Errorf("orgcfg: no org entry named %s", w.Plan.Entry)
	}
	target, err := g.pickLinkStore(ctx, w.Plan)
	if err != nil {
		return LinkWriteResult{}, err
	}
	res := LinkWriteResult{StoreURL: target, NoPush: !g.autopush(o.Live)}
	cr, err := commitStore(ctx, storeCommit{Dir: g.cloneDirOf(target), Store: target, Branch: w.Plan.Branch, NoPush: res.NoPush, Route: g.Route, NewMarker: w.NewMarker,
		FirstCommit: w.NewMarker != nil,
		Message:     fmt.Sprintf(linkMessage, w.Plan.ProjectID), Signature: w.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			put, created, changed, cerr := linkChange(ctx, repo, tip, target, w)
			res.CreatedFolder = res.CreatedFolder || created
			res.Changed = res.Changed || changed
			return put, cerr
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	return res, err
}

// storeCommit is one change to commit into a store's clone.
type storeCommit struct {
	// Dir is the clone, Store its normalised url (for messages), Branch
	// the store branch.
	Dir, Store, Branch string
	// NoPush commits in the clone only (autopush off).
	NoPush bool
	// Message and Signature make the commit.
	Message   string
	Signature gitref.Signature
	// Change returns the files to put at tip; an empty map on the first
	// attempt means "already so" (nothing committed or pushed).
	Change func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error)
	// Route reaches the store as its setup's account (fetch, push).
	Route Router
	// NewMarker, when set, adds a marker to a tip of lore files only that
	// has none (LinkWrite.NewMarker), in the same commit.
	NewMarker func() ([]byte, error)
	// FirstCommit lets a store repo with no branch at all (a brand-new
	// empty repo `lore org store add` just listed) get its first commit:
	// the marker and Change's files, on a new root commit (the docs'
	// "a brand-new empty repo becomes a lore store with the first write to
	// it"). It needs NewMarker; a repo with other branches still refuses a
	// missing branch (a typo must not start a second line).
	FirstCommit bool
	// PushPending, when Change has nothing to put, still pushes a local
	// branch that is ahead of the remote's (an earlier commit made with
	// NoPush, B-4: `lore org store add --push` after an add without it).
	PushPending bool
}

// storeCommitResult says where the commit went.
type storeCommitResult struct {
	// Pushed: the commit reached the remote; Offline: committed locally
	// only because the remote could not be reached.
	Pushed, Offline bool
}

// commitStore commits c into the clone under its write lock and pushes it
// with retry: the marker is checked on every tip, and a push that lost a
// race fetches and builds c.Change again on the remote's new tip (shared
// by `lore org link` and `lore org create`).
func commitStore(ctx context.Context, c storeCommit) (storeCommitResult, error) {
	var res storeCommitResult
	dir, branch, target := c.Dir, c.Branch, c.Store
	// One writer per clone: the store lock every writer of the store takes
	// (row batches, folder edits, links), so two racing commits never
	// fight over its refs or its checkout (F11 race).
	ctx, unlock, err := gitbackend.LockClone(ctx, dir)
	if err != nil {
		return res, err
	}
	defer unlock()
	for attempt := 0; attempt <= linkPushRetries; attempt++ {
		offline := false
		if attempt > 0 {
			offline = fetch(ctx, c.Route, dir) != nil
		}
		tip, ok := branchTip(ctx, dir, branch)
		first := false // the store's first commit, into an empty repo
		if !ok {
			if !c.FirstCommit || c.NewMarker == nil || !cloneHasNoBranches(ctx, dir) {
				return res, fmt.Errorf("%w: %s has no branch %s", ErrNoStoreBranch, target, branch)
			}
			first = true
		}
		repo, err := gitref.Open(ctx, dir, nil)
		if err != nil {
			return res, err
		}
		if attempt > 0 && !offline {
			// The push lost a race: build on the remote's new tip (the
			// local branch holds this run's rejected commit, which would
			// never fast-forward).
			if t, tok, terr := repo.ResolveRef(ctx, trackingRef(branch)); terr == nil && tok {
				tip = t
			}
		}
		old, hasOld, oerr := repo.ResolveRef(ctx, localRef(branch))
		if oerr != nil {
			return res, oerr
		}
		if !hasOld {
			old = ""
		}
		hasMarker := false
		if !first {
			_, found, merr := repo.ReadFile(ctx, tip, storeMarkerFile)
			if merr != nil {
				return res, merr
			}
			hasMarker = found
		}
		var marker []byte
		if first {
			if marker, err = c.NewMarker(); err != nil {
				return res, err
			}
		} else if !hasMarker {
			// lore files only and no marker: adopted (the marker goes in
			// this commit); anything else is not a store
			if c.NewMarker == nil || checkStoreTree(ctx, dir, tip, target, branch) != nil {
				return res, fmt.Errorf("%w: %s@%s has no %s", ErrNotAStore, target, branch, storeMarkerFile)
			}
			if marker, err = c.NewMarker(); err != nil {
				return res, err
			}
		}
		put, err := c.Change(ctx, repo, tip)
		if err != nil {
			return res, err
		}
		if len(put) > 0 && marker != nil {
			put[storeMarkerFile] = marker
		}
		if len(put) == 0 && attempt == 0 && !(c.PushPending && !c.NoPush && aheadOfRemote(ctx, repo, branch, tip)) {
			return res, nil // already so: nothing to commit or push
		}
		oid := tip
		if len(put) > 0 {
			cm, cerr := repo.MakeCommit(ctx, gitref.CommitRequest{Base: tip, Changes: gitref.Changes{Put: put},
				Message: c.Message, Signature: c.Signature})
			if cerr != nil {
				return res, fmt.Errorf("orgcfg: commit on %s: %w", branch, cerr)
			}
			oid = cm.OID
		}
		if old != oid {
			if err := gitbackend.AdvanceBranch(ctx, dir, branch, oid, old); err != nil {
				return res, err
			}
		}
		if c.NoPush || offline {
			res.Offline = offline
			return res, nil
		}
		// B2-28 / L-2: every org push (links, lore-org.yaml, the source
		// stamp) passes the backend's reset check: nothing goes into a
		// store repo that was deleted and made again.
		gerr := gitbackend.GuardPushAt(ctx, dir, branch, oid)
		if gerr == nil && attempt == 0 {
			// The first attempt fetched nothing, so the tracking ref may
			// be stale: ask the remote itself, one call (an unreachable
			// remote is left to the push, which reports it).
			gerr = guardFromRemote(ctx, c.Route, dir, branch, oid)
		}
		if gerr != nil {
			if errors.Is(gerr, gitbackend.ErrRemoteReset) {
				return res, fmt.Errorf("%w: %s %s: %w", ErrStoreRefused, target, branch, gerr)
			}
			return res, gerr
		}
		out, perr := netGit(ctx, c.Route, dir, "push", "--quiet", "--porcelain", remoteName, oid+":"+localRef(branch))
		if perr == nil {
			res.Pushed = true
			gitbackend.NoteRemoteTipAt(ctx, dir, branch, oid)
			reportPush(ctx, StorePush{Store: target, Branch: branch, Commit: oid})
			_ = moveRef(ctx, repo, trackingRef(branch), oid) // the next fetch sets it anyway
			return res, nil
		}
		switch outcome, summary := classifyPush(out); outcome {
		case pushLostRace:
			continue // fetch and rebuild on the remote's new tip
		case pushRefused:
			return res, fmt.Errorf("%w: %s %s: %s", ErrStoreRefused, target, branch, summary)
		case pushUnreachable:
			res.Offline = true // unreachable: the next write or fetch pushes the local commit
			return res, nil
		}
	}
	return res, fmt.Errorf("%w: %s %s after %d attempts (another writer keeps winning)", ErrLinkRace, target, branch, linkPushRetries+1)
}

// linkChange is the file to put for the link at tip: the folder's
// _meta.json with the link set-unioned in (created when the folder has
// none). An empty map means the link is already there.
func linkChange(ctx context.Context, repo *gitref.Repo, tip, store string, w LinkWrite) (put map[string][]byte, created, changed bool, err error) {
	path, err := metaPathAt(ctx, repo, tip, w.Plan.Folder)
	if err != nil {
		return nil, false, false, err
	}
	var raw []byte
	found := false
	if tip != "" { // "" is an empty store's first commit: no file yet
		if raw, found, err = repo.ReadFile(ctx, tip, path); err != nil {
			return nil, false, false, err
		}
	}
	doc := map[string]json.RawMessage{}
	if found {
		if jerr := json.Unmarshal(raw, &doc); jerr != nil {
			return nil, false, false, fmt.Errorf("orgcfg: %s in %s: %w", path, store, jerr)
		}
	} else {
		pid, perr := w.FolderProjectID(store, w.Plan.Branch, w.Plan.Folder)
		if perr != nil {
			return nil, false, false, perr
		}
		pj, _ := json.Marshal(pid) // a string never fails to encode
		doc[metaVersionKey] = json.RawMessage(metaFormatVersion)
		doc[metaProjectKey] = pj
		created = true
	}
	var links []linkRecord
	if lr, ok := doc[metaLinksKey]; ok && string(lr) != "null" {
		if jerr := json.Unmarshal(lr, &links); jerr != nil {
			return nil, false, false, fmt.Errorf("orgcfg: %s in %s: links: %w", path, store, jerr)
		}
	}
	want := linkRecord{ProjectID: w.Plan.ProjectID, Remote: w.Plan.Remote, Own: w.Plan.Own, Default: w.Plan.Default}
	links, changed = addLink(links, want)
	if !changed && !created {
		return nil, false, false, nil
	}
	lb, err := json.Marshal(links)
	if err != nil {
		return nil, false, false, err
	}
	doc[metaLinksKey] = lb
	out, err := json.MarshalIndent(doc, "", "  ") // map keys encode sorted: one byte form on every machine
	if err != nil {
		return nil, false, false, err
	}
	return map[string][]byte{path: append(out, '\n')}, created, changed, nil
}

// addLink set-unions l into links keyed by (project id, remote), replacing
// a different record for the same key; sorted by remote then project id.
func addLink(links []linkRecord, l linkRecord) ([]linkRecord, bool) {
	changed, found := false, false
	for i := range links {
		if links[i].ProjectID == l.ProjectID && links[i].Remote == l.Remote {
			found = true
			if links[i] != l {
				links[i], changed = l, true
			}
		}
	}
	if !found {
		links, changed = append(links, l), true
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Remote != links[j].Remote {
			return links[i].Remote < links[j].Remote
		}
		return links[i].ProjectID < links[j].ProjectID
	})
	return links, changed
}

// pickLinkStore is F11 step 1 + F13: the store already holding the folder;
// else --store (or the entry's first store). A folder that exists in
// another store than an explicit --store names is refused. Every store is
// fetched now (a link is a rare, deliberate change).
func (g *GitLinks) pickLinkStore(ctx context.Context, plan orgresolve.LinkPlan) (string, error) {
	o := g.Config.FindOrg(plan.Entry)
	named := ""
	holder := ""
	for _, sc := range o.Stores {
		n, nerr := repourl.NormalizeStore(sc.URL)
		if nerr != nil {
			return "", fmt.Errorf("orgcfg: store url %q of %s: %w", sc.URL, o.Name, nerr)
		}
		if n == plan.StoreURL && named == "" {
			named = n
		}
		dir, own := cloneDir(g.Home, n)
		if berr := ensureClone(ctx, dir, sc.URL, own); berr != nil {
			return "", berr
		}
		if ferr := fetchLocked(ctx, g.Route, dir, g.now()); ferr != nil && lastFetch(dir).IsZero() {
			return "", fmt.Errorf("%w: %s (cannot tell which store holds %s/)", ErrStoreOffline, n, plan.Folder)
		}
		if holder != "" {
			continue
		}
		if tip, ok := branchTip(ctx, dir, sc.EffectiveBranch()); ok {
			for _, p := range []string{metaPathOf(plan.Folder), oldMetaPathOf(plan.Folder)} {
				if _, rerr := gitRun(ctx, "-C", dir, "cat-file", "-e", tip+":"+p); rerr == nil {
					holder = n
				}
			}
		}
	}
	if named == "" {
		return "", fmt.Errorf("orgcfg: %s is not one of %s's stores", plan.StoreURL, o.Name)
	}
	if holder == "" || holder == named {
		return named, nil
	}
	if plan.StoreExplicit {
		return "", fmt.Errorf("%w: %s/ is in %s, not in %s", ErrFolderElsewhere, plan.Folder, holder, named)
	}
	return holder, nil
}

// autopush reports whether store writes push at once: LORE_LIVE_AUTOPUSH=0
// turns it off; else the entry's live.autopush, the top level's, true.
func (g *GitLinks) autopush(entry *orgconf.Live) bool {
	if os.Getenv(envAutopush) == envAutopushOff {
		return false
	}
	if entry != nil && entry.Autopush != nil {
		return *entry.Autopush
	}
	if g.Config.Live.Autopush != nil {
		return *g.Config.Live.Autopush
	}
	return true
}

// metaPathOf is a folder's _meta.json in the store layout
// ("<folder>/_meta.json"; "." = the store root's).
func metaPathOf(folder string) string {
	return rowfs.LayoutStore.StorePath(folder, rowfs.MetaFileName)
}

// oldMetaPathOf is the same file in the old layout (<folder>/.lore/data/_meta.json).
func oldMetaPathOf(folder string) string {
	if folder == "" || folder == "." {
		return oldMetaPathSuffix
	}
	return folder + "/" + oldMetaPathSuffix
}

// metaPathAt is where folder's _meta.json is at tip: the old place when
// only that exists (a folder not moved yet keeps one _meta.json), else
// the store layout's.
func metaPathAt(ctx context.Context, repo *gitref.Repo, tip, folder string) (string, error) {
	if tip == "" {
		return metaPathOf(folder), nil // an empty store: nothing to read yet
	}
	if _, found, err := repo.ReadFile(ctx, tip, metaPathOf(folder)); err != nil || found {
		return metaPathOf(folder), err
	}
	if _, found, err := repo.ReadFile(ctx, tip, oldMetaPathOf(folder)); err != nil || found {
		return oldMetaPathOf(folder), err
	}
	return metaPathOf(folder), nil
}

// cloneHasNoBranches reports whether the clone at dir holds no branch at
// all, local or fetched: the remote is a brand-new empty repo (an error
// reading the refs counts as "has branches", the safe answer).
func cloneHasNoBranches(ctx context.Context, dir string) bool {
	out, err := gitRun(ctx, "-C", dir, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	return err == nil && strings.TrimSpace(out) == ""
}

// localRef / trackingRef name a store branch in the clone.
func localRef(branch string) string    { return "refs/heads/" + branch }
func trackingRef(branch string) string { return "refs/remotes/" + remoteName + "/" + branch }

// aheadOfRemote reports whether tip is not the remote-tracking tip of
// branch: a local commit not pushed yet (no tracking ref counts too).
func aheadOfRemote(ctx context.Context, repo *gitref.Repo, branch, tip string) bool {
	t, ok, err := repo.ResolveRef(ctx, trackingRef(branch))
	return err == nil && (!ok || t != tip)
}

// moveRef points ref at oid from wherever it is now.
func moveRef(ctx context.Context, repo *gitref.Repo, ref, oid string) error {
	old, ok, err := repo.ResolveRef(ctx, ref)
	if err != nil {
		return err
	}
	if !ok {
		old = ""
	}
	if old == oid {
		return nil
	}
	return repo.UpdateRef(ctx, ref, oid, old)
}

// pushOutcome is what a failed store push means for commitStore.
type pushOutcome int

const (
	// pushUnreachable: no ref verdict came back (the remote could not be
	// reached): keep the commit locally, the next write or fetch pushes it.
	pushUnreachable pushOutcome = iota
	// pushLostRace: another push moved the branch first: fetch and rebuild.
	pushLostRace
	// pushRefused: the remote answered and refused for another reason.
	pushRefused
)

// porcelainRejected is the `git push --porcelain` flag of a refused ref
// line "<flag>\t<from>:<to>\t<summary>" (git-push(1) "Output").
const porcelainRejected = "!"

// lostRaceReasons are rejection reasons meaning another push moved the
// branch first: the client-side "fetch first" / "non-fast-forward" /
// "stale info", and receive-pack's compare-and-swap losses, which are the
// same race found one step later on the server ("[remote rejected]
// (reference already exists)" when the other push still held the ref
// lock, "failed to update ref", "incorrect old value provided", ...; the
// same list as adapters/backend/git and adapters/gitremote). Hook declines
// and protection refusals carry other reasons and never match. A server
// race that is not retried would be reported as offline and lose the
// write (F11 race: one of two concurrent links vanished under load).
var lostRaceReasons = []string{
	"non-fast-forward", "fetch first", "stale info",
	"incorrect old value provided", "cannot lock ref", "failed to lock", "reference already exists",
	"atomic transaction failed", "failed to update ref", "unable to update local ref",
}

// classifyPush reads a failed push's porcelain stdout: a refused ref line
// whose reason is a lost race is pushLostRace, any other refused ref line
// is pushRefused (with its summary), no refused ref line is
// pushUnreachable.
func classifyPush(out string) (pushOutcome, string) {
	for _, line := range strings.Split(out, "\n") {
		flag, rest, ok := strings.Cut(line, "\t")
		if !ok || flag != porcelainRejected {
			continue
		}
		_, summary, ok := strings.Cut(rest, "\t")
		if !ok {
			continue
		}
		for _, r := range lostRaceReasons {
			if strings.Contains(summary, r) {
				return pushLostRace, summary
			}
		}
		return pushRefused, summary
	}
	return pushUnreachable, ""
}

// guardFromRemote asks the remote for its branches (`git ls-remote
// --heads`, one network call) and, when the store branch is not there,
// runs the backend's reset rule as a sync would: an org write must not
// push the whole store into a repo that was deleted and made again,
// although this clone's tracking ref still shows the old branch.
func guardFromRemote(ctx context.Context, r Router, dir, branch, h string) error {
	out, err := netGit(ctx, r, dir, "ls-remote", "--heads", "--", remoteName)
	if err != nil {
		return nil // unreachable: the push says so
	}
	noHeads, found := true, false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		noHeads = false
		if strings.TrimSpace(ref) == localRef(branch) {
			found = true
		}
	}
	if found {
		return nil
	}
	return gitbackend.GuardPushGone(ctx, dir, branch, h, noHeads)
}
