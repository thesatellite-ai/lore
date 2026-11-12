package importscan

// sources.go — the readers: config.yaml, the legacy identity files,
// machine.db (from a private copy), the folder walk and each repo's
// stores.yaml. Each records one Source per file it looked at and never
// writes outside its own temporary folder.

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // the pure-Go driver lore already ships

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/tmpdir"
	"github.com/thesatellite-ai/lore/model/value"
)

// identityFact is one identity block (config.yaml identity:, an org's
// identity:, or a legacy file).
type identityFact struct {
	file              string
	key, name, handle located
}

func (i identityFact) zero() bool {
	return i.key.value == "" && i.name.value == "" && i.handle.value == ""
}

// orgFact is one orgs[] entry.
type orgFact struct {
	name     located
	identity identityFact
	stores   []located
}

// existingSetup is one setups[] entry already in config.yaml.
type existingSetup struct {
	name, kind, org, personalStore string
	// identityKey is a solo setup's identity.key: a proposal with the same
	// identity is the same person's setup, already made.
	identityKey string
	isDefault   bool
	// trashed: a trash.setups entry; disabled: setups[] with disabled:
	// true. Both still cover their proposals (owner decision 2026-11-06:
	// import never re-proposes what the person disabled or removed; the
	// way back is enable / trash restore).
	trashed, disabled bool
	// githubUser is github.user; handle identity.handle (a solo setup
	// without an account stands for the login its handle names).
	githubUser, handle string
}

// purgedSetup is one trash.purged entry (onboard.PurgedSetup): a setup
// deleted for good. It covers its proposals like a trashed one, matched
// by fingerprints because the entry keeps no identity key or store url.
type purgedSetup = onboard.PurgedSetup

// configFacts is what config.yaml holds that an import can use.
type configFacts struct {
	path          string
	identity      identityFact
	personalStore located
	orgs          []orgFact
	repoOrgs      map[string]located
	setups        []existingSetup
	purged        []purgedSetup
}

// readConfig reads <lore home>/config.yaml.
func readConfig(path string, res *Result) configFacts {
	cf := configFacts{path: path, repoOrgs: map[string]located{}}
	b, state, detail := readFile(path)
	src := Source{Kind: KindConfig, Path: path, State: state, Detail: detail}
	if state != StateFound {
		res.Sources = append(res.Sources, src)
		return cf
	}
	root, bad := parseYAML(b)
	if root == nil {
		src.State, src.Detail = StateMalformed, bad
		res.Sources = append(res.Sources, src)
		return cf
	}
	cf.identity = identityOf(get(root, "identity"), path)
	cf.personalStore = scalar(get(root, "personal"), "store")
	if orgs := get(root, "orgs"); orgs != nil && orgs.Kind == yaml.SequenceNode {
		for _, m := range orgs.Content {
			o := orgFact{name: scalar(m, "name"), identity: identityOf(get(m, "identity"), path)}
			if o.name.value == "" {
				continue
			}
			if st := get(m, "stores"); st != nil && st.Kind == yaml.SequenceNode {
				for _, s := range st.Content {
					if u := scalar(s, "url"); u.value != "" {
						o.stores = append(o.stores, u)
					}
				}
			}
			cf.orgs = append(cf.orgs, o)
		}
	}
	if ro := get(root, "repo_orgs"); ro != nil && ro.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(ro.Content); i += 2 {
			v := ro.Content[i+1]
			org := scalar(v, "org")
			if v.Kind == yaml.ScalarNode {
				org = located{value: strings.TrimSpace(v.Value), line: v.Line}
			}
			if org.value != "" {
				cf.repoOrgs[ro.Content[i].Value] = org
			}
		}
	}
	if ss := get(root, "setups"); ss != nil && ss.Kind == yaml.SequenceNode {
		for _, m := range ss.Content {
			e := existingSetup{name: scalar(m, "name").value, kind: scalar(m, "kind").value, org: scalar(m, "org").value,
				personalStore: scalar(m, "personal_store").value, isDefault: scalar(m, "default").value == "true",
				identityKey: scalar(get(m, "identity"), "key").value}
			e.disabled = scalar(m, "disabled").value == "true"
			e.githubUser, e.handle = scalar(get(m, "github"), "user").value, scalar(get(m, "identity"), "handle").value
			if e.name != "" {
				cf.setups = append(cf.setups, e)
			}
		}
	}
	if tr := get(root, "trash"); tr != nil {
		if ps := get(tr, "purged"); ps != nil && ps.Kind == yaml.SequenceNode {
			for _, m := range ps.Content {
				var p purgedSetup
				if m.Decode(&p) == nil && p.Name != "" {
					cf.purged = append(cf.purged, p)
				}
			}
		}
		if ts := get(tr, "setups"); ts != nil && ts.Kind == yaml.SequenceNode {
			for _, m := range ts.Content {
				e := existingSetup{name: scalar(m, "name").value, kind: scalar(m, "kind").value, org: scalar(m, "org").value,
					personalStore: scalar(m, "personal_store").value, trashed: true, identityKey: scalar(get(m, "identity"), "key").value,
					githubUser: scalar(get(m, "github"), "user").value, handle: scalar(get(m, "identity"), "handle").value}
				if e.name != "" {
					cf.setups = append(cf.setups, e)
				}
			}
		}
	}
	src.Detail = counted(len(cf.orgs), "org") + ", " + counted(len(cf.setups), "setup")
	if len(cf.purged) > 0 {
		src.Detail += ", " + counted(len(cf.purged), "deleted setup")
	}
	res.Sources = append(res.Sources, src)
	return cf
}

