package orgcfg

// orgfile.go — the store side of `lore org create` and `lore org import`
// for cmd/lore (P3-ORG-G-2): write and read <org folder>/.lore/
// lore-org.yaml, the team's suggested org entry (CLI_REFERENCE 2.26,
// CONFIG_GUIDE 6.4: the org name, its stores, the repo patterns and the
// client-repo defaults; never an identity), in the shared v0.1.17 bare
// clone the link index reads. Ported from v0.1.17's stores.WriteOrgFile /
// ReadOrgFile and the CLI's readSuggestion.
//
//	WriteOrgFile ─▶ clone + fetch ─▶ default branch ─▶ commitStore: marker
//	                check ─▶ same org's file kept / another org's refused
//	                ─▶ commit as the caller's org identity ─▶ push
//	ReadSuggestion ─▶ clone + fetch (offline refuses) ─▶ branch tip ─▶
//	                  store tree check ─▶ marker's org_folder, "org", "."
//	                  ─▶ first lore-org.yaml found

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// orgFileMessage is the commit subject of WriteOrgFile (v0.1.17
// stores msgOrgFile).
const orgFileMessage = "lore: org %s"

// fallbackOrgFolder is the org folder read when the marker names none
// (v0.1.17 defaultMarkerOrgFolder).
const fallbackOrgFolder = "org"

var (
	// ErrOrgFileExists is returned when the org folder already holds a lore-org.yaml naming
	// another org (WriteOrgFile never overwrites a team's suggestion).
	ErrOrgFileExists = errors.New("orgcfg: the org folder already has a lore-org.yaml")
	// ErrNoOrgFile is returned when the store suggests no org entry.
	ErrNoOrgFile = errors.New("orgcfg: this store has no lore-org.yaml suggesting an org entry")
)

// OrgFileDoc is lore-org.yaml (v0.1.17 stores.OrgFileDefaults, same keys
// and order so both binaries write one byte form).
type OrgFileDoc struct {
	// Name is the suggested entry name.
	Name string `yaml:"name,omitempty"`
	// Stores are the org's stores.
	Stores []OrgFileStore `yaml:"stores,omitempty"`
	// Own and Repos are the repo patterns.
	Own      []string `yaml:"own,omitempty"`
	Repos    []string `yaml:"repos,omitempty"`
	Defaults struct {
		// ClientRepos is defaults.client_repos: kind → repo | <org> |
		// <org>/<folder>.
		ClientRepos map[string]string `yaml:"client_repos,omitempty"`
	} `yaml:"defaults,omitempty"`
}

// OrgFileStore is one store in lore-org.yaml.
type OrgFileStore struct {
	URL       string `yaml:"url"`
	Branch    string `yaml:"branch,omitempty"`
	OrgFolder string `yaml:"org_folder,omitempty"`
}

// orgFilePath is lore-org.yaml's path for an org folder ("." = the root).
func orgFilePath(folder string) string {
	p := storeLoreDir + "/" + orgDefaultsFile
	if folder == "" || folder == orgconf.OrgFolderRoot {
		return p
	}
	return folder + "/" + p
}

// OrgFileWrite is one lore-org.yaml to write.
type OrgFileWrite struct {
	// URL is the store url as the user gave it.
	URL string
	// OrgFolder is the folder that gets the file.
	OrgFolder string
	// Doc is the file's content.
	Doc OrgFileDoc
	// Signature is the commit author (the creator's org identity).
	Signature gitref.Signature
}

// OrgFileResult says what WriteOrgFile did.
type OrgFileResult struct {
	// StoreURL is the normalised store, Branch the branch written.
	StoreURL, Branch string
	// Existed: the same org's file was already there (nothing written).
	Existed bool
	// Pushed / Offline / NoPush as for a link.
	Pushed, Offline, NoPush bool
}

