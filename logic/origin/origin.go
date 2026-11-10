// Package origin builds a row's provenance blocks (PROVENANCE_DESIGN 5, 6.2,
// 8): the write-once origin `_o` and the `_w` stamp, and the reduced form a
// client repo may hold. Pure: it gets every fact from the caller (the
// engine, fed by wire) and reads nothing itself, so it is tested without a
// repository.
package origin

import (
	"cmp"
	"path"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Keys of the `_o` object (PROVENANCE_DESIGN 5.1). Absent facts are left
// out, never written as empty strings.
const (
	KeyCmd        = "cmd"
	KeyStore      = "store"
	KeyFolder     = "folder"
	KeyRepo       = "repo"
	KeyProject    = "project"
	KeyDir        = "dir"
	KeyGit        = "git"
	KeyImport     = "import"
	KeyCopiedFrom = "copied_from"
	KeyMoves      = "moves"

	gitBranch = "branch"
	gitCommit = "commit"
	gitState  = "state"

	importSource = "source"
	importSrc    = "src"
	importExtID  = "ext_id"
)

// GitState is `_o.git.state`: where HEAD pointed when the row was born.
type GitState string

// The four git states (PROVENANCE_DESIGN 5.1, 11.7, 11.8).
const (
	GitBranch   GitState = "branch"   // HEAD on a branch
	GitDetached GitState = "detached" // CI checkouts, bisect, a rebase
	GitUnborn   GitState = "unborn"   // a repo with no commit yet
	GitNone     GitState = "none"     // the folder is not a git repo
)

// Git is the git block; a zero State means "no git facts" (block absent).
type Git struct {
	Branch string // set only in GitBranch
	Commit string // full id; empty in GitUnborn and GitNone
	State  GitState
}

// Import is the import slot of `_o` (PROVENANCE_DESIGN 5.1: importer kind,
// the external_sources id, the external id). The engine fills it from the
// importer's facts; agent r3-batch wires the importers to it. Zero = absent.
type Import struct {
	Source string // importer kind: jira, csv, learn-from, …
	Src    string // ext_ id of the registered source, when registered
	ExtID  string // the external id in that source
}

// IsZero reports an absent import block.
func (i Import) IsZero() bool { return i == Import{} }

// Key is PROVENANCE_DESIGN 11.4's import key: (src, ext_id), or
// (source, ext_id) when no source record exists (a one-off CSV).
func (i Import) Key() ImportKey { return ImportKey{Source: cmp.Or(i.Src, i.Source), ExtID: i.ExtID} }

// ImportKey identifies one external item across re-imports (11.4, PROV9).
type ImportKey struct {
	Source string // the ext_ id when registered, else the importer kind
	ExtID  string
}

// ImportOf reads the import slot of an `_o` block; ok is false when the
// row was not imported (no block, no ext_id) or `_o` is unreadable. The one
// reader of the slot, so importers (task, resource) and the
// duplicate_import check agree on its keys (r3-batch).
func ImportOf(o value.JSON) (Import, bool) {
	if o.IsZero() {
		return Import{}, false
	}
	ms, err := canon.Members(o.Raw())
	if err != nil {
		return Import{}, false
	}
	for _, m := range ms {
		if m.Key != KeyImport {
			continue
		}
		im, ierr := canon.Members(m.Raw)
		if ierr != nil {
			return Import{}, false
		}
		var out Import
		for _, f := range im {
			switch f.Key {
			case importSource:
				out.Source = textOf(f.Raw)
			case importSrc:
				out.Src = textOf(f.Raw)
			case importExtID:
				out.ExtID = textOf(f.Raw)
			}
		}
		return out, out.ExtID != ""
	}
	return Import{}, false
}

// Facts are what one create knows about where it happened.
type Facts struct {
	// Cmd is the command path, words only (`memory add`); never arguments,
	// which can carry row text and so a secret (PROVENANCE_DESIGN 5.1).
	Cmd string
	// Store names the store the row was written into: its sto_ id when
	// the store has one, else the attached name (the repo store, a
	// v0.1.17 marker) (PROVENANCE 5.1, r3-origin).
	Store string
	// Folder is the store folder's prj_ id (its _meta.json project_id);
	// "" for the store root or outside a store (PROVENANCE 5.1). Never
	// client-visible: ClientFilter keeps only allowlisted keys.
	Folder string
	// Repo is the rep_ id of the checkout the command ran in; "" absent.
	Repo string
	// Project is the prj_ id of the lore place the command ran in (its
	// _meta.json project_id); "" absent (PROVENANCE 5.1). ClientFilter
	// keeps it only when it is the client repo's own project id.
	Project string
	// Dir is the working folder relative to the repo root, forward
	// slashes; "" at the root (RelDir builds it).
	Dir string
	// Git is the HEAD state at creation; zero State = absent.
	Git Git
	// Import is the importer's slot; zero = absent.
	Import Import
}

// Build returns the `_o` block for f (canonical JSON, keys sorted).
func Build(f Facts) (value.JSON, error) {
	var ms []canon.Member
	add := func(k, v string) {
		if v != "" {
			ms = append(ms, canon.Member{Key: k, Raw: canon.String(v)})
		}
	}
	add(KeyCmd, f.Cmd)
	add(KeyStore, f.Store)
	add(KeyFolder, f.Folder)
	add(KeyRepo, f.Repo)
	add(KeyProject, f.Project)
	add(KeyDir, f.Dir)
	if f.Git.State != "" {
		g, err := gitObject(f.Git)
		if err != nil {
			return value.JSON{}, err
		}
		ms = append(ms, canon.Member{Key: KeyGit, Raw: g})
	}
	if !f.Import.IsZero() {
		var im []canon.Member
		for _, kv := range [][2]string{{importSource, f.Import.Source}, {importSrc, f.Import.Src}, {importExtID, f.Import.ExtID}} {
			if kv[1] != "" {
				im = append(im, canon.Member{Key: kv[0], Raw: canon.String(kv[1])})
			}
		}
		obj, err := canon.Object(im)
		if err != nil {
			return value.JSON{}, errs.Wrap(errs.Internal, "origin: build the import block", err)
		}
		ms = append(ms, canon.Member{Key: KeyImport, Raw: obj})
	}
	return object(ms)
}

// gitObject encodes g; a branch is written only in GitBranch and a commit
// only when there is one.
func gitObject(g Git) ([]byte, error) {
	ms := []canon.Member{{Key: gitState, Raw: canon.String(string(g.State))}}
	if g.State == GitBranch && g.Branch != "" {
		ms = append(ms, canon.Member{Key: gitBranch, Raw: canon.String(g.Branch)})
	}
	if g.Commit != "" && g.State != GitUnborn && g.State != GitNone {
		ms = append(ms, canon.Member{Key: gitCommit, Raw: canon.String(g.Commit)})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "origin: build the git block", err)
	}
	return obj, nil
}

