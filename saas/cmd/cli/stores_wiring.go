// stores_wiring.go — the executor half of stores (LORE_TEAM_SPEC §5, F11,
// F12, F13): it builds the stores.Manager for this machine (~/.lore, the
// fetch TTL from config, the clock), the real link source for org
// resolution, the store-side `lore org link` writer, and maps stores errors
// onto the CLI's codes. Every command that touches a store goes through
// here so the clone layout, the checks and the messages are the same
// everywhere.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/repourl"
	"saas/pkg/aicoder/stores"
)

// envStoreAutopush mirrors live.autopush for store writes: LORE_LIVE_AUTOPUSH=0
// means lore never touches the network on its own (§1 env vars); store
// writes are then committed in the clone and pushed by `lore sync`.
const envStoreAutopush = "LORE_LIVE_AUTOPUSH"

// storeHintInit is the one way to make a repo a store (F12).
const storeHintInit = "create one with `lore store init <url>` in a new empty repo"

// loreHome returns ~/.lore (identity.Dir honours HOME and the test env).
func loreHome() (string, error) {
	d, err := identity.Dir()
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
	}
	return d, nil
}

// newStoreManager builds the Manager for this machine. org may be nil (no
// org resolved): the top-level live: settings apply.
func newStoreManager(cfg *lorecfg.Config, org *lorecfg.Org) (*stores.Manager, error) {
	home, err := loreHome()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &lorecfg.Config{}
	}
	return &stores.Manager{
		Layout:   stores.Layout{Home: home},
		Clock:    cliClock(),
		FetchTTL: cfg.LiveFor(org).FetchTTL,
	}, nil
}

// storeAutopush reports whether store writes push at once.
func storeAutopush(cfg *lorecfg.Config, org *lorecfg.Org) bool {
	if os.Getenv(envStoreAutopush) == envValueOff0 {
		return false
	}
	if cfg == nil {
		return true
	}
	return cfg.LiveFor(org).Autopush
}

// envValueOff0 is the "off" value of LORE_LIVE_AUTOPUSH.
const envValueOff0 = "0"

// storeSignature is the author of every store commit: the org entry's
// identity (§6.5 — never a personal git email). With no org (`lore store
// init` before any config exists) the fallback identity is used, which is
// the only identity there is.
func storeSignature(org *lorecfg.Org) gitref.Signature {
	when := cliClock().Now()
	if org != nil && org.Identity.Key != "" {
		name := org.Identity.Name
		if name == "" {
			name = org.Name
		}
		return gitref.Signature{Name: name, Email: emailOfKey(org.Identity.Key), When: when}
	}
	r := identity.Resolve(identity.Inputs{})
	name := r.DisplayName
	if name == "" {
		name = "lore"
	}
	email := emailOfKey(r.StableKey)
	if email == "" {
		email = fallbackStoreEmail
	}
	return gitref.Signature{Name: name, Email: email, When: when}
}

// fallbackStoreEmail is used only when no identity names an email at all.
const fallbackStoreEmail = "lore@localhost"

// emailOfKey turns "human:alice@x" into "alice@x" ("" when it holds no email).
func emailOfKey(key string) string {
	_, rest, ok := strings.Cut(key, ":")
	if !ok || !strings.Contains(rest, "@") {
		return ""
	}
	return rest
}

// orgForStoreURL returns the first org entry listing url among its stores,
// and that store's config (nil when no entry lists it).
func orgForStoreURL(cfg *lorecfg.Config, url string) (*lorecfg.Org, *lorecfg.Store) {
	if cfg == nil {
		return nil, nil
	}
	n, err := repourl.Normalize(url)
	if err != nil {
		return nil, nil
	}
	for i := range cfg.Orgs {
		if s := stores.StoreFor(&cfg.Orgs[i], n); s != nil {
			return &cfg.Orgs[i], s
		}
	}
	return nil, nil
}

// storeCLIError maps a stores error onto the CLI envelope (E_NOT_A_STORE,
// E_OFFLINE, missing branch); other errors pass through as internal.
func storeCLIError(err error, what string) error {
	if err == nil {
		return nil
	}
	var ce *errcodes.CLIError
	if errors.As(err, &ce) {
		return err
	}
	switch {
	case errors.Is(err, stores.ErrNotAStore):
		return errcodes.New(errcodes.NotAStore, redactIfQuiet(err.Error())).WithHint(storeHintInit)
	case errors.Is(err, stores.ErrNoBranch):
		return errcodes.New(errcodes.InvalidInput, err.Error()).
			WithHint("a store branch is created only on purpose: `lore store init <url> --branch <branch>`")
	case errors.Is(err, stores.ErrOffline):
		return errcodes.New(errcodes.Offline, err.Error()).WithHint("retry when the store is reachable")
	case errors.Is(err, stores.ErrNotAProject), errors.Is(err, stores.ErrFolderMissing), errors.Is(err, stores.ErrFolderElsewhere):
		return errcodes.New(errcodes.InvalidInput, err.Error())
	case errors.Is(err, stores.ErrBadStoreURL):
		return errcodes.New(errcodes.InvalidInput, err.Error())
	}
	return errcodes.New(errcodes.Internal, what).WithCause(err)
}

