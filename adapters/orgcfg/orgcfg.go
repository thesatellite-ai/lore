// Package orgcfg gathers org resolution's inputs from the machine and runs
// it (LORE_TEAM_SPEC F10, F18; layer L3): ~/.lore/config.yaml, LORE_ORG,
// the project id in .lore/data/_meta.json, the repo's git remotes, the
// committed .lore/org.yaml and the store links (a LinkSource).
//
// Why here: v0.1.17 did this in the old binary (saas/cmd/cli
// org_context.go gatherOrgSituation), so the standalone binary refused
// every setup with an org (FA-OLDCODE-1). Both binaries now ask this
// package, so "which org, which role" has one answer.
//
//	Gather(ctx, In) ──▶ LoadConfig(home) ── E_CONFIG_INVALID on a bad file
//	               ├──▶ ProjectID(dir), Remotes(ctx, dir), LoadOrgFile(dir)
//	               ├──▶ In.Links.Links(projectID, remotes)  (store links, F11)
//	               ├──▶ orgresolve.Resolve(...)              (pure F10)
//	               └──▶ staging entry chosen on purpose: In.Links.EntryLinks
//
// Every problem but a broken config lands in the Situation, so a caller
// can show it (whoami) or fail closed (RepoIsClient).
package orgcfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// EnvOrg chooses the org entry for this shell (F10 rule 1).
const EnvOrg = "LORE_ORG"

// Folder and file names read here (v0.1.17 layout).
const (
	// loreDir is the repo's lore folder.
	loreDir = ".lore"
	// dataDir holds the project's row files and _meta.json.
	dataDir = "data"
	// metaFile is the project marker naming the project id.
	metaFile = "_meta.json"
	// gitBin is the git program; remotes are read from git config.
	gitBin = "git"
	// rawRemoteKeyPattern matches every remote's fetch and push URL keys
	// (a remote name may contain dots).
	rawRemoteKeyPattern = `^remote\..*\.(url|pushurl)$`
)

// LinkSource answers the store links of a repo (F11). It is
// orgresolve.LinkSource plus the per-entry answer a staging entry chosen
// on purpose needs (links exist per store branch, TEAM-011).
type LinkSource interface {
	orgresolve.LinkSource
	// EntryLinks returns the links of one entry, staging included; ok is
	// false when one of its stores could not be read (fail closed).
	EntryLinks(ctx context.Context, o *orgconf.Org, projectID string, remotes []string) (links []orgresolve.Link, ok bool, err error)
	// DefaultBranch returns a store's default branch by normalised url
	// (orgconf.DefaultStoreBranch when unknown): an entry on another
	// branch is a staging entry (TEAM-022).
	DefaultBranch(ctx context.Context, storeNorm string) string
}

// In is what Gather reads.
type In struct {
	// Home is the folder holding config.yaml (~/.lore).
	Home string
	// Dir is the project folder the command runs in.
	Dir string
	// EnvOrg is LORE_ORG ("" = unset); the caller reads the environment.
	EnvOrg string
	// Links reads the store links; nil reads none (no org stores yet).
	Links LinkSource
}

// Situation is everything gathered for one command, plus the decision.
type Situation struct {
	Dir        string
	Home       string
	Config     *orgconf.Config
	ConfigPath string
	HasConfig  bool
	ProjectID  string
	// Remotes are the repo's remotes, normalised, sorted, once each.
	Remotes []string
	// RemoteNotes are remotes that could not be normalised (local paths).
	RemoteNotes []string
	Committed   *orgconf.OrgFile
	// CommittedErr is a .lore/org.yaml that exists but is invalid; it is
	// ignored (never steers resolution) and reported.
	CommittedErr error
	Result       orgresolve.Result
	ResolveErr   error
}

// ConfigPath is ~/.lore/config.yaml under home.
func ConfigPath(home string) string {
	return filepath.Join(home, orgconf.TeamFileBase+orgconf.ExtYAML)
}

// LoadConfig reads the org part of home's config.yaml. An absent file is
// an empty, valid config (exists false); a bad one is E_CONFIG_INVALID
// naming every problem.
func LoadConfig(home string) (cfg *orgconf.Config, exists bool, err error) {
	b, err := os.ReadFile(filepath.Clean(ConfigPath(home)))
	if errors.Is(err, fs.ErrNotExist) {
		return &orgconf.Config{}, false, nil
	}
	if err != nil {
		return nil, false, errs.Wrap(errs.Internal, "orgcfg: read ~/.lore/"+orgconf.TeamFileBase+orgconf.ExtYAML, err)
	}
	c, perr := orgconf.Parse(b)
	if perr != nil {
		return nil, true, errs.Wrap(errs.ConfigInvalid, "~/.lore/config.yaml is not valid: "+perr.Error(), perr).
			WithHint("run `lore config check` for every problem with its location")
	}
	return c, true, nil
}

// LoadOrgFile reads dir's committed .lore/org.yaml; nil when absent.
func LoadOrgFile(dir string) (*orgconf.OrgFile, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, loreDir, orgconf.OrgFileBase+orgconf.ExtYAML)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orgcfg: read .lore/org.yaml: %w", err)
	}
	return orgconf.ParseOrgFile(b)
}

// ProjectID is the project id in dir's .lore/data/_meta.json; "" when the
// file is missing or names none (before `lore init`).
func ProjectID(dir string) string {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, loreDir, dataDir, metaFile)))
	if err != nil {
		return ""
	}
	var m struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.ProjectID
}