// identityOf reads an identity mapping (key, name, handle).
func identityOf(m *yaml.Node, file string) identityFact {
	if m == nil {
		return identityFact{}
	}
	return identityFact{file: file, key: scalar(m, "key"), name: scalar(m, "name"), handle: scalar(m, "handle")}
}

// readIdentities reads the legacy identity files, best first: identity.yaml
// (v0.1.17), then identity.toml (older). Their *.migrated copies are
// listed as KindIdentityBackup and never read into a proposal: lore wrote
// them when it moved the file into config.yaml, so what they name is
// already imported (and may belong to a setup since deleted).
func readIdentities(home string, res *Result) []identityFact {
	var out []identityFact
	for _, name := range []string{IdentityYAML, IdentityYAML + MigratedSuffix, IdentityTOML, IdentityTOML + MigratedSuffix} {
		path := filepath.Join(home, name)
		b, state, detail := readFile(path)
		if strings.HasSuffix(name, MigratedSuffix) {
			if state == StateFound {
				detail = backupDetail
			}
			res.Sources = append(res.Sources, Source{Kind: KindIdentityBackup, Path: path, State: state, Detail: detail})
			continue
		}
		src := Source{Kind: KindIdentityOld, Path: path, State: state, Detail: detail}
		if state == StateFound {
			var f identityFact
			var ok bool
			if strings.HasPrefix(name, IdentityTOML) {
				f, ok = legacyTOML(b, path)
			} else {
				f, ok = legacyYAML(b, path)
			}
			switch {
			case !ok:
				src.State, src.Detail = StateMalformed, "not a lore identity file"
			case f.zero():
				src.State, src.Detail = StateEmpty, "names nobody"
			default:
				src.Detail = "names " + firstNonEmpty(f.name.value, f.key.value)
				out = append(out, f)
			}
		}
		res.Sources = append(res.Sources, src)
	}
	return out
}

// backupDetail is a found *.migrated file's detail line.
const backupDetail = "backup of a migrated install: already in config.yaml, never imported again"

// legacyYAML reads display_name / stable_key (v0.1.17), or key / name /
// handle (a hand-written file in today's shape).
func legacyYAML(b []byte, path string) (identityFact, bool) {
	root, bad := parseYAML(b)
	if root == nil || bad != "" {
		return identityFact{}, false
	}
	f := identityOf(root, path)
	if k := scalar(root, "stable_key"); k.value != "" {
		f.key = k
	}
	if n := scalar(root, "display_name"); n.value != "" {
		f.name = n
	}
	return f, true
}

