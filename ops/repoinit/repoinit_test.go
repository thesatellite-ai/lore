package repoinit

// Decision tests of repo.init over a fake Host: refuse when local state
// existed, adopt a clone's project, mint a fresh one, refuse a second
// repo id, undo the rooted folder on failure, keep warnings non-fatal,
// and the registry entry running the same Run as the CLI word.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
)

// fakeHost records what Run asked for.
type fakeHost struct {
	local      bool
	hasProject bool
	projectErr error
	shared     rowfs.RepoMeta
	hasShared  bool
	sharedErr  error
	ownMeta    bool
	createErr  error
	gitignore  error
	toolErr    error
	pid        ids.ID[ids.Project]
	linked     ids.ID[ids.Project] // LinkedProject's answer (zero: no link)
	createdID  ids.ID[ids.Project] // the id CreateProject was asked for

	undone, primed, inherited bool
	created                   map[string]value.Value
	targets                   []string
	setupTools                SetupTools
}

func (f *fakeHost) WorkDir() string     { return "/work/acme-hub" }
func (f *fakeHost) HadLocalState() bool { return f.local }
func (f *fakeHost) SharedRepoMeta(context.Context) (rowfs.RepoMeta, bool, error) {
	return f.shared, f.hasShared, f.sharedErr
}
func (f *fakeHost) OwnRepoMeta(context.Context) bool { return f.ownMeta }
func (f *fakeHost) RootAt(context.Context) (func(), error) {
	return func() { f.undone = true }, nil
}
func (f *fakeHost) Project(context.Context) (ids.ID[ids.Project], error) {
	if f.projectErr != nil {
		return ids.ID[ids.Project]{}, f.projectErr
	}
	if !f.hasProject {
		return ids.ID[ids.Project]{}, errs.New(errs.NotFound, "no project")
	}
	return f.pid, nil
}
func (f *fakeHost) DefaultName(context.Context) string { return "acme-hub" }
func (f *fakeHost) OriginURL(context.Context) string   { return "git@github.com:agency/acme-hub.git" }
func (f *fakeHost) CreateProject(_ context.Context, id ids.ID[ids.Project], fields map[string]value.Value) (string, ids.ID[ids.Project], error) {
	if f.createErr != nil {
		return "", ids.ID[ids.Project]{}, f.createErr
	}
	f.created, f.createdID = fields, id
	if !id.IsZero() {
		return id.String(), id, nil
	}
	return f.pid.String(), f.pid, nil
}
func (f *fakeHost) LinkedProject(context.Context) (ids.ID[ids.Project], string, bool) {
	return f.linked, "github.com/agency/acme-hub", !f.linked.IsZero()
}
func (f *fakeHost) SeedRepoMeta(context.Context, ids.ID[ids.Project]) error { return nil }
func (f *fakeHost) InheritRepoID(context.Context, rowfs.RepoMeta) error {
	f.inherited = true
	return nil
}
func (f *fakeHost) EnsureRepoIdentity(context.Context) (string, error) { return "rep_test", nil }
func (f *fakeHost) EnsureGitignore(context.Context) error              { return f.gitignore }
func (f *fakeHost) Prime(context.Context)                              { f.primed = true }
func (f *fakeHost) WireToolFiles(_ context.Context, targets []string) (string, error) {
	f.targets = targets
	return "  wired AGENTS.md\n", f.toolErr
}

func (f *fakeHost) SetupTools(context.Context) SetupTools { return f.setupTools }

func newFake(t *testing.T) *fakeHost {
	t.Helper()
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeHost{pid: pid}
}