// Remotes reads dir's git remote urls as written in git config (never
// `git remote get-url`: insteadOf would swap the remote the org is keyed
// on for a mirror, F10 edge cases) and normalises them; a url that is not
// a hosted remote is a note. No git, no repo or no remote gives nil.
func Remotes(ctx context.Context, dir string) (norm, notes []string) {
	out, err := gitRun(ctx, "-C", dir, "config", "--get-regexp", rawRemoteKeyPattern)
	if err != nil {
		return nil, nil // exit 1 = no remote configured
	}
	seen := map[string]bool{}
	seenNote := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		key, val, ok := strings.Cut(line, " ")
		val = strings.TrimSpace(val)
		if !ok || val == "" {
			continue
		}
		name := strings.TrimPrefix(key, "remote.")
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		n, err := repourl.Normalize(val)
		if err != nil {
			note := fmt.Sprintf("%s (%s): not a hosted remote, ignored", name, val)
			if !seenNote[note] {
				seenNote[note] = true
				notes = append(notes, note)
			}
			continue
		}
		if !seen[n] {
			seen[n] = true
			norm = append(norm, n)
		}
	}
	sort.Strings(norm)
	return norm, notes
}

// Gather reads the machine and runs F10. A broken config is an error
// (E_CONFIG_INVALID); every other problem lands in the Situation.
func Gather(ctx context.Context, in In) (*Situation, error) {
	cfg, exists, err := LoadConfig(in.Home)
	if err != nil {
		return nil, err
	}
	s := &Situation{Dir: in.Dir, Home: in.Home, Config: cfg, ConfigPath: ConfigPath(in.Home), HasConfig: exists}
	s.ProjectID = ProjectID(in.Dir)
	s.Remotes, s.RemoteNotes = Remotes(ctx, in.Dir)
	if committed, cerr := LoadOrgFile(in.Dir); cerr != nil {
		s.CommittedErr = cerr
	} else {
		s.Committed = committed
	}
	var links orgresolve.LinkSource = orgresolve.NoLinks{}
	var defBranch func(string) string
	if in.Links != nil && len(cfg.Orgs) > 0 {
		links = in.Links
		defBranch = func(norm string) string { return in.Links.DefaultBranch(ctx, norm) }
	}
	lr, err := links.Links(ctx, s.ProjectID, s.Remotes)
	if err != nil {
		return nil, err
	}
	s.Result, s.ResolveErr = orgresolve.Resolve(orgresolve.Inputs{
		Config:             cfg,
		EnvOrg:             strings.TrimSpace(in.EnvOrg),
		ProjectID:          s.ProjectID,
		Remotes:            s.Remotes,
		Committed:          s.Committed,
		Links:              lr,
		StoreDefaultBranch: defBranch,
	})
	if s.ResolveErr == nil && s.Result.Org != nil && s.ProjectID != "" && in.Links != nil && orgresolve.IsStaging(s.Result.Org, defBranch) {
		if err := stagingLinks(ctx, s, in.Links); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// stagingLinks replaces the link answer of a staging entry chosen on
// purpose with that entry's own links (links exist per store branch,
// TEAM-011, side effect 19). A store branch that cannot be read makes the
// link status unknown (F10 fail closed).
func stagingLinks(ctx context.Context, s *Situation, src LinkSource) error {
	org := s.Result.Org
	l, ok, err := src.EntryLinks(ctx, org, s.ProjectID, s.Remotes)
	if err != nil {
		return err
	}
	if !ok {
		s.Result.Links = nil
		s.Result.LinkStatus = orgresolve.LinkStatusUnknown
		s.Result.LinkUnknown = true
		s.Result.LinkDetail = "cannot read the store branch of " + org.Name
		return nil
	}
	s.Result.Links = l
	detail := fmt.Sprintf("%d link(s) for this repo on %s's store branch %s (links are per store branch)",
		len(l), org.Name, strings.Join(entryBranches(org), ", "))
	if s.Result.LinkStatus == orgresolve.LinkStatusUnknown {
		s.Result.LinkDetail = detail + "; " + s.Result.LinkDetail
		return nil
	}
	s.Result.LinkStatus = orgresolve.LinkStatusKnown
	s.Result.LinkDetail = detail
	return nil
}

// entryBranches lists the store branches an entry uses, once each.
func entryBranches(o *orgconf.Org) []string {
	seen := map[string]bool{}
	var out []string
	for _, sc := range o.Stores {
		if b := sc.EffectiveBranch(); !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}

// IsClient is the repo's role for the write path: client when F10 says
// client, when a staging entry's own links are not all own, or on any
// refusal or unreadable link (fail closed). No org, or the org's own
// repo, is internal.
func (s *Situation) IsClient() (bool, error) {
	if s.ResolveErr != nil {
		return true, s.ResolveErr
	}
	r := s.Result
	if r.LinkUnknown {
		return true, errs.New(errs.LinkUnknown, "the store links of this repo cannot be read: "+r.LinkDetail)
	}
	if r.Role == orgresolve.RoleClient {
		return true, nil
	}
	for _, l := range r.Links {
		if !l.Own {
			return true, nil
		}
	}
	return false, nil
}

// ResolveError maps an orgresolve refusal to the core's error codes, as
// v0.1.17's orgResolveCLIError.
func ResolveError(err error) error {
	var oe *orgresolve.Error
	if !errors.As(err, &oe) {
		return err
	}
	code := errs.InvalidInput
	switch oe.Kind {
	case orgresolve.KindAmbiguous:
		code = errs.OrgAmbiguous
	case orgresolve.KindDisagrees:
		code = errs.OrgDisagrees
	case orgresolve.KindLinkUnknown:
		code = errs.LinkUnknown
	case orgresolve.KindUnknownEntry:
		code = errs.InvalidInput
	}
	e := errs.New(code, oe.Error())
	if oe.Hint != "" {
		e = e.WithHint(oe.Hint)
	}
	return e
}