// legacyTOML reads `key = "value"` lines of a pre-0.1.17 identity.toml
// (flat keys only; a table header or a line that is not key = value is
// malformed).
func legacyTOML(b []byte, path string) (identityFact, bool) {
	f := identityFact{file: path}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, tomlCommentStart) {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "[") {
			return identityFact{}, false
		}
		v = strings.TrimSpace(v)
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `'`)
		}
		l := located{value: strings.TrimSpace(v), line: i + 1}
		switch strings.TrimSpace(k) {
		case "stable_key", "key":
			f.key = l
		case "display_name", "name":
			f.name = l
		case "handle":
			f.handle = l
		}
	}
	return f, true
}

// knownReposQuery reads the known_repos rows of the machine store.
const knownReposQuery = `SELECT doc FROM machine_rows WHERE tbl = 'known_repos' AND deleted = 0 ORDER BY id`

// readMachineRepos lists known_repos from a private copy of machine.db
// (and its -wal), so the scan never opens, locks or touches the real file.
func readMachineRepos(ctx context.Context, path, tempDir string, res *Result) []Repo {
	src := Source{Kind: KindMachineDB, Path: path}
	defer func() { res.Sources = append(res.Sources, src) }()
	st, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		src.State = StateMissing
		return nil
	case err != nil || !st.Mode().IsRegular():
		src.State, src.Detail = StateUnreadable, "cannot be read"
		return nil
	case st.Size() == 0:
		src.State = StateEmpty
		return nil
	}
	dir, err := os.MkdirTemp(tempDir, "lore-importscan-")
	if err != nil && tempDir == "" {
		// TMPDIR cannot be used: copy next to machine.db, in lore's home.
		dir, err = tmpdir.Dir("lore-importscan-", filepath.Dir(path))
	}
	if err != nil {
		src.State, src.Detail = StateUnreadable, "no temporary folder for a copy"
		return nil
	}
	defer func() { _ = os.RemoveAll(dir) }() // our own temporary copy only
	cp := filepath.Join(dir, "machine.db")
	if err := copyFile(path, cp); err != nil {
		src.State, src.Detail = StateUnreadable, "cannot be copied"
		return nil
	}
	if _, err := os.Lstat(path + "-wal"); err == nil {
		_ = copyFile(path+"-wal", cp+"-wal") // committed rows still in the log; a failed copy loses only those
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(cp))
	if err != nil {
		src.State, src.Detail = StateMalformed, "not a SQLite database"
		return nil
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, knownReposQuery)
	if err != nil {
		src.State, src.Detail = StateMalformed, "not a lore machine store"
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []Repo
	for rows.Next() {
		var doc []byte
		if rows.Scan(&doc) != nil {
			continue
		}
		r, derr := row.Decode(doc)
		if derr != nil {
			continue
		}
		root := text(r.Fields["root"])
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		out = append(out, Repo{Root: filepath.Clean(root), RepoID: text(r.Fields["repo_id"]), From: FromMachineDB})
	}
	if rows.Err() != nil {
		src.State, src.Detail = StateMalformed, "read stopped part way"
		return out
	}
	src.State = StateFound
	if len(out) == 0 {
		src.State = StateEmpty
	}
	src.Detail = counted(len(out), "known repo")
	return out
}

// text is a text field's value ("" for absent, null or another kind).
func text(v value.Value) string {
	if v == nil || v.Kind() != value.KindText {
		return ""
	}
	return v.String()
}