// storeLinkSource is the LinkSource org resolution uses once a config with
// org entries exists: the real store scan (F11), with the clone's pending
// writes pushed on the way (an offline write is pushed by the next
// command, §5.3). Without org entries it is orgresolve.NoLinks.
func storeLinkSource(cfg *lorecfg.Config) (orgresolve.LinkSource, error) {
	if cfg == nil || len(cfg.Orgs) == 0 {
		return orgresolve.NoLinks{}, nil
	}
	m, err := newStoreManager(cfg, nil)
	if err != nil {
		return nil, err
	}
	return &pushingLinkSource{inner: &stores.LinkSource{Manager: m, Config: cfg}, m: m, cfg: cfg}, nil
}

// noProjectIDDetail explains why no store was read for a repo without lore.
const noProjectIDDetail = "no project id here yet, so no store link can name this repo"

// pushingLinkSource wraps the link scan and pushes waiting store writes of
// the stores it just reached.
type pushingLinkSource struct {
	inner *stores.LinkSource
	m     *stores.Manager
	cfg   *lorecfg.Config
}

// Links implements orgresolve.LinkSource.
func (p *pushingLinkSource) Links(ctx context.Context, projectID string, remotes []string) (orgresolve.LinkResult, error) {
	if projectID == "" {
		// No project id yet (before `lore init`): no link can name this
		// repo, so no store is read; init itself must not depend on a store.
		return orgresolve.LinkResult{Status: orgresolve.LinkStatusKnown, Detail: noProjectIDDetail}, nil
	}
	res, err := p.inner.Links(ctx, projectID, remotes)
	if err != nil {
		return res, err
	}
	if storeAutopush(p.cfg, nil) {
		pushPendingStores(ctx, p.cfg, p.m)
	}
	return res, nil
}

// pushPendingStores pushes every configured store clone that holds
// commits the store has not confirmed. Best effort: offline stays pending,
// a failure is printed and retried by the next command.
func pushPendingStores(ctx context.Context, cfg *lorecfg.Config, m *stores.Manager) {
	seen := map[string]bool{}
	orgs := cfg.Orgs
	if ps := cfg.Personal.Store; ps != "" && ps != lorecfg.PersonalStoreLocal {
		// The personal store is pushed like an org store; its commits are
		// signed with the fallback identity (no org entry owns it).
		orgs = append(append([]lorecfg.Org{}, orgs...), lorecfg.Org{Name: personalStoreEntry, Stores: []lorecfg.Store{{URL: ps}}})
	}
	for i := range orgs {
		o := &orgs[i]
		for _, sc := range o.Stores {
			st, err := m.Lookup(ctx, sc.URL)
			if err != nil || seen[st.Dir] {
				continue
			}
			seen[st.Dir] = true
			pending, err := st.Unpushed(ctx)
			if err != nil || !hasBranchRef(pending) {
				continue
			}
			if _, _, err := st.PushPending(ctx, storeSignature(o)); err != nil {
				fmt.Fprintln(os.Stderr, "lore: push waiting store writes: "+redactIfQuiet(err.Error()))
			}
		}
	}
}

// personalStoreEntry names the personal store in pushPendingStores.
const personalStoreEntry = "me"

func hasBranchRef(refs []string) bool {
	for _, r := range refs {
		if strings.HasPrefix(r, "refs/heads/") {
			return true
		}
	}
	return false
}

// storeDefaultBranchFunc fills orgresolve.Inputs.StoreDefaultBranch from the
// bare clones (their remote HEAD, recorded at fetch); a store never cloned
// reports lorecfg.DefaultStoreBranch.
func storeDefaultBranchFunc(cfg *lorecfg.Config) func(string) string {
	m, err := newStoreManager(cfg, nil)
	if err != nil {
		return nil
	}
	return func(norm string) string {
		st, err := m.Lookup(context.Background(), norm)
		if err != nil {
			return lorecfg.DefaultStoreBranch
		}
		return st.DefaultBranch()
	}
}

// storeLinkWriter is the store-side half of `lore org link` (F11 steps
// 1–5): it picks the store already holding the folder (folder names are
// unique across an entry's stores, F13), checks the store, creates the
// folder project when missing, writes the link and pushes.
type storeLinkWriter struct {
	// Remove makes it the `lore org unlink` writer.
	Remove bool
}

// compile-time check.
var _ orgresolve.LinkWriter = storeLinkWriter{}

