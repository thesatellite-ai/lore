package importscan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// world is a fake HOME with its lore home.
type world struct {
	home, lore string
}

func newWorld(t *testing.T) world {
	t.Helper()
	h := t.TempDir()
	w := world{home: h, lore: filepath.Join(h, ".lore")}
	if err := os.MkdirAll(w.lore, 0o700); err != nil {
		t.Fatal(err)
	}
	return w
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repo makes a lore repo at root with an optional stores.yaml.
func repo(t *testing.T, root, stores string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".lore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if stores != "" {
		write(t, filepath.Join(root, ".lore", "stores.yaml"), stores)
	}
}

// machineDB writes a machine.db holding known_repos rows for roots.
func machineDB(t *testing.T, w world, roots ...string) {
	t.Helper()
	path := filepath.Join(w.lore, "state", "machine.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE machine_rows (tbl TEXT NOT NULL, id TEXT NOT NULL, hlc TEXT NOT NULL, deleted INTEGER NOT NULL, doc BLOB NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY (tbl, id))`); err != nil {
		t.Fatal(err)
	}
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	for i, root := range roots {
		id, err := ids.New[ids.KnownRepo]()
		if err != nil {
			t.Fatal(err)
		}
		rootV, _ := value.TextOf(root)
		repoV, _ := value.TextOf("rep_test" + string(rune('a'+i)))
		now := time.Now().UTC()
		r := row.Row{Table: "known_repos", ID: id.Any(), HLC: hlc.Next(hlc.HLC{}, now, actor), CreatedAt: now, CreatedBy: actor, UpdatedAt: now, UpdatedBy: actor,
			Fields: map[string]value.Value{"root": rootV, "repo_id": repoV}}
		doc, err := row.Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO machine_rows VALUES (?, ?, ?, 0, ?, ?)`, "known_repos", id.String(), r.HLC.String(), doc, i+1); err != nil {
			t.Fatal(err)
		}
	}
}

func scan(t *testing.T, w world, folders ...string) Result {
	t.Helper()
	res, err := Scan(context.Background(), Options{LoreHome: w.lore, Home: w.home, Folders: folders, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func sourceState(res Result, kind, base string) State {
	for _, s := range res.Sources {
		if s.Kind == kind && filepath.Base(s.Path) == base {
			return s.State
		}
	}
	return ""
}

func proposal(res Result, name string) (Proposal, bool) {
	i := slices.IndexFunc(res.Proposals, func(p Proposal) bool { return p.Entry.Name == name })
	if i < 0 {
		return Proposal{}, false
	}
	return res.Proposals[i], true
}

func field(p Proposal, name string) (Field, bool) {
	i := slices.IndexFunc(p.Fields, func(f Field) bool { return f.Field == name })
	if i < 0 {
		return Field{}, false
	}
	return p.Fields[i], true
}

const configFull = `identity:
  key: human:pat
  name: Pat Example
personal:
  store: git@github-personal:octo-personal/notes.git
orgs:
  - name: acme
    identity:
      name: Pat at Acme
    stores:
      - url: git@github-work:acme/lore-store.git
`

// TestConfigSourceStates: config.yaml missing, empty, malformed, present.
func TestConfigSourceStates(t *testing.T) {
	cases := []struct {
		name, body string
		want       State
		proposals  int
	}{
		{"missing", "", StateMissing, 0},
		{"empty", "  \n", StateEmpty, 0},
		{"malformed", "identity: [unclosed\n", StateMalformed, 0},
		{"not a mapping", "- a\n- b\n", StateMalformed, 0},
		{"present", configFull, StateFound, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			if c.name != "missing" {
				write(t, filepath.Join(w.lore, ConfigFile), c.body)
			}
			res := scan(t, w)
			if got := sourceState(res, KindConfig, ConfigFile); got != c.want {
				t.Errorf("state %q, want %q", got, c.want)
			}
			if len(res.Proposals) != c.proposals {
				t.Errorf("%d proposals, want %d: %+v", len(res.Proposals), c.proposals, res.Proposals)
			}
		})
	}
}

// TestFullConfigProposals: one solo and one member proposal with values
// and provenance lines.
func TestFullConfigProposals(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull)
	res := scan(t, w)
	p, ok := proposal(res, "personal")
	if !ok {
		t.Fatalf("no personal proposal: %+v", res.Proposals)
	}
	if p.Entry.Kind != onboard.KindSolo || p.Entry.Identity.Name != "Pat Example" || p.Entry.Identity.Key != "human:pat" || !p.Entry.Default {
		t.Errorf("personal entry %+v", p.Entry)
	}
	if p.Entry.PersonalStore != "git@github-personal:octo-personal/notes.git" || p.Entry.GitHub.HostAlias != "github-personal" {
		t.Errorf("personal store/alias %+v", p.Entry)
	}
	if f, _ := field(p, FieldIdentityName); f.Line != 3 || filepath.Base(f.File) != ConfigFile {
		t.Errorf("identity.name provenance %+v, want config.yaml:3", f)
	}
	m, ok := proposal(res, "acme")
	if !ok {
		t.Fatalf("no acme proposal")
	}
	if m.Entry.Kind != onboard.KindMember || m.Entry.Org != "acme" || !m.Entry.Identity.IsZero() || m.Entry.GitHub.HostAlias != "github-work" {
		t.Errorf("acme entry %+v", m.Entry)
	}
	if f, _ := field(m, FieldOrg); f.Line != 7 {
		t.Errorf("org line %d, want 7", f.Line)
	}
	// The entries must pass the same validation config.yaml writes use.
	var entries []onboard.SetupEntry
	for _, pr := range res.Proposals {
		entries = append(entries, pr.Entry)
	}
	if probs := onboard.Validate(entries, nil, onboard.Known{Orgs: []string{"acme"}, Home: w.home}); len(probs) > 0 {
		t.Errorf("proposals do not validate: %v", probs)
	}
}

// TestLegacyIdentities: an old identity.yaml / identity.toml fills the
// solo identity when config.yaml has none; their *.migrated backups never
// do (lore wrote them after importing the file, owner report 2026-11-07)
// and are listed as KindIdentityBackup; each file state is reported.
func TestLegacyIdentities(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string // identity name, "" none
		state map[string]State
	}{
		{"none", nil, "", map[string]State{IdentityYAML: StateMissing, IdentityYAML + MigratedSuffix: StateMissing}},
		{"yaml migrated backup is not an import", map[string]string{IdentityYAML + MigratedSuffix: "display_name: Pat Example\nstable_key: human:pat\n"}, "",
			map[string]State{IdentityYAML + MigratedSuffix: StateFound, IdentityYAML: StateMissing}},
		{"toml migrated backup is not an import", map[string]string{IdentityTOML + MigratedSuffix: "# old\ndisplay_name = \"Pat Toml\"\n"}, "",
			map[string]State{IdentityTOML + MigratedSuffix: StateFound, IdentityTOML: StateMissing}},
		{"old file beside its backup", map[string]string{IdentityYAML: "display_name: Pat Yaml\n", IdentityYAML + MigratedSuffix: "display_name: Pat Backup\n"}, "Pat Yaml",
			map[string]State{IdentityYAML: StateFound, IdentityYAML + MigratedSuffix: StateFound}},
		{"yaml beats toml", map[string]string{IdentityYAML: "display_name: Pat Yaml\n", IdentityTOML: "display_name = \"Pat Toml\"\n"}, "Pat Yaml", nil},
		{"empty file", map[string]string{IdentityYAML: ""}, "", map[string]State{IdentityYAML: StateEmpty}},
		{"names nobody", map[string]string{IdentityYAML: "other: 1\n"}, "", map[string]State{IdentityYAML: StateEmpty}},
		{"malformed yaml", map[string]string{IdentityYAML: "display_name: [x\n"}, "", map[string]State{IdentityYAML: StateMalformed}},
		{"malformed toml", map[string]string{IdentityTOML: "[table]\nname = \"x\"\n"}, "", map[string]State{IdentityTOML: StateMalformed}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			for n, b := range c.files {
				write(t, filepath.Join(w.lore, n), b)
			}
			res := scan(t, w)
			p, ok := proposal(res, "personal")
			got := ""
			if ok {
				got = p.Entry.Identity.Name
			}
			if got != c.want {
				t.Errorf("identity %q, want %q", got, c.want)
			}
			for n, st := range c.state {
				kind := KindIdentityOld
				if strings.HasSuffix(n, MigratedSuffix) {
					kind = KindIdentityBackup
				}
				if s := sourceState(res, kind, n); s != st {
					t.Errorf("%s state %q, want %q", n, s, st)
				}
			}
		})
	}
}

// TestMachineDBRepos: known repos give folders; a repo whose stores.yaml
// names an org's store goes to that org's setup.
func TestMachineDBRepos(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull)
	notes := filepath.Join(w.home, "code", "notes")
	work := filepath.Join(w.home, "work", "acme", "app")
	repo(t, notes, "stores:\n  - name: me\n    url: git@github-personal:octo-personal/notes.git\n")
	repo(t, work, "stores:\n  - name: acme\n    url: git@github-work:acme/lore-store\n")
	machineDB(t, w, notes, work)
	res := scan(t, w)
	if got := sourceState(res, KindMachineDB, "machine.db"); got != StateFound {
		t.Fatalf("machine.db state %q", got)
	}
	if len(res.Repos) != 2 {
		t.Fatalf("repos %+v", res.Repos)
	}
	p, _ := proposal(res, "personal")
	if !slices.Equal(p.Entry.Folders, []string{"~/code/**"}) {
		t.Errorf("personal folders %v", p.Entry.Folders)
	}
	m, _ := proposal(res, "acme")
	if !slices.Equal(m.Entry.Folders, []string{"~/work/acme/**"}) {
		t.Errorf("acme folders %v", m.Entry.Folders)
	}
}

// TestMachineDBStates: missing, empty file, not SQLite, no table.
func TestMachineDBStates(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T, path string)
		want State
	}{
		{"missing", func(*testing.T, string) {}, StateMissing},
		{"empty", func(t *testing.T, p string) { write(t, p, "") }, StateEmpty},
		{"not sqlite", func(t *testing.T, p string) { write(t, p, "hello, not a database at all, just text") }, StateMalformed},
		{"no rows", func(t *testing.T, p string) { machineDB(t, world{lore: filepath.Dir(filepath.Dir(p))}) }, StateEmpty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			c.make(t, filepath.Join(w.lore, "state", "machine.db"))
			if got := sourceState(scan(t, w), KindMachineDB, "machine.db"); got != c.want {
				t.Errorf("state %q, want %q", got, c.want)
			}
		})
	}
}

// TestFolderScan: depth limit, node_modules / vendor / hidden skipped,
// .lore found, malformed stores.yaml reported.
func TestFolderScan(t *testing.T) {
	w := newWorld(t)
	base := filepath.Join(w.home, "src")
	repo(t, filepath.Join(base, "a"), "stores:\n  - name: me\n    url: git@github.com:octo-personal/a.git\n")
	repo(t, filepath.Join(base, "x", "y", "z", "deep"), "")        // depth 4: found
	repo(t, filepath.Join(base, "x", "y", "z", "w", "deeper"), "") // depth 5: not found
	repo(t, filepath.Join(base, "node_modules", "pkg"), "")
	repo(t, filepath.Join(base, "vendor", "v"), "")
	repo(t, filepath.Join(base, ".hidden", "h"), "")
	repo(t, filepath.Join(base, "bad"), "stores: 12\n")
	res := scan(t, w, "~/src")
	var roots []string
	for _, r := range res.Repos {
		rel, _ := filepath.Rel(base, r.Root)
		roots = append(roots, filepath.ToSlash(rel))
	}
	slices.Sort(roots)
	if want := []string{"a", "bad", "x/y/z/deep"}; !slices.Equal(roots, want) {
		t.Errorf("repos %v, want %v", roots, want)
	}
	for _, s := range res.Sources {
		if s.Kind == KindStores && strings.Contains(s.Path, "bad") && s.State != StateMalformed {
			t.Errorf("bad stores.yaml state %q", s.State)
		}
	}
	p, ok := proposal(res, "personal")
	if !ok || p.Entry.PersonalStore != "git@github.com:octo-personal/a.git" || p.Entry.GitHub.HostAlias != "" {
		t.Errorf("personal %+v", p.Entry)
	}
	if missing := scan(t, w, filepath.Join(w.home, "no", "such", "folder")); sourceState(missing, KindFolder, "folder") != StateMissing {
		t.Errorf("missing folder not reported")
	}
}

// TestFolderScanKnownRepos: a scanned folder whose lore repos machine.db
// already lists says so, never "empty, 0 lore repos".
func TestFolderScanKnownRepos(t *testing.T) {
	w := newWorld(t)
	base := filepath.Join(w.home, "src")
	a, b, c := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c")
	for _, r := range []string{a, b} {
		repo(t, r, "")
	}
	machineDB(t, w, a, b)
	detail := func(res Result) (State, string) {
		for _, s := range res.Sources {
			if s.Kind == KindFolder {
				return s.State, s.Detail
			}
		}
		return "", ""
	}
	if st, d := detail(scan(t, w, "~/src")); st != StateFound || d != "2 lore repos, already known" {
		t.Errorf("all known: %s %q", st, d)
	}
	repo(t, c, "")
	if st, d := detail(scan(t, w, "~/src")); st != StateFound || d != "3 lore repos (2 already known)" {
		t.Errorf("mixed: %s %q", st, d)
	}
	if st, d := detail(scan(t, w, "~/.lore")); st != StateEmpty || d != "0 lore repos" {
		t.Errorf("none: %s %q", st, d)
	}
	// Every count in a source detail is singular for one.
	for _, s := range scan(t, w).Sources {
		if s.Kind == KindMachineDB && s.Detail != "2 known repos" {
			t.Errorf("machine.db detail %q", s.Detail)
		}
	}
	if counted(1, "setup") != "1 setup" || counted(0, "org") != "0 orgs" || counted(2, "store") != "2 stores" {
		t.Error("counted")
	}
}

// TestCoveredAndRenamed: an existing setup of the org hides its proposal;
// a taken name gets a suffix.
func TestCoveredAndRenamed(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull+"setups:\n  - name: work\n    kind: member\n    org: acme\n  - name: personal\n    kind: member\n    org: other\n    default: true\n")
	res := scan(t, w)
	if _, ok := proposal(res, "acme"); ok {
		t.Errorf("acme proposed although setup work covers it")
	}
	p, ok := proposal(res, "personal-2")
	if !ok || p.Entry.Default {
		t.Errorf("want personal-2 without default, got %+v", res.Proposals)
	}
	if len(res.Covered) != 1 {
		t.Errorf("covered %v", res.Covered)
	}
}

// TestScanWritesNothing hashes every file and folder listing under the
// fake HOME before and after a full scan: nothing may change.
func TestScanWritesNothing(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull)
	write(t, filepath.Join(w.lore, IdentityYAML+MigratedSuffix), "display_name: Pat Example\n")
	write(t, filepath.Join(w.lore, IdentityTOML+MigratedSuffix), "display_name = \"Pat\"\n")
	notes := filepath.Join(w.home, "code", "notes")
	repo(t, notes, "stores:\n  - name: me\n    url: git@github-personal:octo-personal/notes.git\n")
	repo(t, filepath.Join(w.home, "code", "node_modules", "x"), "")
	// A teammate's repo lore was never run in: only setup.discover's
	// FolderStores reads it (issue 14).
	repo(t, filepath.Join(w.home, "work", "acme", "app"), "stores:\n  - name: team\n    url: git@github.com:acme/lore.git\n")
	machineDB(t, w, notes)
	before := snapshot(t, w.home)
	res := scan(t, w, "~/code", "~")
	if len(res.Proposals) == 0 {
		t.Fatal("the scan found nothing; the test would prove nothing")
	}
	found := FolderStores(context.Background(), StoreScan{Folders: []string{"~/work", "~/code"}, Home: w.home, Known: []string{notes}})
	if len(found.Repos) == 0 || len(found.Repos[0].Stores) == 0 {
		t.Fatalf("FolderStores found nothing; the test would prove nothing: %+v", found)
	}
	after := snapshot(t, w.home)
	if !maps(before, after) {
		for k, v := range after {
			if before[k] != v {
				t.Errorf("changed: %s", k)
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				t.Errorf("removed: %s", k)
			}
		}
	}
}

// snapshot maps every path under root to a hash of its content (files),
// its sorted listing (folders) or its target (symlinks), plus its mode and
// modification time.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h := sha256.New()
		switch {
		case d.IsDir():
			ents, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			for _, e := range ents {
				h.Write([]byte(e.Name() + "\n"))
			}
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(b)
		}
		stamp := info.ModTime().String()
		if d.IsDir() && runtime.GOOS == "windows" {
			// NTFS updates a folder's write time lazily in its parent's
			// index (what a directory listing reports): it moves without a
			// write under it. The listing above covers what a folder holds.
			stamp = ""
		}
		out[p] = hex.EncodeToString(h.Sum(nil)) + " " + info.Mode().String() + " " + stamp
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func maps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestHostAlias(t *testing.T) {
	cases := map[string]string{
		"git@github-work:acme/x.git":     "github-work",
		"git@github.com:acme/x.git":      "",
		"ssh://git@github-work/acme/x":   "github-work",
		"ssh://git@github.com:22/acme/x": "",
		"https://github.com/acme/x":      "",
		"file:///tmp/x":                  "",
		"/tmp/x":                         "",
	}
	for in, want := range cases {
		if got := hostAlias(in); got != want {
			t.Errorf("hostAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetupNameFor(t *testing.T) {
	for in, want := range map[string]string{"acme": "acme", "Acme Corp": "acme-corp", "42labs": "org-42labs", "a.b": "a-b"} {
		if got := setupNameFor(in); got != want || !onboard.ValidSetupName(got) {
			t.Errorf("setupNameFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// A solo setup that inherits personal.store covers it: no "personal-2"
// proposal for the store an existing setup already uses (realmac NEW-3);
// one with its own other store does not.
func TestInheritSetupCoversPersonalStore(t *testing.T) {
	for name, c := range map[string]struct {
		store   string
		covered bool
	}{
		"inherit":     {"inherit", true},
		"unset":       {"", true},
		"other store": {"git@github.com:work-user/notes.git", false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			body := "setups:\n  - name: personal\n    kind: solo\n"
			if c.store != "" {
				body += "    personal_store: \"" + c.store + "\"\n"
			}
			write(t, filepath.Join(w.lore, ConfigFile), configFull+body)
			res := scan(t, w)
			_, proposed := proposal(res, "personal-2")
			if proposed == c.covered {
				t.Errorf("personal-2 proposed=%v, want %v (covered %v)", proposed, !c.covered, res.Covered)
			}
		})
	}
}

// TestReposSortedByRoot: repos and their stores.yaml sources come out in
// path order, not machine.db's row id order (random ids made the order
// change from run to run).
func TestReposSortedByRoot(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull)
	var roots []string
	for _, n := range []string{"zeta", "mid", "alpha"} {
		r := filepath.Join(w.home, "code", n)
		repo(t, r, "")
		roots = append(roots, r)
	}
	machineDB(t, w, roots...) // inserted z, m, a: id order is not path order
	res := scan(t, w)
	var got, stores []string
	for _, r := range res.Repos {
		got = append(got, filepath.Base(r.Root))
	}
	for _, s := range res.Sources {
		if s.Kind == KindStores {
			stores = append(stores, s.Path)
		}
	}
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("repos %v, want %v", got, want)
	}
	if !slices.IsSorted(stores) || len(stores) != 3 {
		t.Fatalf("stores.yaml sources not in path order: %v", stores)
	}
}

// TestMachineDBWithTMPDIRGone: machine.db is still read when TMPDIR names
// a folder that is gone (its private copy goes next to it).
func TestMachineDBWithTMPDIRGone(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull)
	notes := filepath.Join(w.home, "code", "notes")
	repo(t, notes, "stores:\n  - name: me\n    url: git@github-personal:octo-personal/notes.git\n")
	machineDB(t, w, notes)
	testenv.SetTemp(t, filepath.Join(t.TempDir(), "gone"))
	res, err := Scan(context.Background(), Options{LoreHome: w.lore, Home: w.home}) // TempDir "": the system one, as lore runs it
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceState(res, KindMachineDB, "machine.db"); got != StateFound {
		t.Fatalf("machine.db state %q with TMPDIR gone", got)
	}
}

// TestWalkFolderSkipsStoreClones (STORE layout 1.5): a lore store's clone
// (lore-store.yaml at its top, a person's own made with git clone) is
// never offered as a repo to set up, even with a .lore folder inside it;
// a repo beside it is.
func TestWalkFolderSkipsStoreClones(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{filepath.Join(base, "notes", "client_a", ".lore", "data"), filepath.Join(base, "app", ".lore")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "notes", "lore-store.yaml"), []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var res Result
	got, _ := walkFolder(t.Context(), base, 4, 10, map[string]bool{}, &res)
	if len(got) != 1 || got[0].Root != filepath.Join(base, "app") {
		t.Fatalf("walk found %+v, want only the app repo", got)
	}
}