// object builds a JSON object; no members is the zero JSON (absent _o).
func object(ms []canon.Member) (value.JSON, error) {
	if len(ms) == 0 {
		return value.JSON{}, nil
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "origin: build _o", err)
	}
	return value.JSONOf(obj)
}

// RelDir returns workDir relative to repoRoot with forward slashes, ""
// when workDir is the root, outside it, or either is empty.
//
// PROV6: `_o.dir` is kept as a relative folder for monorepos
// (`services/billing`), omitted at the root; the absolute path is never
// stored (it names the person's disk layout).
func RelDir(repoRoot, workDir string) string {
	if repoRoot == "" || workDir == "" {
		return ""
	}
	rel, err := filepath.Rel(filepath.Clean(repoRoot), filepath.Clean(workDir))
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return ""
	}
	return rel
}

// Client is what a client repo already holds or can see, the test of
// PROVENANCE_DESIGN 8.1: a key of `_o` is written into a client repo only
// if everything it names lives in, or is visible from, that repo.
type Client struct {
	// OwnStore is the `_o.store` value that names the client repo itself
	// (the repo store's name); any other store value is internal.
	OwnStore string
	// RepoID and ProjectID are the client repo's own ids ("" unknown:
	// then `_o.repo` / `_o.project` never pass).
	RepoID    string
	ProjectID string
	// BornHere is true when the block was built for a command that ran in
	// this client repo (a create there), so dir and git name the client's
	// own folder and commit. A row moved in from our side is not.
	BornHere bool
	// Upstream reports whether branch has an upstream on one of the
	// client repo's remotes; nil = none has.
	Upstream func(branch string) bool
	// LocalSource reports whether an ext_ id is an external_sources row
	// stored in this client repo; nil = none is.
	LocalSource func(src string) bool
	// LocalRow reports whether a row id lives in this client repo; nil =
	// none does.
	LocalRow func(id string) bool
}

