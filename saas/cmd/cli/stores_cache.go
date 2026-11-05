// stores_cache.go — the executor half of the external cache (F20, C18):
// it gathers the rows of every source loaded for this code project — the
// resolved org's linked projects, its global project(s) and the personal
// folders for that org — from the bare clones (branch data AND the store
// live refs) and the local personal store, and keeps
// ~/.lore/cache/<project-id>.db current. Nothing here writes inside the
// checkout.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/stores"

	"github.com/thesatellite-ai/lore/schema"
)

// Store live refs (fixed rule 3): a store keeps one live ref per store
// branch; its tree holds one folder per project.
const storeLiveRefPrefix = "refs/lore/live/"

// errLinkUnknownRender: internal context is not rendered while lore cannot
// tell whether this is a client repo (F10 fail-closed rule).
var errLinkUnknownRender = errors.New("cannot tell whether this repo is a client repo; internal context is not rendered")

// externalPlan is what the cache for one code project is built from.
type externalPlan struct {
	ProjectID string
	Org       *lorecfg.Org
	Sources   []cachedb.SourceFiles
	Key       cachedb.Key
	// Notes are sources that could not be read (offline, missing branch);
	// printed by doctor and render, never fatal for reads.
	Notes []string
	// Unreadable are stores that are unreachable AND were never fetched on
	// this machine: their rows are unknown, so a leak check cannot decide.
	Unreadable []string
}

// planExternal gathers every source for the situation. With no org only
// the personal everywhere/ folder loads (F17, 5.6).
func planExternal(ctx context.Context, s *orgSituation) (*externalPlan, error) {
	p := &externalPlan{ProjectID: s.ProjectID, Org: s.Result.Org, Key: cachedb.Key{Commits: map[string]string{}}}
	if s.Result.Org != nil {
		p.Key.Entry = s.Result.Org.Name
		if err := p.addOrgSources(ctx, s); err != nil {
			return nil, err
		}
	}
	if err := p.addPersonalSources(ctx, s); err != nil {
		return nil, err
	}
	return p, nil
}

// addOrgSources adds the linked projects and the global project(s).
func (p *externalPlan) addOrgSources(ctx context.Context, s *orgSituation) error {
	org := s.Result.Org
	m, err := newStoreManager(s.Config, org)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	links := s.Result.Links
	if len(links) == 0 && s.ProjectID != "" {
		// A staging entry chosen on purpose: its own links (per branch).
		if el, ok, err := entryLinks(ctx, s, org); err == nil && ok {
			links = el
		}
	}
	for _, l := range links {
		want[l.Folder] = true
	}
	if s.Committed != nil && s.Committed.Project != "" && s.Result.Role == "own" {
		want[s.Committed.Project] = true
	}
	for _, sc := range org.Stores {
		st, err := m.Open(ctx, sc.URL)
		if err != nil {
			return storeCLIError(err, "open the store")
		}
		fr, err := st.Fetch(ctx, false)
		if err != nil {
			return storeCLIError(err, "fetch the store")
		}
		if fr.Offline {
			if fr.At.IsZero() {
				p.Unreadable = append(p.Unreadable, st.Norm)
				continue
			}
			p.Notes = append(p.Notes, st.Norm+": unreachable, using the last fetched state")
		}
		branch := sc.EffectiveBranch()
		tip, ok, err := st.Tip(ctx, branch)
		if err != nil {
			return storeCLIError(err, "read the store")
		}
		if !ok {
			p.Notes = append(p.Notes, fmt.Sprintf("%s: no branch %s", st.Norm, branch))
			continue
		}
		if err := st.Check(ctx, branch); err != nil {
			return storeCLIError(err, "check the store")
		}
		key := st.Norm + "@" + branch
		p.Key.Commits[key] = tip
		liveTree, liveOID, err := readStoreLive(ctx, st, branch)
		if err != nil {
			return err
		}
		if liveOID != "" {
			p.Key.Commits[key+"#live"] = liveOID
		}
		metas, _, err := st.ReadMetas(ctx, tip)
		if err != nil {
			return storeCLIError(err, "read the store")
		}
		folders := make([]string, 0, len(metas))
		for f := range metas {
			folders = append(folders, f)
		}
		sort.Strings(folders)
		for _, folder := range folders {
			global := metas[folder].Global || (sc.OrgFolder != "" && sc.OrgFolder == folder)
			if !want[folder] && !global {
				continue
			}
			files, _, err := st.ReadProject(ctx, branch, folder)
			if err != nil {
				return storeCLIError(err, "read the store project")
			}
			p.Sources = append(p.Sources, cachedb.SourceFiles{
				Source:    cachedb.StoreSource(org.Name, folder),
				Label:     "[" + org.Name + "/" + folder + "]",
				Files:     files,
				LiveFiles: liveFolder(liveTree, folder),
			})
		}
	}
	return nil
}

