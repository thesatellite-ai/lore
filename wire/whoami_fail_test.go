package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

const testMachine = "mch_0cd630f071ab154c3b6a0f44e9366337ec0fb87c4e688d19fc5e84f1bf8dd980"

// lineOf is the value of the whoami row labelled label ("", false when
// the row is absent).
func lineOf(lines []string, label string) (string, bool) {
	for _, l := range lines {
		if strings.HasPrefix(l, label+" ") {
			return strings.TrimSpace(strings.TrimPrefix(l, label)), true
		}
	}
	return "", false
}

// TestWhoamiLinesFailMatrix: the human lines for a refused org, an
// identity error and a success, and the machine row's three shapes.
func TestWhoamiLinesFailMatrix(t *testing.T) {
	t.Parallel()
	ok := identity.Resolved{DisplayName: "Alice", StableKey: "human:alice@agency.example"}
	own := orgresolve.Result{Org: &orgconf.Org{Name: "agency"}, Role: orgresolve.RoleOwn, MatchedBy: "own pattern"}
	refused := &orgresolve.Error{Kind: orgresolve.KindAmbiguous, Message: "the patterns of 2 org entries match this repo (one, two)"}
	cases := []struct {
		name             string
		res              orgresolve.Result
		resolveErr       error
		id               identity.Resolved
		idErr            error
		machine          string
		org, ident, mrow string
		repo             bool
	}{
		{"success", own, nil, ok, nil, testMachine, "agency  (matched by: own pattern)", "Alice <human:alice@agency.example>", "mch_0cd630f0", true},
		{"org refused", orgresolve.Result{}, refused, identity.Resolved{}, nil, testMachine, "refused  (the patterns of 2 org entries", "(not resolved: the org is refused above)", "mch_0cd630f0", false},
		{"identity error", own, nil, identity.Resolved{}, errors.New("no identity: set one with lore identity set"), "", "agency", "missing  no identity: set one", "(no OS machine id readable here)", true},
		{"unparsable machine id shown as is", own, nil, ok, nil, "mch_short", "agency", "Alice", "mch_short", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &orgcfg.Situation{Config: &orgconf.Config{}, Result: c.res, ResolveErr: c.resolveErr, HasConfig: true}
			lines := whoamiLines(s, c.id, c.idErr, false, whoamiRedactor{}, nil, c.machine, "")
			check := func(label, want string) {
				t.Helper()
				got, present := lineOf(lines, label)
				if !present || !strings.HasPrefix(got, want) {
					t.Fatalf("%s row %q (present %v), want prefix %q\n%s", label, got, present, want, strings.Join(lines, "\n"))
				}
			}
			check("org", c.org)
			check("identity", c.ident)
			check("machine", c.mrow)
			if _, present := lineOf(lines, "repo"); present != c.repo {
				t.Fatalf("repo row present %v, want %v", present, c.repo)
			}
			for _, label := range []string{"project", "remotes", "links", "personal"} {
				if _, present := lineOf(lines, label); !present {
					t.Fatalf("the %s row is missing:\n%s", label, strings.Join(lines, "\n"))
				}
			}
		})
	}
}

