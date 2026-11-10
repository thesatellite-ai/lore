package doctor

// repostores.go — `lore doctor --all` checks every store the known repos
// attach in their .lore/stores.yaml (ONBOARDING_DESIGN 6.1 store.read:
// "each .lore/stores.yaml store of scanned repos"), not only `me` and the
// org roster store, so a broken team store attached in one repo shows up
// without running a command there.
//
//	reposBySetup ─▶ each setup's repos (+ the default setup: the known
//	   repos no setup's folders hold; storeReposOf)
//	   ─▶ RepoStoresEnv.RepoStores(roots)
//	   ─▶ group by (normalised url, branch) per setup: one store, the repos
//	      that attach it (sorted), read-only only when every repo says so
//	   ─▶ drop the org's own store (already checked as "store <org>")
//	   ─▶ store.read/write/marker/private once per store, through the
//	      setup's route (one fetch per store, the prober's 4-at-once cap)
//
// Per setup, not machine-wide: the route (ssh alias, key, https account)
// is the setup's, and two setups may legitimately reach one store with
// different access. A store attached by repos of two setups is therefore
// checked once under each.
//
// Invariant (#16): a store that did not answer (network, VPN, timeout) is a
// warning naming the repos that attach it, never a broken run: the store
// may be fine and only this machine's link is down.

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/ops/setup"
)

// RepoStore is one entry of one known repo's .lore/stores.yaml.
type RepoStore struct {
	// Repo is the root of the repo whose stores.yaml lists it.
	Repo string
	// Name is the store's name in that file.
	Name string
	// URL is what the probe reads: the url cleaned of any credential and of
	// its folder and branch query. It never holds a secret.
	URL string
	// Key is the store's identity shared by every spelling of its url
	// (scp form, https, a trailing slash or .git); "" when it could not be
	// normalised, which makes the url itself the key.
	Key string
	// Branch is the pinned store branch ("" = the default branch).
	Branch string
	// ReadOnly is the entry's read_only.
	ReadOnly bool
	// Org is the org (config.yaml orgs[]) this url is the store of, "" for
	// none: a member/admin setup of that org already checks it as
	// "store <org>".
	Org string
}

// RepoStoresEnv is the optional upgrade of SetupEnv that lists the stores
// repos attach; *wire.App implements it. Without it --all checks only `me`
// and the org store, as before.
type RepoStoresEnv interface {
	// RepoStores reads each root's .lore/stores.yaml (a repo without one
	// lists nothing). warnings has one line per repo whose file could not
	// be read; its stores are then not checked.
	RepoStores(ctx context.Context, roots []string) (stores []RepoStore, warnings []string)
}

// attachedStore is one distinct store of one setup's repos.
type attachedStore struct {
	name, url, branch string
	readOnly          bool
	// repos attach it, sorted.
	repos []string
	// target is the result's target, "store <name>[@<branch>]", unique in
	// the setup.
	target string
}

// role is the 6.1 level context: store.write is a warning for a store
// attached read-only.
func (as *attachedStore) role() onboard.StoreRole {
	if as.readOnly {
		return onboard.StoreRoleAttachedReadOnly
	}
	return onboard.StoreRoleAttached
}

// detailAttachedReadOnly is the skipped store.write of a store every repo
// attaches read-only (6.1 "warn for a store attached --read-only (skip)"):
// lore never writes it from here, so push access is not this machine's
// concern.
const detailAttachedReadOnly = "attached read-only: lore does not write it, so the write test is skipped"

// skipsWrite reports whether check id is the write test of a read-only
// attached store (false for a nil store: not an attached-store item).
func (as *attachedStore) skipsWrite(id onboard.CheckID) bool {
	return as != nil && as.readOnly && id == onboard.CheckStoreWrite
}

// attachedBy is the suffix naming the repos that attach a store.
func (as *attachedStore) attachedBy() string {
	return "attached by " + strings.Join(as.repos, ", ")
}

// adjust applies the #16 rules to a probe result of this store: a store
// that did not answer is a warning (never broken), and the fix's
// {store_name} is the store's name (the prober was asked by url). The
// repos are not named here: the result is cached, and the repos that
// attach a store can change while it is (withRepos names them per run).
func (as *attachedStore) adjust(pr setup.ProbeResult) setup.ProbeResult {
	vars := make(map[string]string, len(pr.Vars)+1)
	for k, v := range pr.Vars {
		vars[k] = v
	}
	vars[onboard.VarStoreName] = as.name
	pr.Vars = vars
	if pr.Status == onboard.StatusFail && onboard.FixVariant(vars[onboard.VarFixVariant]) == onboard.FixUnreachable {
		pr.Status = onboard.StatusWarn
	}
	return pr
}

// withRepos is r with its detail naming the repos that attach the store
// in this run.
func (as *attachedStore) withRepos(r onboard.CheckResult) onboard.CheckResult {
	if r.Detail == "" {
		r.Detail = as.attachedBy()
	} else {
		r.Detail += " (" + as.attachedBy() + ")"
	}
	return r
}