// readStoreLive reads a store branch's live ref tree: the local ref when it
// exists (it holds this machine's not yet pushed writes), else the fetched
// copy. Empty when neither exists.
func readStoreLive(ctx context.Context, st *stores.Store, branch string) (map[string][]byte, string, error) {
	local := storeLiveRefPrefix + branch
	tracking, _ := gitremote.TrackingRef(stores.RemoteName, local)
	for _, ref := range []string{local, tracking} {
		oid, ok, err := st.Repo.ResolveRef(ctx, ref)
		if err != nil {
			return nil, "", storeCLIError(err, "read the store live ref")
		}
		if !ok {
			continue
		}
		tree, err := st.Repo.ReadTree(ctx, oid, "")
		if err != nil {
			return nil, "", storeCLIError(err, "read the store live ref")
		}
		return tree, oid, nil
	}
	return nil, "", nil
}

// liveFolder returns one project folder of a live tree, relative to it.
func liveFolder(tree map[string][]byte, folder string) map[string][]byte {
	out := map[string][]byte{}
	prefix := folder + "/"
	if folder == stores.RootFolder {
		prefix = rootLiveFolder + "/"
	}
	for p, b := range tree {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			out[rest] = b
		}
	}
	return out
}

// rootLiveFolder is a root project's folder in a live tree (§2: `_root/`).
const rootLiveFolder = "_root"

// addPersonalSources adds the personal folders loaded here: the resolved
// org's folder and everywhere/ (F17 load rule; never another org's).
func (p *externalPlan) addPersonalSources(ctx context.Context, s *orgSituation) error {
	folders := []string{lorecfg.PersonalEverywhere}
	if org := s.Result.OrgName(); org != "" {
		folders = []string{org, lorecfg.PersonalEverywhere}
	}
	store := personalStoreV017(s.Config)
	if store == "" || store == lorecfg.PersonalStoreLocal {
		home, err := loreHome()
		if err != nil {
			return err
		}
		for _, f := range folders {
			files, err := readDirFiles(filepath.Join(home, personalLocalDir, f, ".lore", "data"))
			if err != nil {
				return err
			}
			if len(files) == 0 {
				continue
			}
			p.Key.Commits["personal:"+f] = filesHash(files)
			p.Sources = append(p.Sources, cachedb.SourceFiles{Source: cachedb.PersonalSource(f), Label: personalLabel, Files: files})
		}
		return nil
	}
	m, err := newStoreManager(s.Config, nil)
	if err != nil {
		return err
	}
	st, err := m.Open(ctx, store)
	if err != nil {
		return storeCLIError(err, "open the personal store")
	}
	if _, err := st.Fetch(ctx, false); err != nil {
		return storeCLIError(err, "fetch the personal store")
	}
	branch := st.DefaultBranch()
	tip, ok, err := st.Tip(ctx, branch)
	if err != nil {
		return storeCLIError(err, "read the personal store")
	}
	if !ok {
		p.Notes = append(p.Notes, "personal store has no branch "+branch+" yet")
		return nil
	}
	p.Key.Commits["personal@"+branch] = tip
	liveTree, _, err := readStoreLive(ctx, st, branch)
	if err != nil {
		return err
	}
	for _, f := range folders {
		files, _, err := st.ReadProject(ctx, branch, f)
		if err != nil {
			return storeCLIError(err, "read the personal store")
		}
		p.Sources = append(p.Sources, cachedb.SourceFiles{Source: cachedb.PersonalSource(f), Label: personalLabel, Files: files, LiveFiles: liveFolder(liveTree, f)})
	}
	return nil
}

// filesHash is a content hash of a file set (the cache key of a local
// personal folder, which has no commit).
func filesHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		h.Write(fmt.Appendf(nil, "%s %x\n", p, sum)) // hash.Hash.Write never returns an error
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cacheUse is why the external cache is opened; it decides how strict a
// missing store is.
type cacheUse int