// whoamiRepo is a git repo whose origin matches both org entries' own:
// patterns of ambiguousConfig, and a lore home holding that config.
func whoamiRepo(t *testing.T, config string) (repo, home string) {
	t.Helper()
	t.Setenv("LORE_ORG", "")
	t.Setenv("LORE_HUMAN", "1")
	repo = gitRepo(t)
	gitIn(t, repo, nil, "remote", "add", "origin", "git@github.com:acme/app.git")
	home = filepath.Join(t.TempDir(), ".lore")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(configPath(home), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo, home
}

const ambiguousConfig = `orgs:
  - name: one
    identity: {key: "human:a@one.example", name: A}
    own: ["github.com/acme/*"]
  - name: two
    identity: {key: "human:a@two.example", name: A}
    own: ["github.com/acme/*"]
`

// TestWhoamiPrintsBeforeFailing (P3-ORG-G-3): with an org that cannot be
// resolved, whoami still returns every line and the JSON data, with
// Data.Error and Out.Fail carrying the refusal (E_ORG_AMBIGUOUS); the op
// returns no error itself (the CLI prints, then fails). LORE_ORG picking
// one entry is a plain success.
func TestWhoamiPrintsBeforeFailing(t *testing.T) {
	repo, home := whoamiRepo(t, ambiguousConfig)
	o := StandaloneOptions(repo, home, repo, "test")
	h, isHost := o.Whoami.(*standaloneWhoami)
	if !isHost {
		t.Fatal("StandaloneOptions installs no standaloneWhoami")
	}
	out, err := opswhoami.Show(context.Background(), h, opswhoami.ShowIn{})
	if err != nil {
		t.Fatalf("whoami returned the error instead of printing first: %v", err)
	}
	if errs.CodeOf(out.Failure()) != errs.OrgAmbiguous {
		t.Fatalf("Out.Fail = %v, want E_ORG_AMBIGUOUS", out.Failure())
	}
	if v, _ := lineOf(out.Lines, "org"); !strings.HasPrefix(v, "refused") {
		t.Fatalf("org row %q:\n%s", v, strings.Join(out.Lines, "\n"))
	}
	if out.Data.Error == "" || strings.Contains(out.Data.Error, "[E_") || len(out.Data.Remotes) != 1 || len(out.Data.Trace) == 0 {
		t.Fatalf("--json data: %+v", out.Data)
	}

	t.Setenv("LORE_ORG", "one")
	o = StandaloneOptions(repo, home, repo, "test")
	out, err = opswhoami.Show(context.Background(), o.Whoami, opswhoami.ShowIn{})
	if err != nil || out.Failure() != nil || out.Data.Error != "" || out.Data.Org != "one" {
		t.Fatalf("LORE_ORG=one: %+v fail=%v err=%v", out.Data, out.Failure(), err)
	}
}

// TestWhoamiMachineLabel: --machine-label through cmd/lore's whoami host
// runs machine.label on the bound App: refused with no own store, set,
// unchanged on a repeat, cleared with "", an invalid label refused, and
// --explain together with it refused before anything runs.
func TestWhoamiMachineLabel(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	repo, home := whoamiRepo(t, "")
	opts := StandaloneOptions(repo, home, repo, "test")
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	withProject(t, app)
	h, isHost := opts.Whoami.(*standaloneWhoami)
	if !isHost {
		t.Fatal("no standaloneWhoami")
	}
	ctx := context.Background()
	label := func(v string) opswhoami.ShowIn {
		in := opswhoami.ShowIn{}
		in.MachineLabel.Set, in.MachineLabel.Value = true, v
		return in
	}
	if _, err = opswhoami.Show(ctx, h, label("laptop")); !errs.Has(err, errs.Refused) {
		t.Fatalf("no own store: %v, want E_REFUSED", err)
	}
	store := filepath.Join(t.TempDir(), "mine")
	if err = os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = app.InitStore(ctx, opsstores.InitIn{Name: "mine", URL: fileuri.URL(store, "")}); err != nil {
		t.Fatalf("store init: %v", err)
	}
	// The next command is a new process: a fresh App over the new stores.yaml.
	opts = StandaloneOptions(repo, home, repo, "test")
	app2, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app2.Close() }()
	if h, isHost = opts.Whoami.(*standaloneWhoami); !isHost {
		t.Fatal("no standaloneWhoami")
	}
	steps := []struct {
		value, want string
		changed     bool
	}{
		{"laptop one", `labelled "laptop one"`, true},
		{"laptop one", "unchanged", false},
		{"", "label cleared", true},
	}
	for _, s := range steps {
		out, serr := opswhoami.Show(ctx, h, label(s.value))
		if serr != nil {
			t.Fatalf("label %q: %v", s.value, serr)
		}
		if out.Kind != opswhoami.KindMachineLabel || out.Label.Changed != s.changed || out.Label.Label != s.value || out.Label.Store != "mine" ||
			!strings.HasPrefix(out.Label.ID, "mch_") || !strings.Contains(out.Text(), s.want) {
			t.Fatalf("label %q: %+v %q", s.value, out.Label, out.Text())
		}
	}
	if _, err = opswhoami.Show(ctx, h, label("two\nlines")); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("a label with a newline: %v, want E_INVALID_INPUT", err)
	}
	in := label("x")
	in.Explain = true
	if _, err = opswhoami.Show(ctx, h, in); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--machine-label with --explain: %v", err)
	}
}