// copyFile copies src to dst (dst created 0600).
func copyFile(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(filepath.Clean(dst), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	return errors.Join(cerr, out.Close())
}

// skipDirs are never walked into.
var skipDirs = []string{"node_modules", ".git", "vendor"}

// walkFolder finds folders holding .lore/ under base, at most depth levels
// down and at most limit of them; hidden folders, node_modules, .git,
// vendor and lore stores' clones (lore-store.yaml) are skipped, symlinks
// are not followed.
func walkFolder(ctx context.Context, base string, depth, limit int, seen map[string]bool, res *Result) ([]Repo, bool) {
	src := Source{Kind: KindFolder, Path: base}
	defer func() { res.Sources = append(res.Sources, src) }()
	if !filepath.IsAbs(base) {
		src.State, src.Detail = StateUnreadable, "not an absolute folder"
		return nil, false
	}
	st, err := os.Stat(base)
	if err != nil {
		src.State = StateMissing
		return nil, false
	}
	if !st.IsDir() {
		src.State, src.Detail = StateUnreadable, "not a folder"
		return nil, false
	}
	base = filepath.Clean(base)
	var out []Repo
	known := 0 // lore repos here that machine.db (or an earlier folder) already listed
	truncated := false
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if werr != nil || !d.IsDir() {
			return nil // an unreadable folder is skipped, never an error
		}
		if p != base {
			name := d.Name()
			if slices.Contains(skipDirs, name) || (strings.HasPrefix(name, ".") && name != loreDir) {
				return fs.SkipDir
			}
			if name == loreDir {
				return fs.SkipDir
			}
			rel, _ := filepath.Rel(base, p)
			if strings.Count(filepath.ToSlash(rel), "/")+1 > depth {
				return fs.SkipDir
			}
		}
		if fi, err := os.Lstat(filepath.Join(p, rowfs.MarkerFile)); err == nil && fi.Mode().IsRegular() {
			return fs.SkipDir // a lore store's clone (a person's own, `lore store use`): never a repo to set up (STORE layout 1.5)
		}
		if fi, err := os.Lstat(filepath.Join(p, loreDir)); err == nil && fi.IsDir() {
			if seen[p] {
				known++
				return nil
			}
			if len(out) >= limit {
				truncated = true
				return fs.SkipAll
			}
			seen[p] = true
			out = append(out, Repo{Root: p, RepoID: repoID(p), From: FromScan})
		}
		return nil
	})
	src.State, src.Detail = StateFound, folderDetail(len(out), known)
	if len(out)+known == 0 {
		src.State = StateEmpty
	}
	return out, truncated
}

// folderDetail is a scanned folder's line: how many lore repos it holds
// and how many of those were already known (from machine.db or an earlier
// folder), so a folder of known repos never reads as empty.
func folderDetail(found, known int) string {
	switch {
	case known == 0:
		return loreRepos(found)
	case found == 0:
		return loreRepos(known) + ", already known"
	}
	return loreRepos(found+known) + " (" + strconv.Itoa(known) + " already known)"
}

// loreRepos is "1 lore repo" / "n lore repos".
func loreRepos(n int) string { return counted(n, "lore repo") }

// counted is "1 <word>" / "n <word>s": every count in a source's detail.
func counted(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// repoID reads <root>/.lore/data/_meta.json's repo_id ("" when absent);
// read-only, like rowfs.ReadRepoMeta, without its chain checks.
func repoID(root string) string {
	b, state, _ := readFile(filepath.Join(root, loreDir, "data", "_meta.json"))
	if state != StateFound {
		return ""
	}
	r, bad := parseYAML(b) // JSON is YAML
	if r == nil || bad != "" {
		return ""
	}
	return scalar(r, "repo_id").value
}

// readRepoStores reads <root>/.lore/stores.yaml into r.Stores.
func readRepoStores(r *Repo, res *Result) {
	path := filepath.Join(r.Root, filepath.FromSlash(RepoStoresFile))
	b, state, detail := readFile(path)
	src := Source{Kind: KindStores, Path: path, State: state, Detail: detail}
	defer func() { res.Sources = append(res.Sources, src) }()
	if state != StateFound {
		return
	}
	root, bad := parseYAML(b)
	list := get(root, "stores")
	if root == nil || (list != nil && list.Kind != yaml.SequenceNode) {
		src.State, src.Detail = StateMalformed, firstNonEmpty(bad, "stores is not a list")
		return
	}
	r.StoresFile = path
	if list != nil {
		for _, m := range list.Content {
			if u := scalar(m, "url"); u.value != "" {
				r.Stores = append(r.Stores, RepoStore{Name: scalar(m, "name").value, URL: u.value, Line: u.line})
			}
		}
	}
	src.Detail = counted(len(r.Stores), "store")
}

// firstNonEmpty is the first non-empty string.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