func TestRunDecisions(t *testing.T) {
	ctx := context.Background()
	t.Run("already initialised", func(t *testing.T) {
		f := newFake(t)
		f.local, f.hasProject = true, true
		_, err := Run(ctx, f, InitIn{})
		if !errs.Has(err, errs.AlreadyInitialized) || !strings.Contains(err.Error(), f.pid.String()) {
			t.Fatalf("err = %v, want E_ALREADY_INITIALIZED naming %s", err, f.pid)
		}
		if !f.undone || f.primed {
			t.Fatalf("undone %v primed %v: a refusal must undo and not prime", f.undone, f.primed)
		}
	})
	t.Run("already initialised with --target names render --add-target", func(t *testing.T) {
		f := newFake(t)
		f.local, f.hasProject = true, true
		_, err := Run(ctx, f, InitIn{Target: []string{"cursor", " "}})
		var e *errs.Error
		if !errs.Has(err, errs.AlreadyInitialized) || !errors.As(err, &e) || !strings.Contains(e.Hint, "lore render --add-target cursor") {
			t.Fatalf("err = %v, want a hint naming lore render --add-target cursor", err)
		}
	})
	t.Run("a sub-project names the repo's other projects", func(t *testing.T) {
		f := newFake(t)
		out, err := Run(ctx, monoFake{f, []string{"."}}, InitIn{Name: "acme-web"})
		if err != nil || !slices.Equal(out.OtherProjects, []string{"."}) || !strings.Contains(out.Body(), "this repo already holds a lore project in the repo root: acme-web is a separate project") {
			t.Fatalf("out %+v err %v body %q", out, err, out.Body())
		}
	})
	t.Run("adopt a clone", func(t *testing.T) {
		f := newFake(t)
		f.hasProject = true
		out, err := Run(ctx, f, InitIn{Name: "ignored"})
		if err != nil || !out.Adopted || out.ProjectID != f.pid.String() || f.created != nil || f.undone || !f.primed {
			t.Fatalf("out %+v err %v created %v undone %v primed %v", out, err, f.created, f.undone, f.primed)
		}
		want := "✓ adopted the shared lore project at /work/acme-hub\n    project_id: " + f.pid.String() + "\n"
		if out.Body() != want || f.targets != nil {
			t.Fatalf("body %q targets %q, want %q and no tool file touched", out.Body(), f.targets, want)
		}
	})
	// A-9: adopting under a setup that chose tools wires them (the host
	// resolves nil targets: the repo's row, else the setup's tools), and
	// --target is wired as given; the lines follow the adopted line.
	t.Run("adopt wires the setup's tools and --target", func(t *testing.T) {
		f := newFake(t)
		f.hasProject = true
		f.setupTools = SetupTools{Setup: "test1", Profiles: []string{"codex"}, Files: []string{"AGENTS.md"}}
		out, err := Run(ctx, f, InitIn{})
		if err != nil || !out.Adopted || f.targets != nil || !strings.HasSuffix(out.Body(), "  wired AGENTS.md\n") {
			t.Fatalf("setup: out %+v err %v targets %q", out, err, f.targets)
		}
		g := newFake(t)
		g.hasProject = true
		if _, err := Run(ctx, g, InitIn{Target: []string{"cursor"}}); err != nil || len(g.targets) != 1 || g.targets[0] != "cursor" {
			t.Fatalf("--target: err %v targets %q", err, g.targets)
		}
	})
	t.Run("fresh, flag name lower-cased, origin kept, shared id inherited", func(t *testing.T) {
		f := newFake(t)
		f.hasShared = true
		out, err := Run(ctx, f, InitIn{Name: "  Acme-Hub ", Target: []string{"codex"}})
		if err != nil {
			t.Fatal(err)
		}
		if out.Name != "acme-hub" || out.RepoID != "rep_test" || !f.inherited || !f.primed || f.undone {
			t.Fatalf("out %+v inherited %v primed %v undone %v", out, f.inherited, f.primed, f.undone)
		}
		if n := f.created[FieldName].String(); n != "acme-hub" {
			t.Fatalf("name field %q", n)
		}
		if o := f.created[FieldOrigin].String(); o != "git@github.com:agency/acme-hub.git" {
			t.Fatalf("origin field %q", o)
		}
		if len(f.targets) != 1 || f.targets[0] != "codex" {
			t.Fatalf("targets %v", f.targets)
		}
		want := "lore initialized\n    project_id: " + f.pid.String() + "\n    name:       acme-hub\n    repo_id:    rep_test\n\n  wired AGENTS.md\n" + NextHint + "\n"
		if out.Body() != want {
			t.Fatalf("body %q\nwant %q", out.Body(), want)
		}
	})
	t.Run("two repo ids refuse a new project", func(t *testing.T) {
		f := newFake(t)
		f.sharedErr = errs.New(errs.AmbiguousID, "two ids")
		if _, err := Run(ctx, f, InitIn{}); !errs.Has(err, errs.AmbiguousID) || f.undone {
			t.Fatalf("err %v undone %v: refused before rooting", err, f.undone)
		}
		f.ownMeta = true // wd has its own _meta.json: reach the project check, still refuse a new one
		if _, err := Run(ctx, f, InitIn{}); !errs.Has(err, errs.AmbiguousID) || !f.undone || f.created != nil {
			t.Fatalf("err %v undone %v created %v", err, f.undone, f.created)
		}
	})
	t.Run("a failed write undoes the rooted folder", func(t *testing.T) {
		f := newFake(t)
		f.createErr = errs.New(errs.Internal, "disk")
		if _, err := Run(ctx, f, InitIn{}); err == nil || !f.undone {
			t.Fatalf("err %v undone %v", err, f.undone)
		}
	})
	t.Run("a broken project read is returned as is", func(t *testing.T) {
		f := newFake(t)
		f.projectErr = errs.New(errs.Internal, "broken store")
		if _, err := Run(ctx, f, InitIn{}); !errs.Has(err, errs.Internal) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("warnings never fail init", func(t *testing.T) {
		f := newFake(t)
		f.gitignore, f.toolErr = errors.New("read-only"), errors.New("no tools")
		out, err := Run(ctx, f, InitIn{})
		if err != nil || len(out.Warnings) != 2 {
			t.Fatalf("out %+v err %v", out, err)
		}
		if got := out.WarningLines(); got != "WARN: could not update .gitignore: read-only\nWARN: could not wire AI tool files: no tools\n" {
			t.Fatalf("warning lines %q", got)
		}
		if !strings.HasSuffix(out.Text(), out.WarningLines()) || !strings.HasPrefix(out.Text(), out.Body()) {
			t.Fatalf("text %q", out.Text())
		}
	})
}

// fakeEnv hands the registry entry the same fake host.
type fakeEnv struct{ h Host }

func (e fakeEnv) RepoInitHost(context.Context) (Host, error) { return e.h, nil }

// TestOpMatchesRun: the registry entry decodes the CLI flags' JSON names
// and returns what Run returns over the same host.
func TestOpMatchesRun(t *testing.T) {
	ctx := context.Background()
	viaRun := newFake(t)
	want, err := Run(ctx, viaRun, InitIn{Name: "mira", Target: []string{"claude_code"}})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := op.NewRegistry(Ops(fakeEnv{h: &fakeHost{pid: viaRun.pid}})...)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup(OpRepoInit)
	if !ok {
		t.Fatalf("%s not registered", OpRepoInit)
	}
	if !e.Spec().Local {
		t.Fatal("repo.init must be Local: it runs before any project exists")
	}
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{"name":"mira","target":["claude_code"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Invoke(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	if string(got) != string(wantJSON) {
		t.Fatalf("op %s\nrun %s", got, wantJSON)
	}
}

// TestRunToolsFromSetup: with no --target the applying setup tools are
// wired and named in the output; --target always wins; no setup tools
// leaves the targets empty (WireToolFiles default).
func TestRunToolsFromSetup(t *testing.T) {
	ctx := context.Background()
	st := SetupTools{Setup: "test1", Profiles: []string{"claude_code"}, Files: []string{"CLAUDE.md"}}

	f := newFake(t)
	f.setupTools = st
	out, err := Run(ctx, f, InitIn{})
	if err != nil || strings.Join(f.targets, ",") != "claude_code" || out.ToolsFromSetup != "test1" {
		t.Fatalf("setup tools: targets %v out %+v err %v", f.targets, out, err)
	}
	if !strings.Contains(out.Body(), "tool files from setup test1: CLAUDE.md") {
		t.Fatalf("body does not name the setup:\n%s", out.Body())
	}

	f = newFake(t)
	f.setupTools = st
	out, err = Run(ctx, f, InitIn{Target: []string{"codex"}})
	if err != nil || strings.Join(f.targets, ",") != "codex" || out.ToolsFromSetup != "" || strings.Contains(out.Body(), "from setup") {
		t.Fatalf("--target: targets %v out %+v err %v", f.targets, out, err)
	}

	f = newFake(t)
	out, err = Run(ctx, f, InitIn{})
	if err != nil || len(f.targets) != 0 || out.ToolsFromSetup != "" {
		t.Fatalf("no setup: targets %v out %+v err %v", f.targets, out, err)
	}
}

// TestRunRefusesControlChars: a project name holding a control character
// (C0, DEL, C1) is E_INVALID_INPUT naming the character, nothing created;
// unicode letters, spaces and long names are kept (no length cap).
func TestRunRefusesControlChars(t *testing.T) {
	ctx := context.Background()
	refused := map[string]string{
		"acme\nhub": "U+000A (newline)", "acme\thub": "U+0009 (tab)", "acme\rhub": "U+000D (carriage return)",
		"acme\x00hub": "U+0000 (NUL)", "acme\x1b[31mhub": "U+001B (escape)", "acme\x07": "U+0007", "a\x7fb": "U+007F (delete)",
		"a\u0085b": "U+0085", "a\x1fb": "U+001F",
	}
	for name, want := range refused {
		f := newFake(t)
		_, err := Run(ctx, f, InitIn{Name: name})
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), want) || errs.EnvelopeOf(err).Error.Hint == "" {
			t.Errorf("name %q: E_INVALID_INPUT naming %s with a hint, got %v", name, want, err)
		}
		if f.created != nil || f.primed {
			t.Errorf("name %q: nothing created (created %v primed %v)", name, f.created, f.primed)
		}
	}
	for _, name := range []string{"acme hub", "ácme-日本", strings.Repeat("a", 4096), "a/b", "a\u200bb"} {
		f := newFake(t)
		out, err := Run(ctx, f, InitIn{Name: name})
		if err != nil || out.Name != strings.ToLower(name) {
			t.Errorf("name %q: kept, got %+v %v", name, out, err)
		}
	}
}

// TestRunTakesTheLinkedProjectID (B1-18): a repo the org store already
// links by its remote gets that link's project id, and init says so; with
// no link a new id is minted and nothing is said.
func TestRunTakesTheLinkedProjectID(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	linked, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	f.linked = linked
	out, err := Run(ctx, f, InitIn{})
	if err != nil {
		t.Fatal(err)
	}
	if f.createdID != linked || out.ProjectID != linked.String() {
		t.Fatalf("project %s (asked for %s), want the linked %s", out.ProjectID, f.createdID, linked)
	}
	if !strings.Contains(out.Body(), "already links github.com/agency/acme-hub") {
		t.Fatalf("init does not say it took the link's id:\n%s", out.Body())
	}
	f = newFake(t)
	out, err = Run(ctx, f, InitIn{})
	if err != nil {
		t.Fatal(err)
	}
	if !f.createdID.IsZero() || out.ProjectID != f.pid.String() || strings.Contains(out.Body(), "already links") {
		t.Fatalf("no link: project %s, asked for %s:\n%s", out.ProjectID, f.createdID, out.Body())
	}
}

// monoFake is fakeHost with the optional MonorepoHost part.
type monoFake struct {
	*fakeHost
	others []string
}

func (m monoFake) OtherProjectFolders(context.Context) []string { return m.others }