// WriteLink implements orgresolve.LinkWriter.
func (w storeLinkWriter) WriteLink(ctx context.Context, plan orgresolve.LinkPlan) error {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return err
	}
	org := cfg.FindOrg(plan.Entry)
	if org == nil {
		return errcodes.New(errcodes.InvalidInput, "no org entry named "+plan.Entry)
	}
	m, err := newStoreManager(cfg, org)
	if err != nil {
		return err
	}
	target, err := pickLinkStore(ctx, m, org, plan)
	if err != nil {
		return storeCLIError(err, "pick the store")
	}
	st, err := m.Open(ctx, target.URL)
	if err != nil {
		return storeCLIError(err, "open the store")
	}
	res, err := st.WriteLink(ctx, stores.LinkChange{
		Branch: target.EffectiveBranch(), Folder: plan.Folder, Remove: w.Remove,
		Link:       stores.LinkRecord{ProjectID: plan.ProjectID, Remote: plan.Remote, Own: plan.Own, Default: plan.Default},
		NewProject: newStoreProjectFiles,
		Signature:  storeSignature(org),
		NoPush:     !storeAutopush(cfg, org),
	})
	if err != nil {
		return storeCLIError(err, "write the link")
	}
	verb := "linked to"
	if w.Remove {
		verb = "unlinked from"
	}
	state := "pushed"
	switch {
	case res.Write.Offline:
		state = "committed in the local clone; offline, pushed by the next command"
	case !res.Write.Pushed && res.Write.Changed:
		state = "committed in the local clone (autopush off; `lore sync` pushes it)"
	case !res.Changed && !res.CreatedFolder:
		state = "already so, nothing changed"
	}
	created := ""
	if res.CreatedFolder {
		created = "created; "
	}
	fmt.Printf("✓ %s (%s, %s) %s %s %s/ (%s%s)\n  nothing was written to this repo\n", plan.Remote, plan.ProjectID, plan.Remote, verb, plan.StoreURL, plan.Folder, created, state)
	return nil
}

// pickLinkStore is F11 step 1 + F13: the store already holding the folder;
// else --store (plan.StoreURL when the user named it) or the first store.
// A folder that exists in another store than the one named is refused.
func pickLinkStore(ctx context.Context, m *stores.Manager, org *lorecfg.Org, plan orgresolve.LinkPlan) (*lorecfg.Store, error) {
	named := stores.StoreFor(org, plan.StoreURL)
	if named == nil {
		return nil, fmt.Errorf("stores: %s is not one of %s's stores", plan.StoreURL, org.Name)
	}
	var holder *lorecfg.Store
	for i := range org.Stores {
		sc := &org.Stores[i]
		st, err := m.Open(ctx, sc.URL)
		if err != nil {
			return nil, err
		}
		fr, err := st.Fetch(ctx, false)
		if err != nil {
			return nil, err
		}
		if fr.Offline && fr.At.IsZero() {
			return nil, fmt.Errorf("%w: %s (cannot tell which store holds %s/)", stores.ErrOffline, st.Norm, plan.Folder)
		}
		ok, err := st.FolderExists(ctx, sc.EffectiveBranch(), plan.Folder)
		if err != nil {
			return nil, err
		}
		if ok {
			holder = sc
			break
		}
	}
	if holder == nil || holder == named {
		return named, nil
	}
	// The folder lives in another store than the plan names. An explicit
	// --store is refused (folder names are unique across an entry's stores,
	// F13); without --store the link follows the folder.
	if plan.StoreExplicit {
		return nil, fmt.Errorf("%w: %s/ is in %s, not in %s", stores.ErrFolderElsewhere, plan.Folder, holder.URL, named.URL)
	}
	return holder, nil
}

// newStoreProjectFiles is the template of a fresh store project folder
// created by `lore org link` (see newStoreProjectFilesNamed).
func newStoreProjectFiles(folder string) (map[string][]byte, error) {
	return newStoreProjectFilesNamed(folder)
}

// loadTeamConfig loads ~/.lore/config.yaml with the CLI's error mapping.
func loadTeamConfig(ctx context.Context) (*lorecfg.Config, error) {
	home, err := loreHome()
	if err != nil {
		return nil, err
	}
	cfg, _, err := lorecfg.LoadTeam(ctx, home, lorecfg.Options{})
	if err != nil {
		return nil, errcodes.New(errcodes.ConfigInvalid, "~/.lore/config.yaml is not valid").
			WithCause(err).WithHint("run `lore config check` for every problem with its location")
	}
	return cfg, nil
}

// syncStores pushes waiting store writes of every configured store (the
// `lore sync` half of stores). Best effort and quiet when nothing waits.
func syncStores(ctx context.Context) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil || len(cfg.Orgs) == 0 {
		return
	}
	m, err := newStoreManager(cfg, nil)
	if err != nil {
		return
	}
	pushPendingStores(ctx, cfg, m)
}