// openClone makes sure the store's shared bare clone exists and fetches
// it; refuseOffline turns an unreachable remote into ErrStoreOffline even
// when a copy exists (import must read the team's current suggestion).
func (g *GitLinks) openClone(ctx context.Context, url string, refuseOffline bool) (norm, dir string, err error) {
	norm, err = repourl.Normalize(url)
	if err != nil {
		return "", "", fmt.Errorf("orgcfg: store url %q: %w", url, err)
	}
	dir = g.cloneDirOf(norm)
	if err = ensureBare(ctx, dir, url); err != nil {
		return "", "", err
	}
	unlock, err := lockClone(ctx, dir)
	if err != nil {
		return "", "", err
	}
	ferr := fetch(ctx, dir)
	unlock()
	if ferr != nil {
		if refuseOffline || lastFetch(dir).IsZero() {
			return "", "", fmt.Errorf("%w: %s", ErrStoreOffline, norm)
		}
		return norm, dir, nil
	}
	recordFetch(ctx, dir, g.now())
	return norm, dir, nil
}

// WriteOrgFile commits w.Doc as <org folder>/.lore/lore-org.yaml on the
// store's default branch and pushes it (autopush rules as for a link). A
// file naming the same org is kept (Existed); one naming another org is
// ErrOrgFileExists.
func (g *GitLinks) WriteOrgFile(ctx context.Context, w OrgFileWrite) (OrgFileResult, error) {
	norm, dir, err := g.openClone(ctx, w.URL, false)
	if err != nil {
		return OrgFileResult{}, err
	}
	body, err := yaml.Marshal(w.Doc)
	if err != nil {
		return OrgFileResult{}, fmt.Errorf("orgcfg: encode %s: %w", orgDefaultsFile, err)
	}
	res := OrgFileResult{StoreURL: norm, Branch: g.DefaultBranch(ctx, norm), NoPush: !g.autopush(nil)}
	path := orgFilePath(w.OrgFolder)
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: res.Branch, NoPush: res.NoPush,
		Message: fmt.Sprintf(orgFileMessage, w.Doc.Name), Signature: w.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			old, found, rerr := repo.ReadFile(ctx, tip, path)
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				return map[string][]byte{path: body}, nil
			}
			var cur OrgFileDoc
			if perr := yaml.Unmarshal(old, &cur); perr != nil || cur.Name != w.Doc.Name {
				return nil, fmt.Errorf("%w: %s (org %q)", ErrOrgFileExists, path, cur.Name)
			}
			res.Existed = true
			return nil, nil
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	return res, err
}

// Suggestion is a store's suggested org entry.
type Suggestion struct {
	// Doc is lore-org.yaml; Folder the org folder it was read from.
	Doc    OrgFileDoc
	Folder string
	// StoreURL is the normalised store, Branch the branch read.
	StoreURL, Branch string
}

// ReadSuggestion reads the org entry a store suggests: its lore-org.yaml
// in the org folder its marker names, else "org", else the root. branch ""
// is the store's default branch. An unreachable store is ErrStoreOffline;
// a tree that is not a lore store is E_NOT_A_STORE; no file is
// ErrNoOrgFile.
func (g *GitLinks) ReadSuggestion(ctx context.Context, url, branch string) (Suggestion, error) {
	norm, dir, err := g.openClone(ctx, url, true)
	if err != nil {
		return Suggestion{}, err
	}
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
		return Suggestion{}, fmt.Errorf("%w: %s has no branch %s", ErrNoStoreBranch, norm, branch)
	}
	if err := checkStoreTree(ctx, dir, tip, norm, branch); err != nil {
		return Suggestion{}, err
	}
	candidates := []string{fallbackOrgFolder, orgconf.OrgFolderRoot}
	if raw, merr := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+storeMarkerFile); merr == nil {
		var mk struct {
			OrgFolder string `yaml:"org_folder"`
		}
		if yaml.Unmarshal([]byte(raw), &mk) == nil && mk.OrgFolder != "" {
			candidates = append([]string{mk.OrgFolder}, candidates...)
		}
	}
	for _, folder := range candidates {
		raw, rerr := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+orgFilePath(folder))
		if rerr != nil {
			continue // no lore-org.yaml in this folder
		}
		var doc OrgFileDoc
		if perr := yaml.Unmarshal([]byte(raw), &doc); perr != nil {
			return Suggestion{}, fmt.Errorf("orgcfg: %s in %s: %w", orgFilePath(folder), norm, perr)
		}
		return Suggestion{Doc: doc, Folder: folder, StoreURL: norm, Branch: branch}, nil
	}
	return Suggestion{}, fmt.Errorf("%w: %s@%s", ErrNoOrgFile, norm, branch)
}
