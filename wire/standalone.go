package wire

// standalone.go — the wire.Options of the binary built only from this
// module (cmd/lore, STORE_SERVER_SPEC 10.4 / 16 phase 10).
//
// Flow:
//
//	cmd/lore main ──▶ StandaloneOptions(repo, home, workdir, version)
//	                     ├─ RepoIsClient: no org configured → internal repo;
//	                     │   an org configured → client (fail closed, FA-OLDCODE-1)
//	                     └─ Org: no org → nil; an org configured → E_UNSUPPORTED
//	                         (never write under a guessed identity, FA-OLDCODE-1)
//
// The old binary (saas/cmd/cli) supplies these hooks from v0.1.17's org
// resolution (links in the org stores, repo_orgs, own:/repos: patterns);
// until that resolution moves into the core, the standalone binary only
// serves repos of people with no org configured, and refuses the rest
// instead of guessing.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// EnvOrg is v0.1.17's org override variable; set, it means an org is
// chosen, which the standalone binary cannot resolve yet.
const EnvOrg = "LORE_ORG"

// envOn is the value that turns a boolean LORE_* variable on.
const envOn = "1"

// orgUnsupportedMsg is the refusal of the standalone binary in a setup with
// an org (FA-OLDCODE-1).
const orgUnsupportedMsg = "this lore binary does not resolve orgs yet (an org is configured in ~/.lore/config.yaml or LORE_ORG is set)"

// orgUnsupportedHint names the binary that still does.
const orgUnsupportedHint = "use the released lore binary (built from saas/cmd/cli) until org resolution moves into the core"

// StandaloneOptions returns the Options of the standalone binary: the
// repo, home, working folder and version, LORE_ALLOW_NETWORK_FS, and the
// org hooks of FA-OLDCODE-1. Every other hook keeps its documented
// default (append-only audit file, no live-ref overlay, no org stores).
func StandaloneOptions(repoDir, home, workDir, version string) Options {
	return Options{
		RepoDir:        repoDir,
		Home:           home,
		WorkDir:        workDir,
		Version:        version,
		AllowNetworkFS: os.Getenv(rowfs.AllowNetworkFSEnv) == envOn,
		RepoIsClient:   func(context.Context) (bool, error) { return standaloneRepoIsClient(home) },
		Org:            func(context.Context) (*identity.Org, error) { return standaloneOrg(home) },
	}
}

// orgConfigured reports whether this machine names an org: LORE_ORG set,
// or at least one orgs[] entry in config.yaml.
func orgConfigured(home string) (bool, error) {
	if strings.TrimSpace(os.Getenv(EnvOrg)) != "" {
		return true, nil
	}
	c, err := readMachineConfig(home)
	if err != nil {
		return false, err
	}
	return len(c.Orgs) > 0, nil
}

// standaloneRepoIsClient: with no org anywhere the repo is the person's own
// (internal), as v0.1.17 decided for "no org at all"; with an org the link
// logic is needed, so it answers client with the reason (fail closed: the
// client write path strips internal fields and projects agent ids).
func standaloneRepoIsClient(home string) (bool, error) {
	has, err := orgConfigured(home)
	if err != nil {
		return true, err
	}
	if has {
		return true, errs.New(errs.Unsupported, orgUnsupportedMsg).WithHint(orgUnsupportedHint)
	}
	return false, nil
}

// standaloneOrg: no org → nil (the identity chain uses the top identity, git
// email, …); an org configured → E_UNSUPPORTED, because the actor id
// depends on which org's identity applies and a guess would stamp rows
// with the wrong person.
func standaloneOrg(home string) (*identity.Org, error) {
	has, err := orgConfigured(home)
	if err != nil {
		return nil, err
	}
	if has {
		return nil, errs.New(errs.Unsupported, orgUnsupportedMsg).WithHint(orgUnsupportedHint)
	}
	return nil, nil
}

// repoMetaMode is the mode of a new _meta.json (P2-LINT-1: files lore
// creates are owner-only).
const repoMetaMode = 0o600

// repoMetaSeed is the _meta.json `lore init` creates: the keys v0.1.17's
// lsync writes (project_id, _v), so both binaries read the same checkout.
type repoMetaSeed struct {
	ProjectID string `json:"project_id"`
	Version   int    `json:"_v"`
}

// SeedRepoMeta creates the checkout's .lore/data/_meta.json naming project
// pid, the marker every later step (repo id, PROV1; the format gate) reads.
// An existing file is never touched (a teammate's project wins: E1), so a
// repeated init is a no-op here.
func (a *App) SeedRepoMeta(ctx context.Context, pid ids.ID[ids.Project]) error {
	top, err := a.repoTop(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(repoMetaSeed{ProjectID: pid.String(), Version: row.FormatVersion})
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+rowfs.MetaFileName, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+rowfs.MetaFileName, err)
	}
	path := filepath.Join(top, filepath.FromSlash(rowfs.RepoMetaPath))
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, repoMetaMode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: create "+rowfs.RepoMetaPath, err)
	}
	_, werr := f.Write(doc)
	return errors.Join(werr, f.Close())
}