const (
	// cacheForRead: lists and search; an unreadable store just drops out.
	cacheForRead cacheUse = iota
	// cacheForRender: the internal render; refused while the link status
	// is unknown (F10 fail-closed rule).
	cacheForRender
	// cacheForCheck: a leak check; refused while the link status is unknown
	// or a store was never readable and nothing was cached (F16: "cannot
	// decide → block").
	cacheForCheck
)

// errCannotCheck is the F16 fail-closed refusal.
var errCannotCheck = errors.New("cannot check: store unreachable and nothing cached")

// openExternalCache returns this project's external cache, rebuilt when its
// key changed (see cacheUse for the fail-closed rules).
func openExternalCache(ctx context.Context, s *orgSituation, use cacheUse) (*cachedb.DB, *externalPlan, error) {
	if s.ProjectID == "" {
		return nil, nil, errcodes.New(errcodes.NotProjectRoot, "this project has no project id yet (.lore/data/_meta.json)")
	}
	if use == cacheForRender && s.Result.LinkUnknown {
		return nil, nil, errcodes.New(errcodes.LinkUnknown, errLinkUnknownRender.Error()).
			WithHint("retry when the store is reachable (`lore store doctor`)")
	}
	if use == cacheForCheck && s.Result.LinkUnknown {
		return nil, nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (lore cannot tell whether this is a client repo)").
			WithHint("retry when the store is reachable; bypassing the check is your deliberate `git commit --no-verify`")
	}
	plan, err := planExternal(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	home, err := loreHome()
	if err != nil {
		return nil, nil, err
	}
	db, err := cachedb.Open(ctx, cachedb.Path(filepath.Join(home, stores.CacheDir), s.ProjectID))
	if err != nil {
		return nil, nil, errcodes.New(errcodes.Internal, "open the external cache").WithCause(err)
	}
	if len(plan.Unreadable) > 0 {
		built, err := db.Built(ctx)
		if err != nil {
			_ = db.Close() // the read error is the one to report
			return nil, nil, errcodes.New(errcodes.Internal, "read the external cache").WithCause(err)
		}
		if built {
			// Offline: the last complete cache is the best answer there is.
			plan.Notes = append(plan.Notes, "unreachable, using the cached rows: "+strings.Join(plan.Unreadable, ", "))
			return db, plan, nil
		}
		if use == cacheForCheck {
			_ = db.Close() // refusing
			return nil, nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).
				WithHint("retry when the store is reachable; bypassing the check is your deliberate `git commit --no-verify`")
		}
		plan.Notes = append(plan.Notes, "unreachable: "+strings.Join(plan.Unreadable, ", "))
	}
	cur, err := db.Current(ctx, plan.Key)
	if err != nil {
		_ = db.Close() // the read error is the one to report
		return nil, nil, errcodes.New(errcodes.Internal, "read the external cache").WithCause(err)
	}
	now := cliClock().Now()
	if !cur {
		err = db.Rebuild(ctx, plan.Key, plan.Sources, now)
	} else {
		err = db.Touch(ctx, now)
	}
	if err != nil {
		_ = db.Close() // the write error is the one to report
		return nil, nil, errcodes.New(errcodes.Internal, "update the external cache").WithCause(err)
	}
	return db, plan, nil
}

// externalRowsHere returns the external rows of one table (or matching a
// text) for the project in the working directory — what list and search
// commands append after the repo's own rows, each with its label. A repo
// without an org or personal notes returns nothing. Never fails a read:
// problems are returned as notes.
func externalRowsHere(ctx context.Context, q cachedb.Query) ([]cachedb.Row, []string) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil || s.ResolveErr != nil || s.ProjectID == "" {
		return nil, nil
	}
	db, plan, err := openExternalCache(ctx, s, cacheForRead)
	if err != nil {
		return nil, []string{redactIfQuiet(err.Error())}
	}
	defer func() { _ = db.Close() }() // read-only use
	rows, err := db.Rows(ctx, q)
	if err != nil {
		return nil, []string{err.Error()}
	}
	// RES 3.9: a client repo never lists store resources (fail closed when
	// the core cannot say).
	if coreApp == nil {
		rows = slices.DeleteFunc(rows, func(r cachedb.Row) bool { return r.Table == schema.TableResources })
	} else if client, cerr := coreApp.ClientRepo(ctx); cerr != nil || client {
		rows = slices.DeleteFunc(rows, func(r cachedb.Row) bool { return r.Table == schema.TableResources })
	}
	return rows, plan.Notes
}