// storeIdentity is the dedup key of a store: normalised url and branch.
type storeIdentity struct{ key, branch string }

// UncheckedNote is doctor --all's info line about a known repo under no
// setup's folders whose stores were not checked: with no setup marked
// default, no setup applies to it, so no account to reach them as is known.
func UncheckedNote(root string) string {
	return root + ": its stores were not checked: no setup applies to it (no setup's folders hold it and no setup has default: true)"
}

// storeReposOf is the repos whose stores each setup checks: repos (the
// repos its folders hold) plus, for the default setup, the unassigned
// known repos, because the default is the setup lore uses in a folder no
// setup's folders hold (3.3). Repo checks still use repos only. With no
// default, each unassigned repo that attaches a store gets an
// UncheckedNote instead (only on a run of every setup: a --setup run is
// about one setup). The input map is not changed.
func (sr *setupRun) storeReposOf(ctx context.Context, enabled, entries []onboard.SetupEntry, repos map[string][]string, unassigned []string, only string) (map[string][]string, []string) {
	if sr.stores == nil || len(unassigned) == 0 {
		return repos, nil
	}
	def := slices.IndexFunc(enabled, func(e onboard.SetupEntry) bool { return e.Default })
	if def < 0 {
		if only != "" {
			return repos, nil
		}
		list, _ := sr.stores.RepoStores(ctx, unassigned) // unreadable files: not checked either way
		var notes []string
		for _, root := range unassigned {
			if slices.ContainsFunc(list, func(rs RepoStore) bool { return rs.Repo == root }) {
				notes = append(notes, UncheckedNote(root))
			}
		}
		return repos, notes
	}
	name := enabled[def].Name
	if !slices.ContainsFunc(entries, func(e onboard.SetupEntry) bool { return e.Name == name }) {
		return repos, nil
	}
	out := make(map[string][]string, len(repos)+1)
	for k, v := range repos {
		out[k] = v
	}
	out[name] = append(slices.Clone(repos[name]), unassigned...)
	return out, nil
}

// attachedBySetup lists, per setup, the distinct stores its repos attach
// (groupAttached), and the warnings of repos whose stores.yaml could not
// be read. Without RepoStoresEnv: nothing.
func (sr *setupRun) attachedBySetup(ctx context.Context, entries []onboard.SetupEntry, repos map[string][]string) (map[string][]attachedStore, []string) {
	if sr.stores == nil {
		return nil, nil
	}
	out := map[string][]attachedStore{}
	var warnings []string
	for _, e := range entries {
		roots := repos[e.Name]
		if len(roots) == 0 {
			continue
		}
		list, ws := sr.stores.RepoStores(ctx, roots)
		warnings = append(warnings, ws...)
		out[e.Name] = groupAttached(e, list)
	}
	return out, warnings
}

// groupAttached is the pure part of attachedBySetup: the entries of one setup's
// repos grouped by (normalised url, branch), first seen order by repo
// root then name, the org's own store left out (checked as "store <org>").
// Targets are "store <name>", "store <name>@<branch>" for a pinned branch,
// and a "~2", "~3" … suffix when two different stores share a name.
func groupAttached(e onboard.SetupEntry, list []RepoStore) []attachedStore {
	list = slices.Clone(list)
	slices.SortStableFunc(list, func(x, y RepoStore) int {
		if c := strings.Compare(x.Repo, y.Repo); c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
	var out []attachedStore
	at := map[storeIdentity]int{}
	for _, rs := range list {
		if rs.URL == "" {
			continue
		}
		if rs.Org != "" && e.Kind.HasOrg() && rs.Org == e.Org {
			continue
		}
		key := rs.Key
		if key == "" {
			key = rs.URL
		}
		id := storeIdentity{key, rs.Branch}
		if i, ok := at[id]; ok {
			as := &out[i]
			if !slices.Contains(as.repos, rs.Repo) {
				as.repos = append(as.repos, rs.Repo)
			}
			// Writable when any repo attaches it writable: that repo writes.
			as.readOnly = as.readOnly && rs.ReadOnly
			continue
		}
		at[id] = len(out)
		out = append(out, attachedStore{name: rs.Name, url: rs.URL, branch: rs.Branch, readOnly: rs.ReadOnly, repos: []string{rs.Repo}})
	}
	taken := map[string]bool{}
	for _, s := range storesOf(e) {
		taken[TargetStore(s.name)] = true
	}
	for i := range out {
		base := TargetStore(out[i].name)
		if out[i].branch != "" {
			base += "@" + out[i].branch
		}
		t := base
		for n := 2; taken[t]; n++ {
			t = base + "~" + strconv.Itoa(n)
		}
		taken[t] = true
		out[i].target = t
		slices.Sort(out[i].repos)
	}
	return out
}
