// org_context.go — the executor half of org resolution (LORE_TEAM_SPEC F10,
// F18): it gathers the inputs from the machine (~/.lore/config.yaml,
// LORE_ORG, the project id, the repo's remotes, the committed
// .lore/org.yaml) and hands them to the pure orgresolve.Resolve and
// identity.ResolveForOrg. Every command that needs "who am I, in which org"
// goes through here so the answer is the same everywhere.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/repourl"
	"saas/pkg/aicoder/stores"

	"golang.org/x/term"
)

// Env vars this file reads.
const (
	// envOrg chooses the org entry for this shell (F10 rule 1).
	envOrg = "LORE_ORG"
)

// orgSituation is everything gathered for one command, plus the decision.
type orgSituation struct {
	Dir        string // project dir (cwd)
	ConfigDir  string // ~/.lore
	Config     *lorecfg.Config
	ConfigPath string
	HasConfig  bool
	ProjectID  string
	Remotes    []string // normalised
	// RemoteNotes are remotes that could not be normalised (local paths).
	RemoteNotes []string
	Committed   *lorecfg.OrgFile
	// CommittedErr is a .lore/org.yaml that exists but is invalid; it is
	// ignored (never steers resolution) and reported.
	CommittedErr error
	Result       orgresolve.Result
	ResolveErr   error
}

// gatherOrgSituation reads the machine and runs F10. A broken config is an
// error (E_CONFIG_INVALID); every other problem lands in the situation so
// whoami can show it.
func gatherOrgSituation(ctx context.Context, dir string) (*orgSituation, error) {
	cfgDir, err := identity.Dir()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
	}
	cfg, loc, err := lorecfg.LoadTeam(ctx, cfgDir, lorecfg.Options{})
	if err != nil {
		return nil, errcodes.New(errcodes.ConfigInvalid, "~/.lore/config.yaml is not valid").
			WithCause(err).WithHint("run `lore config check` for every problem with its location")
	}
	s := &orgSituation{Dir: dir, ConfigDir: cfgDir, Config: cfg, ConfigPath: loc.Path, HasConfig: loc.Exists}

	if m, err := lsync.ReadProjectMeta(filepath.Join(dir, projresolve.MarkerDir, lsync.DataDirName)); err == nil {
		s.ProjectID = m.ProjectID
	}
	s.Remotes, s.RemoteNotes = gitRemotes(ctx, dir)

	committed, err := lorecfg.LoadOrgFile(ctx, filepath.Join(dir, projresolve.MarkerDir), lorecfg.Options{})
	if err != nil {
		s.CommittedErr = err
	} else {
		s.Committed = committed
	}

	// F10 rule 4: links come from the org stores (NoLinks without entries).
	// A code repo listed under stores: is refused here (E_NOT_A_STORE, E10).
	src, err := storeLinkSource(cfg)
	if err != nil {
		return nil, err
	}
	links, err := src.Links(ctx, s.ProjectID, s.Remotes)
	if err != nil {
		return nil, storeCLIError(err, "read store links")
	}
	s.Result, s.ResolveErr = orgresolve.Resolve(orgresolve.Inputs{
		Config:    cfg,
		EnvOrg:    strings.TrimSpace(os.Getenv(envOrg)),
		ProjectID: s.ProjectID,
		Remotes:   s.Remotes,
		Committed: s.Committed,
		Links:     links,
		// Q1 seam: staging = a store branch other than the bare clone's HEAD.
		StoreDefaultBranch: storeDefaultBranchFunc(cfg),
	})
	return s, nil
}

// orgResolveCLIError maps an orgresolve refusal to the CLI envelope.
func orgResolveCLIError(err error) error {
	var oe *orgresolve.Error
	if !errors.As(err, &oe) {
		return err
	}
	code := errcodes.InvalidInput
	switch oe.Kind {
	case orgresolve.KindAmbiguous:
		code = errcodes.OrgAmbiguous
	case orgresolve.KindDisagrees:
		code = errcodes.OrgDisagrees
	case orgresolve.KindLinkUnknown:
		code = errcodes.LinkUnknown
	case orgresolve.KindUnknownEntry:
		code = errcodes.InvalidInput
	}
	return errcodes.New(code, oe.Error()).WithHint(oe.Hint)
}

// resolveIdentityHere is THE identity for writes in this directory: the
// org identity when an org resolves (F18), the fallback chain otherwise.
// It fails when the org is ambiguous or the resolved entry has no identity
// — a write must never be stamped with a guessed person.
func resolveIdentityHere(ctx context.Context) (identity.Resolved, error) {
	dir, err := os.Getwd()
	if err != nil {
		return identity.Resolved{}, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	return resolveIdentityIn(ctx, dir)
}

// resolveIdentityIn is resolveIdentityHere for a project dir other than the
// working directory (`lore init <path>`).
func resolveIdentityIn(ctx context.Context, dir string) (identity.Resolved, error) {
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return identity.Resolved{}, err
	}
	return s.identity()
}

