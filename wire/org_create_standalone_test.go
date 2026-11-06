package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// orgWorld is a fake github.com (bare repos under hosts/, reached through
// url.insteadOf so lore sees real remote urls), one repo checkout with a
// store `agency` made by `lore store init`, and one HOME per person.
type orgWorld struct {
	root, hosts, repo string
}

const (
	orgTestStoreURL = "https://github.com/agency/notes"
	orgTestImport   = "git@github.com:agency/notes"
)

func orgGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: args})
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newOrgWorld(t *testing.T) *orgWorld {
	t.Helper()
	w := &orgWorld{root: t.TempDir()}
	w.hosts = filepath.Join(w.root, "hosts")
	gc := filepath.Join(w.root, "gitconfig")
	cfg := "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \"" + w.hosts + "/\"]\n\tinsteadOf = https://github.com/\n\tinsteadOf = git@github.com:\n"
	if err := os.WriteFile(gc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("LORE_HUMAN", "1")
	t.Setenv("LORE_ORG", "")
	for _, r := range []string{"agency/notes", "agency/empty", "acme/app"} {
		d := filepath.Join(w.hosts, r+".git")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		orgGit(t, d, "init", "-q", "--bare", "-b", "main")
	}
	w.repo = filepath.Join(w.root, "repo")
	orgGit(t, w.root, "clone", "-q", "https://github.com/acme/app", w.repo)
	orgGit(t, w.repo, "commit", "-q", "--allow-empty", "-m", "init")
	orgGit(t, w.repo, "push", "-q", "origin", "main")
	home := w.home(t, "mira")
	app, err := New(StandaloneOptions(w.repo, home, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	ctx := context.Background()
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(w.repo, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := app.SeedRepoMeta(ctx, pid); err != nil {
		t.Fatalf("seed _meta.json: %v", err)
	}
	for _, s := range []opsstores.InitIn{{Name: "agency", URL: orgTestStoreURL}, {Name: "empty", URL: "https://github.com/agency/empty"}} {
		if _, err := app.InitStore(ctx, s); err != nil {
			t.Fatalf("store init %s: %v", s.Name, err)
		}
	}
	return w
}

func (w *orgWorld) home(t *testing.T, who string) string {
	t.Helper()
	h := filepath.Join(w.root, "home-"+who)
	if err := os.MkdirAll(h, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", h)
	return filepath.Join(h, ".lore") // the binary's home is ~/.lore
}

// orgHost is cmd/lore's org host for home, run in dir.
func orgHostAt(t *testing.T, dir, home string) *standaloneOrg {
	t.Helper()
	o, _ := StandaloneOptionsOrg(dir, home, dir, "test")
	h, ok := o.OrgCommands.(*standaloneOrg)
	if !ok {
		t.Fatal("StandaloneOptionsOrg installs no standaloneOrg")
	}
	return h
}

func (w *orgWorld) storeFiles(t *testing.T) string {
	t.Helper()
	return orgGit(t, filepath.Join(w.hosts, "agency/notes.git"), "ls-tree", "-r", "--name-only", "main")
}

func wantCode(t *testing.T, err error, code errs.Code, what string) {
	t.Helper()
	if errs.CodeOf(err) != code {
		t.Fatalf("%s: got %v, want %s", what, err, code)
	}
}

func readConfig(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(configPath(home))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// TestOrgCreateImportRenameOnCmdLore: P3-ORG-G-2, the three verbs cmd/lore
// refused with E_UNSUPPORTED (pointing at the binary phase E deletes),
// over a real store pushed through git: refusals write nothing, a create
// writes lore-org.yaml as the org identity + the entry + a roster row, an
// import outside any repo adopts it and registers, a rename moves the
// label and its references.
func TestOrgCreateImportRenameOnCmdLore(t *testing.T) {
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	base := opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}

	refusals := []struct {
		name string
		edit func(*opsorgcmd.CreateIn)
		code errs.Code
	}{
		{"no identity key", func(in *opsorgcmd.CreateIn) { in.Key = "" }, errs.InvalidInput},
		{"no display name", func(in *opsorgcmd.CreateIn) { in.Name = "" }, errs.InvalidInput},
		{"unknown store name", func(in *opsorgcmd.CreateIn) { in.Store = "nope" }, errs.InvalidInput},
		{"invalid org name", func(in *opsorgcmd.CreateIn) { in.Org = "Bad Name" }, errs.ConfigInvalid},
	}
	for _, c := range refusals {
		in := base
		c.edit(&in)
		_, err := h.Create(ctx, in, true)
		wantCode(t, err, c.code, c.name)
	}
	rep, err := h.Create(ctx, base, false)
	if err != nil || rep.Ask != askCreate {
		t.Fatalf("unconfirmed create: %+v %v", rep, err)
	}
	if strings.Contains(w.storeFiles(t), "lore-org.yaml") || readConfig(t, mira) != "" {
		t.Fatal("refused or unconfirmed create wrote something")
	}

	rep, err = h.Create(ctx, base, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	text := strings.Join(rep.Lines, "\n")
	for _, want := range []string{"✓ wrote org/.lore/lore-org.yaml (pushed)", "✓ added org entry agency", "registered"} {
		if !strings.Contains(text, want) {
			t.Fatalf("create output lacks %q:\n%s", want, text)
		}
	}
	files := w.storeFiles(t)
	if !strings.Contains(files, "org/.lore/lore-org.yaml") || !strings.Contains(files, "org/.lore/data/actors/act_") {
		t.Fatalf("store after create:\n%s", files)
	}
	notes := filepath.Join(w.hosts, "agency/notes.git")
	if a := orgGit(t, notes, "log", "-1", "--format=%ae", "main", "--", "org/.lore/lore-org.yaml"); a != "mira@agency.example" {
		t.Fatalf("lore-org.yaml author %q, want the org identity", a)
	}
	if body := orgGit(t, notes, "show", "main:org/.lore/lore-org.yaml"); strings.Contains(body, "mira@") || !strings.Contains(body, "name: agency") {
		t.Fatalf("lore-org.yaml must name the org and no identity:\n%s", body)
	}
	if !strings.Contains(readConfig(t, mira), "key: human:mira@agency.example") {
		t.Fatalf("config after create:\n%s", readConfig(t, mira))
	}
	_, err = h.Create(ctx, base, true)
	wantCode(t, err, errs.InvalidInput, "second create of the same entry")

	// Another org on the same folder is refused with the import hint, and
	// nothing reaches config.yaml.
	other := base
	other.Org = "globex"
	_, err = h.Create(ctx, other, true)
	wantCode(t, err, errs.InvalidInput, "create over another org's lore-org.yaml")
	if strings.Contains(readConfig(t, mira), "globex") {
		t.Fatal("refused create added the entry")
	}

	// Import, outside any repo (store-only roster, BUG-T3).
	bob := w.home(t, "bob")
	out := filepath.Join(w.root, "outside")
	if merr := os.MkdirAll(out, 0o700); merr != nil {
		t.Fatal(merr)
	}
	hb := orgHostAt(t, out, bob)
	imp := opsorgcmd.ImportIn{URL: orgTestImport, Key: "human:bob@agency.example", Name: "Bob Roe", Handle: "bob"}
	_, err = hb.Import(ctx, opsorgcmd.ImportIn{URL: orgTestImport, Name: "Bob"}, true)
	wantCode(t, err, errs.InvalidInput, "import without a key")
	_, err = hb.Import(ctx, opsorgcmd.ImportIn{URL: "https://github.com/agency/empty", Key: imp.Key, Name: imp.Name}, true)
	wantCode(t, err, errs.InvalidInput, "import of a store with no lore-org.yaml")
	_, err = hb.Import(ctx, opsorgcmd.ImportIn{URL: "https://github.com/agency/missing", Key: imp.Key, Name: imp.Name}, true)
	wantCode(t, err, errs.Offline, "import of an unreachable store")
	rep, err = hb.Import(ctx, imp, false)
	if err != nil || rep.Ask != askImport || readConfig(t, bob) != "" {
		t.Fatalf("unconfirmed import: %+v %v", rep, err)
	}
	if _, err = hb.Import(ctx, imp, true); err != nil {
		t.Fatalf("import: %v", err)
	}
	cfg := readConfig(t, bob)
	if !strings.Contains(cfg, "name: agency") || !strings.Contains(cfg, "key: human:bob@agency.example") || !strings.Contains(cfg, "org_folder: org") {
		t.Fatalf("config after import:\n%s", cfg)
	}
	if n := strings.Count(w.storeFiles(t), "org/.lore/data/actors/act_"); n != 2 {
		t.Fatalf("roster rows after import = %d, want 2 (mira, bob)", n)
	}
	_, err = hb.Import(ctx, imp, true)
	wantCode(t, err, errs.InvalidInput, "import of a name already taken")
	imp.As = "agency-2"
	if _, err = hb.Import(ctx, imp, true); err != nil || !strings.Contains(readConfig(t, bob), "name: agency-2") {
		t.Fatalf("import --as: %v\n%s", err, readConfig(t, bob))
	}

	// Rename on mira's machine, inside the repo (the org folder's id).
	w.home(t, "mira")
	if _, uerr := h.Use(ctx, opsorgcmd.UseIn{Entry: "agency"}); uerr != nil {
		t.Fatalf("org use: %v", uerr)
	}
	_, err = h.Rename(ctx, opsorgcmd.RenameIn{Old: "nope", New: "x"})
	wantCode(t, err, errs.NotFound, "rename of an unknown entry")
	_, err = h.Rename(ctx, opsorgcmd.RenameIn{Old: "agency", New: "Bad Name"})
	wantCode(t, err, errs.InvalidInput, "rename to an invalid name")
	ro, err := h.Rename(ctx, opsorgcmd.RenameIn{Old: "agency", New: "northwind"})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	cfg = readConfig(t, mira)
	if ro.RepoOrgs != 1 || !ro.IDRecorded || !strings.Contains(cfg, "name: northwind") || strings.Contains(cfg, "agency}") ||
		!strings.Contains(cfg, "{org: northwind") || !strings.Contains(cfg, "id: prj_") {
		t.Fatalf("rename: %+v\n%s", ro, cfg)
	}
	_, err = h.Rename(ctx, opsorgcmd.RenameIn{Old: "agency", New: "x"})
	wantCode(t, err, errs.NotFound, "the old name is gone")
}

// TestRenameOrgNode: the config-tree half of rename, over the shapes a
// config.yaml holds (short and mapping repo_orgs, folder targets, another
// org's references, id recorded / equal / different, name taken).
func TestRenameOrgNode(t *testing.T) {
	const doc = `orgs:
  - name: a
    defaults: {client_repos: {memory: a/notes, task: a, rule: repo}}
  - name: b
repo_orgs:
  prj_1: a
  prj_2: {org: a, remotes: [github.com/acme/app]}
  prj_3: b
`
	cases := []struct {
		name, from, to, id, preID string
		code                      errs.Code
		defaults, repoOrgs        int
		recorded                  bool
	}{
		{name: "moves own targets and choices", from: "a", to: "c", defaults: 2, repoOrgs: 2},
		{name: "records the id", from: "a", to: "c", id: "prj_x", defaults: 2, repoOrgs: 2, recorded: true},
		{name: "same id kept", from: "a", to: "c", id: "prj_x", preID: "prj_x", defaults: 2, repoOrgs: 2},
		{name: "another org's id", from: "a", to: "c", id: "prj_x", preID: "prj_y", code: errs.InvalidInput},
		{name: "name taken", from: "a", to: "b", code: errs.InvalidInput},
		{name: "unknown entry", from: "z", to: "c", code: errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			d := doc
			if c.preID != "" {
				d = strings.Replace(d, "  - name: a\n", "  - name: a\n    id: "+c.preID+"\n", 1)
			}
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath(home), []byte(d), 0o600); err != nil {
				t.Fatal(err)
			}
			var out opsorgcmd.RenameOut
			b, err := orgConfigWith(home, func(root *yaml.Node) error { return renameOrgNode(root, c.from, c.to, c.id, &out) })
			if c.code != "" {
				wantCode(t, err, c.code, c.name)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			if out.Defaults != c.defaults || out.RepoOrgs != c.repoOrgs || out.IDRecorded != c.recorded {
				t.Fatalf("counts %+v\n%s", out, s)
			}
			for _, want := range []string{"memory: c/notes", "task: c", "rule: repo", "prj_1: c", "org: c", "prj_3: b"} {
				if !strings.Contains(s, want) {
					t.Fatalf("missing %q:\n%s", want, s)
				}
			}
		})
	}
}
