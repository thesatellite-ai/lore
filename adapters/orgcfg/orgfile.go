package orgcfg

// orgfile.go — the store side of `lore org create` and `lore org import`
// for cmd/lore (P3-ORG-G-2): write and read <org folder>/
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

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
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
	URL string `yaml:"url"`
	// Name is the store's short name (orgconf.Store.ShortName); empty
	// means the repo name of URL.
	Name      string `yaml:"name,omitempty"`
	Branch    string `yaml:"branch,omitempty"`
	OrgFolder string `yaml:"org_folder,omitempty"`
}

// orgFilePath is lore-org.yaml's path for an org folder: <folder>/lore-org.yaml,
// lore-org.yaml at the store's top for the root (D-e).
func orgFilePath(folder string) string {
	if folder == "" || folder == orgconf.OrgFolderRoot {
		return orgDefaultsFile
	}
	return folder + "/" + orgDefaultsFile
}

// oldOrgFilePath is where stores kept lore-org.yaml before the plain-files
// round (<folder>/.lore/lore-org.yaml): read until the store is moved.
func oldOrgFilePath(folder string) string {
	p := storeLoreDir + "/" + orgDefaultsFile
	if folder == "" || folder == orgconf.OrgFolderRoot {
		return p
	}
	return folder + "/" + p
}

// orgFilePaths are the places a reader looks for an org folder's
// lore-org.yaml, the current one first.
func orgFilePaths(folder string) []string {
	return []string{orgFilePath(folder), oldOrgFilePath(folder)}
}

// OrgFilePath is where lore writes an org folder's lore-org.yaml, slash
// separated from the store's top: <folder>/lore-org.yaml, or lore-org.yaml
// when the org folder is the store root (D-e).
func OrgFilePath(folder string) string { return orgFilePath(folder) }

// OrgFilePaths are the places to read an org folder's lore-org.yaml from,
// the current one first, then the old <folder>/.lore/lore-org.yaml of a
// store not moved yet.
func OrgFilePaths(folder string) []string { return orgFilePaths(folder) }

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

// openClone makes sure the store's clone exists and fetches it; refuseOffline turns an unreachable remote into ErrStoreOffline even
// when a copy exists (import must read the team's current suggestion).
func (g *GitLinks) openClone(ctx context.Context, url string, refuseOffline bool) (norm, dir string, err error) {
	norm, err = repourl.NormalizeStore(url)
	if err != nil {
		return "", "", fmt.Errorf("orgcfg: store url %q: %w", url, err)
	}
	dir, own := cloneDir(g.Home, norm)
	if err = ensureClone(ctx, dir, url, own); err != nil {
		return "", "", err
	}
	if ferr := fetchLocked(ctx, g.Route, dir, g.now()); ferr != nil && (refuseOffline || lastFetch(dir).IsZero()) {
		return "", "", fmt.Errorf("%w: %s", ErrStoreOffline, norm)
	}
	return norm, dir, nil
}

// WriteOrgFile commits w.Doc as <org folder>/lore-org.yaml on the
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
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: res.Branch, NoPush: res.NoPush, Route: g.Route,
		Message: fmt.Sprintf(orgFileMessage, w.Doc.Name), Signature: w.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			old, found, rerr := repo.ReadFile(ctx, tip, path)
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				put := map[string][]byte{path: body}
				if mk, ok, merr := markerNamingOrgFolder(ctx, repo, tip, w.OrgFolder); merr != nil {
					return nil, merr
				} else if ok {
					put[storeMarkerFile] = mk
				}
				return put, nil
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

// markerNamingOrgFolder is the store marker at tip with org_folder set to
// folder, when the marker exists and names another folder or none (D-e:
// only the store that holds the org folder names it, and it does from the
// commit that writes the folder's lore-org.yaml). ok is false when the
// marker already says so or there is none to change.
func markerNamingOrgFolder(ctx context.Context, repo *gitref.Repo, tip, folder string) ([]byte, bool, error) {
	if folder == "" {
		folder = orgconf.OrgFolderRoot
	}
	raw, found, err := repo.ReadFile(ctx, tip, storeMarkerFile)
	if err != nil || !found {
		return nil, false, err
	}
	m, err := rowfs.ParseMarker(raw)
	if err != nil {
		return nil, false, nil // a marker lore cannot read is left as it is (the store's opens name it)
	}
	if m.OrgFolder == folder {
		return nil, false, nil
	}
	m.OrgFolder = folder
	b, err := m.Encode()
	return b, err == nil, err
}

// Suggestion is a store's suggested org entry.
type Suggestion struct {
	// Doc is lore-org.yaml; Folder the org folder it was read from.
	Doc    OrgFileDoc
	Folder string
	// StoreURL is the normalised store, Branch the branch read.
	StoreURL, Branch string
	// Version is the git blob id of the lore-org.yaml read (the stamp of
	// a config copy, orgsource.go); StoreID the store marker's sto_ id.
	Version, StoreID string
	// Path is where the file was read (orgFilePath, or the old place in a
	// store not moved yet).
	Path string
}

// parseOrgFileDoc decodes a store's lore-org.yaml: a value of the wrong
// kind names its line and key path (yamlerr), never a Go type.
func parseOrgFileDoc(raw string) (OrgFileDoc, error) {
	var doc OrgFileDoc
	if err := yamlerr.Decode([]byte(raw), &doc, yamlerr.Lenient); err != nil {
		return OrgFileDoc{}, err
	}
	return doc, nil
}

// checkShortNames refuses a lore-org.yaml whose stores share a short name
// (or set a bad one): every problem, the urls of both stores and the key
// to set (orgconf.CheckShortNames).
func (d OrgFileDoc) checkShortNames() error {
	in := make([]orgconf.ShortNameStore, 0, len(d.Stores))
	for _, st := range d.Stores {
		s := orgconf.Store{URL: st.URL, Name: st.Name}
		in = append(in, orgconf.ShortNameStore{URL: st.URL, Name: s.ShortName(), Set: st.Name != ""})
	}
	return errors.Join(orgconf.CheckShortNames(in, orgconf.OrgFileName)...)
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
	return g.suggestionAt(ctx, dir, norm, branch) // orgsource.go
}