// identity resolves the actor for an already gathered situation.
func (s *orgSituation) identity() (identity.Resolved, error) {
	if s.ResolveErr != nil {
		return identity.Resolved{}, orgResolveCLIError(s.ResolveErr)
	}
	if s.Result.Org == nil {
		return identity.ResolveNoOrg(identity.NoOrgInputs{Config: s.Config}), nil
	}
	// F18: LORE_ACTOR agents are checked against the org roster in its store.
	m, err := newStoreManager(s.Config, s.Result.Org)
	if err != nil {
		return identity.Resolved{}, storeCLIError(err, "open the org stores")
	}
	r, err := identity.ResolveForOrg(identity.OrgInputs{Org: s.Result.Org, Getenv: os.Getenv,
		Roster: &stores.RosterCheck{Manager: m, Org: s.Result.Org}})
	if err != nil {
		code := errcodes.IdentityFailed
		return identity.Resolved{}, errcodes.New(code, err.Error()).
			WithHint(fmt.Sprintf("set it with `lore identity set --org %s --key human:<your email> --name \"Your Name\"`", s.Result.Org.Name))
	}
	return r, nil
}

// identityBestEffort is for callers that cannot fail (audit stamps, doctor,
// the agent check): the org identity when it resolves cleanly, otherwise
// the fallback chain. Writes go through resolveIdentityHere, which refuses
// instead, so a bad org setup is still reported before anything is stored.
func identityBestEffort(ctx context.Context) identity.Resolved {
	r, err := resolveIdentityHere(ctx)
	if err == nil {
		return r
	}
	// Still never borrow an org identity key when the org is refused
	// (ambiguous, disagreeing): ResolveNoOrg skips them (F10 rule 6). An
	// unreadable config gives nil, i.e. the legacy chain.
	var cfg *lorecfg.Config
	if dir, derr := identity.Dir(); derr == nil {
		if c, _, lerr := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{}); lerr == nil {
			cfg = c
		}
	}
	return identity.ResolveNoOrg(identity.NoOrgInputs{Config: cfg})
}

// gitRemotes lists dir's remotes, normalised and deduplicated, every URL of
// every remote (push URLs included). A non-repo dir has none. URLs that do
// not name a host (local paths) cannot identify a repo; they are returned
// as notes for whoami, never matched.
func gitRemotes(ctx context.Context, dir string) (norm []string, notes []string) {
	seen := map[string]bool{}
	for _, r := range rawRemoteURLs(ctx, dir, "") {
		n, err := repourl.Normalize(r.URL)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s (%s): not a hosted remote, ignored", r.Name, r.URL))
			continue
		}
		if !seen[n] {
			seen[n] = true
			norm = append(norm, n)
		}
	}
	sort.Strings(norm)
	return norm, dedupe(notes)
}

// gitOriginRemote is the remote `lore init` names a project after.
const gitOriginRemote = "origin"

// rawRemote is one configured URL of a git remote, exactly as written in
// git config (remote.<name>.url or remote.<name>.pushurl).
type rawRemote struct {
	Name string
	URL  string
}

// rawRemoteKeyPattern matches every remote's fetch and push URL keys. A
// remote name may contain dots, so the name is everything between the
// "remote." prefix and the last ".url"/".pushurl".
const rawRemoteKeyPattern = `^remote\..*\.(url|pushurl)$`

// rawRemoteURLs reads the remote URLs as written in git config, in config
// order, optionally for one remote name ("" = all). It deliberately does NOT
// use `git remote get-url`: that applies url.<base>.insteadOf, so a corporate
// mirror or an ssh↔https rewrite would replace the remote the org is keyed
// on with a mirror or local path (spec F10 edge cases, coordinator Q2).
// No git, no repo or no remotes all give nil.
func rawRemoteURLs(ctx context.Context, dir, only string) []rawRemote {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "config", "--get-regexp", rawRemoteKeyPattern).Output()
	if err != nil {
		return nil // exit 1 = no remote configured
	}
	var res []rawRemote
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, val, ok := strings.Cut(line, " ")
		val = strings.TrimSpace(val)
		if !ok || val == "" {
			continue
		}
		name := strings.TrimPrefix(key, "remote.")
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		if only != "" && name != only {
			continue
		}
		res = append(res, rawRemote{Name: name, URL: val})
	}
	return res
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// stdinIsTTY reports whether a person can answer a prompt. A real isatty
// check: /dev/null is a character device too, and agents and CI run with
// stdin on /dev/null, so a mode-bit check would prompt them.
func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