// ClientFilter returns the part of `_o` a client repo may hold
// (PROVENANCE_DESIGN 8.1). Fails closed: a key it does not know (org,
// folder, plugin, moves, anything new) is dropped. A block left empty is
// the zero JSON (no `_o`).
//
// PROV8: a client repo keeps the commit id and the git state and drops a
// local-only branch name: `git.branch` survives only when the branch has an
// upstream on the client's remote (the client already sees it there),
// because a branch name such as `fix/acme-overbilling` can say more than
// the code (11.10). Nothing records that it was dropped.
//
// PROV7: no internal sidecar copy of the dropped keys is kept; the full
// block stays only in our own place's history.
func ClientFilter(o value.JSON, c Client) (value.JSON, error) {
	if o.IsZero() {
		return o, nil
	}
	ms, err := canon.Members(o.Raw())
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "origin: read _o", err)
	}
	var keep []canon.Member
	for _, m := range ms {
		s := textOf(m.Raw)
		switch m.Key {
		case KeyCmd:
			keep = append(keep, m)
		case KeyStore:
			if c.OwnStore != "" && s == c.OwnStore {
				keep = append(keep, m)
			}
		case KeyRepo:
			if c.RepoID != "" && s == c.RepoID {
				keep = append(keep, m)
			}
		case KeyProject:
			if c.ProjectID != "" && s == c.ProjectID {
				keep = append(keep, m)
			}
		case KeyDir:
			if c.BornHere {
				keep = append(keep, m)
			}
		case KeyGit:
			if !c.BornHere {
				continue
			}
			g, gerr := clientGit(m.Raw, c.Upstream)
			if gerr != nil {
				return value.JSON{}, gerr
			}
			if g != nil {
				keep = append(keep, canon.Member{Key: KeyGit, Raw: g})
			}
		case KeyImport:
			if c.LocalSource != nil && c.LocalSource(srcOf(m.Raw)) {
				keep = append(keep, m)
			}
		case KeyCopiedFrom:
			if c.LocalRow != nil && s != "" && c.LocalRow(s) {
				keep = append(keep, m)
			}
		}
	}
	return object(keep)
}

// clientGit keeps state and commit and the branch only with an upstream.
func clientGit(raw []byte, upstream func(string) bool) ([]byte, error) {
	ms, err := canon.Members(raw)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "origin: read _o.git", err)
	}
	var keep []canon.Member
	for _, m := range ms {
		switch m.Key {
		case gitState, gitCommit:
			keep = append(keep, m)
		case gitBranch:
			if upstream != nil && upstream(textOf(m.Raw)) {
				keep = append(keep, m)
			}
		}
	}
	if len(keep) == 0 {
		return nil, nil
	}
	obj, err := canon.Object(keep)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "origin: build _o.git", err)
	}
	return obj, nil
}

// srcOf returns import.src of an import block ("" when absent).
func srcOf(raw []byte) string {
	ms, err := canon.Members(raw)
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.Key == importSrc {
			return textOf(m.Raw)
		}
	}
	return ""
}

// textOf decodes a JSON string member ("" for any other value).
func textOf(raw []byte) string {
	if len(raw) < 2 || raw[0] != '"' {
		return ""
	}
	t, err := value.Parse(value.KindText, raw)
	if err != nil {
		return ""
	}
	return t.String()
}

// KeyWrite is the envelope key of the write stamp.
const KeyWrite = "_w"

// Keys of the `_w` object.
const (
	writeMachine = "m"
	writeVersion = "v"
)

// Stamp returns the `_w` block: which lore version and which machine wrote
// the current content. Zero JSON when there is nothing to say.
//
// OQ7 / PROVENANCE_DESIGN 6.2: written in every save that changes content
// (untouched rows are never restamped); noMachine (a client repo, and
// since Q-R3-9 every code repo) gets `{v}` only, so no machine id of ours
// ever reaches a client (Q3: the machine profile file and `lore machines`
// are deferred, only the id is written).
func Stamp(version string, m ids.MachineID, noMachine bool) (value.JSON, error) {
	var ms []canon.Member
	if !noMachine && !m.IsZero() {
		ms = append(ms, canon.Member{Key: writeMachine, Raw: canon.String(m.String())})
	}
	if version != "" {
		ms = append(ms, canon.Member{Key: writeVersion, Raw: canon.String(version)})
	}
	return object(ms)
}

// Text returns the string member key of `_o` ("" when absent, not a
// string, or o is not an object).
func Text(o value.JSON, key string) string {
	if o.IsZero() {
		return ""
	}
	ms, err := canon.Members(o.Raw())
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.Key == key {
			return textOf(m.Raw)
		}
	}
	return ""
}

// With returns o with the string member key set to v (added or
// replaced); a zero o starts an empty object.
func With(o value.JSON, key, v string) (value.JSON, error) {
	var ms []canon.Member
	if !o.IsZero() {
		var err error
		if ms, err = canon.Members(o.Raw()); err != nil {
			return value.JSON{}, errs.Wrap(errs.Internal, "origin: read _o", err)
		}
	}
	out := ms[:0:0]
	for _, m := range ms {
		if m.Key != key {
			out = append(out, m)
		}
	}
	out = append(out, canon.Member{Key: key, Raw: canon.String(v)})
	return object(out)
}
