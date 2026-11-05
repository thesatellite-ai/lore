package wire

// standalone.go — the wire.Options of the binary built only from this
// module (cmd/lore, STORE_SERVER_SPEC 10.4 / 16 phase 10).
//
// Flow:
//
//	cmd/lore main ──▶ StandaloneOptions(repo, home, workdir, version)
//	                     └─ NewOrgResolution(home, workdir, GitLinks).Apply
//	                         Org, RepoIsClient, OrgStores, OrgEntryStore,
//	                         OrgSigners, DefaultTarget, GuardRepoWrite from
//	                         the core's org resolution (CONFIG_GUIDE F10/F14,
//	                         FA-OLDCODE-1), the same code the old binary uses
//
// The F16 write-time leak check (OrgResolution.RepoWriteCheck) stays nil
// here until the leak check moves into the core: the commit hooks still
// check (P3-ORG-3).

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// envOn is the value that turns a boolean LORE_* variable on.
const envOn = "1"

// StandaloneOptions returns the Options of the standalone binary: the
// repo, home, working folder and version, LORE_ALLOW_NETWORK_FS, and the
// org hooks of StandaloneOrgResolution, with stdin being a terminal as
// "a person can answer" (F14). Every other hook keeps its documented
// default (append-only audit file, no live-ref overlay).
func StandaloneOptions(repoDir, home, workDir, version string) Options {
	o, _ := StandaloneOptionsOrg(repoDir, home, workDir, version)
	return o
}

// StandaloneOptionsOrg is StandaloneOptions that also returns the org
// resolution whose hooks it applied, so the binary can print the
// "  filed: <rule>" line of a routed create from res.Routed() (v0.1.17
// printRoute; the old binary reads the same method).
func StandaloneOptionsOrg(repoDir, home, workDir, version string) (Options, *OrgResolution) {
	o := Options{
		RepoDir:        repoDir,
		Home:           home,
		WorkDir:        workDir,
		Version:        version,
		AllowNetworkFS: os.Getenv(rowfs.AllowNetworkFSEnv) == envOn,
	}
	dir := workDir
	if dir == "" {
		dir = repoDir
	}
	res := StandaloneOrgResolution(home, dir)
	res.Interactive = stdinTTY
	res.Apply(&o)
	// cmd/lore's org and whoami hosts (P3-CMDLORE-4): built on the same
	// resolution the write hooks use, so whoami shows what a write does.
	o.OrgCommands = &standaloneOrg{home: home, res: res}
	if o.Local.Clones == nil {
		o.Local.Clones = &standaloneClones{home: home}
	}
	if o.Local.Leaks == nil {
		o.Local.Leaks = &standaloneLeaks{opts: o, res: res} // its own App: check is Local (P3-CMDLORE-8)
	}
	// P3-LOCAL-7: the repo's hash-chained audit log in the machine store,
	// the same chain the old binary appends (lore audit log|verify work).
	// Built after the leaks host, whose own New must not rebind this sink:
	// New binds it to the App, and whoami and agent start read and write
	// session rows through that App (AGENT_IDENTITY 6.3-6.4).
	if o.Audit == nil {
		o.Audit = NewChainAudit(home, dir, time.Now)
	}
	bound, _ := o.Audit.(boundAppSource)
	o.Whoami = &standaloneWhoami{opts: o, res: res, app: bound}
	o.Sessions.Agent = &standaloneAgent{opts: o, app: bound}
	// Org store writes are pushed at once (P3-CMDLORE-7); host hooks wrap
	// after this (their WrapBackend calls this one first).
	if o.WrapBackend == nil {
		o.WrapBackend = storePushWrap(res)
	}
	return o, res
}

// StandaloneOrgResolution is the org resolution of the project at dir with
// the store links read from the shared v0.1.17 bare clones under home
// (orgcfg.GitLinks). A config.yaml that cannot be read gives the link
// source no entries; the resolution then reports the same config error
// from Gather on the first hook, so nothing is written under a guessed org
// (fail closed, FA-OLDCODE-1).
func StandaloneOrgResolution(home, dir string) *OrgResolution {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil {
		cfg = nil
	}
	return NewOrgResolution(home, dir, &orgcfg.GitLinks{Home: home, Config: cfg})
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
