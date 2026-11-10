package wire

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// activeConfig is the ONBOARDING 3.2 example machine: a solo default, two
// member setups of one org (one more specific), and a second org.
const activeConfig = `orgs:
  - name: agency
    identity: {key: "human:mira@agency.example"}
  - name: acme-hub
    identity: {key: "human:mira@acme.example"}
setups:
  - name: personal
    kind: solo
    default: true
    folders: [~/www/mira/**]
    identity: {key: "human:mira@personal.example", name: Mira}
    personal_store: none
  - name: agency
    kind: member
    org: agency
    folders: [~/www/projects/**]
    github: {user: mira-agency, host_alias: github-agency}
  - name: client-acme
    kind: member
    org: agency
    folders: [~/www/projects/clients/acme/**]
    github: {user: mira-agency, ssh_key: ~/.ssh/id_acme.pub}
    personal_store: file:///home/mira/acme-notes
  - name: hub
    kind: member
    org: acme-hub
    folders: [~/work/**]
  - name: hub2
    kind: member
    org: acme-hub
    folders: [~/work/**]
`

func TestResolveSetupMatrix(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, activeConfig+"repo_setups:\n  rep_pinned: hub\n")
	empty := writeSetupsConfig(t, "orgs: []\n")
	org := func(name string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return name, nil }
	}
	cases := []struct {
		name   string
		home   string
		flag   string
		env    string
		dir    string
		top    string
		repoID string
		orgOf  func(context.Context) (string, error)
		want   string
		why    string
		code   errs.Code
	}{
		{name: "no setups: none, no git", home: empty, dir: "/home/mira/www/projects/x", why: opssetup.WhyNone},
		{name: "flag wins over folder", flag: "hub", dir: "/home/mira/www/projects/x", want: "hub", why: opssetup.WhyFlag},
		{name: "flag naming nothing", flag: "gone", dir: "/x", code: errs.SetupNotFound},
		{name: "env wins over folder", env: "agency", dir: "/home/mira/www/mira/x", want: "agency", why: opssetup.WhyEnv},
		{name: "env naming nothing", env: "gone", dir: "/x", code: errs.SetupNotFound},
		{name: "flag beats env", flag: "personal", env: "agency", dir: "/x", want: "personal", why: opssetup.WhyFlag},
		{name: "repo pin beats folder", dir: "/home/mira/www/projects/x", repoID: "rep_pinned", want: "hub", why: opssetup.WhyRepoPin},
		{name: "folder: most specific wins", dir: "/home/mira/www/projects/clients/acme/web", want: "client-acme", why: opssetup.WhyFolder},
		{name: "folder: the git toplevel is matched, not the subfolder", dir: "/elsewhere/sub", top: "/home/mira/www/projects/clients/acme/web", want: "client-acme", why: opssetup.WhyFolder},
		{name: "folder: broader glob", dir: "/home/mira/www/projects/tomas", want: "agency", why: opssetup.WhyFolder},
		{name: "folder tie refused", dir: "/home/mira/work/x", code: errs.SetupAmbiguous},
		{name: "org with no setup falls to default", dir: "/srv/x", orgOf: org("acme-hub_none"), want: "personal", why: opssetup.WhyDefault},
		{name: "org match: two setups of one org refused", dir: "/srv/x", orgOf: org("agency"), code: errs.SetupAmbiguous},
		{name: "default when nothing matches", dir: "/srv/x", want: "personal", why: opssetup.WhyDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.home
			if h == "" {
				h = home
			}
			gitRan := false
			q := SetupQuery{Home: h, Dir: tc.dir, Flag: tc.flag, UserHome: "/home/mira",
				Getenv:   func(k string) string { return map[string]string{opssetup.EnvSetup: tc.env}[k] },
				TopLevel: func(context.Context, string) string { gitRan = true; return tc.top },
				RepoID:   func(string) string { return tc.repoID },
				Resolve:  func(p string) (string, error) { return "", errors.New("no links") },
				OrgOf:    tc.orgOf}
			res, err := ResolveSetup(context.Background(), q)
			if tc.code != "" {
				if errs.CodeOf(err) != tc.code {
					t.Fatalf("want %s, got %v", tc.code, err)
				}
				if len(res.Steps) == 0 || res.Steps[len(res.Steps)-1].Outcome != SetupStepRefused {
					t.Fatalf("the trace must end with the refusal: %+v", res.Steps)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Active.Name != tc.want || res.Active.Why != tc.why {
				t.Fatalf("got %s/%s, want %s/%s (steps %+v)", res.Active.Name, res.Active.Why, tc.want, tc.why, res.Steps)
			}
			if h == empty && gitRan {
				t.Fatal("no setups must not run git")
			}
			if len(res.Steps) != 7 {
				t.Fatalf("every step is traced, got %d: %+v", len(res.Steps), res.Steps)
			}
		})
	}
}

// TestResolveSetupOrgMatch: step 5 picks the one setup of the repo's org.
func TestResolveSetupOrgMatch(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, `orgs:
  - name: agency
    identity: {key: "human:mira@agency.example"}
setups:
  - name: agency
    kind: member
    org: agency
    folders: [~/www/projects/**]
`)
	q := SetupQuery{Home: home, Dir: "/srv/x", UserHome: "/home/mira", Getenv: func(string) string { return "" },
		TopLevel: func(context.Context, string) string { return "" }, RepoID: func(string) string { return "" },
		OrgOf: func(context.Context) (string, error) { return "agency", nil }}
	res, err := ResolveSetup(context.Background(), q)
	if err != nil || res.Active.Name != "agency" || res.Active.Why != opssetup.WhyOrg {
		t.Fatalf("got %+v %v", res.Active, err)
	}
	if h := setupHint(res); h.Org != "" {
		t.Fatalf("a setup chosen by its org adds no hint, got %+v", h)
	}
}

func TestSetupHintKinds(t *testing.T) {
	t.Parallel()
	member := SetupResolution{Active: opssetup.ActiveSetup{Name: "agency", Why: opssetup.WhyFolder, Detail: "~/www/projects/**"},
		Entry: onboard.SetupEntry{Name: "agency", Kind: onboard.KindMember, Org: "agency"}}
	if h := setupHint(member); h.Org != "agency" || h.Why != "folder ~/www/projects/**" {
		t.Fatalf("member hint %+v", h)
	}
	solo := SetupResolution{Active: opssetup.ActiveSetup{Name: "personal", Why: opssetup.WhyDefault}, Entry: onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo}}
	if h := setupHint(solo); h.Org != "" {
		t.Fatalf("solo gives no hint, got %+v", h)
	}
}

// TestRouteFor is Q5: an alias rewrites github.com per call, a key alone
// is GIT_SSH_COMMAND, github.com itself or nothing adds nothing, and a
// value that could be read as an option adds nothing.
func TestRouteFor(t *testing.T) {
	t.Parallel()
	args, env := routeFor(onboard.SetupGitHub{HostAlias: "github-agency"}, "/home/mira")
	if len(env) != 0 || !strings.Contains(strings.Join(args, " "), "url.git@github-agency:.insteadOf=git@github.com:") {
		t.Fatalf("alias: %v %v", args, env)
	}
	args, env = routeFor(onboard.SetupGitHub{SSHKey: "~/.ssh/id_acme.pub"}, "/home/mira")
	if len(args) != 0 || len(env) != 1 || !strings.HasPrefix(env[0], envGitSSHCommand+"=") || !strings.Contains(env[0], "/home/mira/.ssh/id_acme.pub") || !strings.Contains(env[0], "IdentitiesOnly=yes") {
		t.Fatalf("key: %v %v", args, env)
	}
	args, env = routeFor(onboard.SetupGitHub{SSHKey: "~/.ssh/main-key"}, "/home/mira")
	if len(env) != 1 || !strings.Contains(env[0], "-i /home/mira/.ssh/main-key ") {
		t.Fatalf("private key: %v %v", args, env)
	}
	// https: the store's git calls go over https with the account's gh
	// login (credential helper per call), whatever alias was left behind.
	args, env = routeFor(onboard.SetupGitHub{User: "octo-user", Route: onboard.RouteHTTPS, HostAlias: "stale-alias"}, "/home/mira")
	joined := strings.Join(args, " ")
	if len(env) != 0 || !strings.Contains(joined, "url.https://github.com/.insteadOf=git@github.com:") || !strings.Contains(joined, "gh auth token --hostname github.com --user octo-user") {
		t.Fatalf("https: %v %v", args, env)
	}
	for _, g := range []onboard.SetupGitHub{{}, {HostAlias: "github.com"}, {HostAlias: "-oProxyCommand=x"}, {Route: onboard.RouteHTTPS}, {Route: onboard.RouteHTTPS, User: "a;b"}} {
		if a, e := routeFor(g, "/home/mira"); len(a)+len(e) != 0 {
			t.Fatalf("%+v must add nothing, got %v %v", g, a, e)
		}
	}
}

// TestPersonalStoreOverride is 3.4's `me`: none refuses --to me, a url
// replaces personal.store, inherit / no setup keep it.
func TestPersonalStoreOverride(t *testing.T) {
	t.Setenv("HOME", "/home/mira")
	t.Setenv(opssetup.EnvSetup, "")
	home := writeSetupsConfig(t, activeConfig)
	mc := machineConfig{}
	mc.Personal.Store = "file:///home/mira/notes"
	ctx := context.Background()
	in := func(name string) context.Context { return op.WithSetup(ctx, name) }

	err := checkPersonalTarget(in("personal"), home, "/srv/x", "me")
	if errs.CodeOf(err) != errs.SetupNoPersonalStore || !strings.Contains(err.Error(), "setup personal has no personal store") {
		t.Fatalf("--to me under personal_store: none: %v", err)
	}
	if err := checkPersonalTarget(in("personal"), home, "/srv/x", "me/drafts"); errs.CodeOf(err) != errs.SetupNoPersonalStore {
		t.Fatalf("--to me/<folder>: %v", err)
	}
	if err := checkPersonalTarget(in("personal"), home, "/srv/x", "repo"); err != nil {
		t.Fatalf("--to repo is untouched: %v", err)
	}
	if err := checkPersonalTarget(in("agency"), home, "/srv/x", "me"); err != nil {
		t.Fatalf("inherit allows me: %v", err)
	}
	if got := effectivePersonalURL(in("personal"), home, "/srv/x", mc); got != "" {
		t.Fatalf("none detaches me, got %q", got)
	}
	if got := effectivePersonalURL(in("client-acme"), home, "/srv/x", mc); got != "file:///home/mira/acme-notes" {
		t.Fatalf("url replaces personal.store, got %q", got)
	}
	if got := effectivePersonalURL(in("agency"), home, "/srv/x", mc); got != "file:///home/mira/notes" {
		t.Fatalf("inherit keeps personal.store, got %q", got)
	}
}

// TestSetupMismatchErr: the write gate's refusal names the stronger source
// (LORE_ORG here) and carries E_SETUP_ORG_MISMATCH.
func TestSetupMismatchErr(t *testing.T) {
	home := writeSetupsConfig(t, activeConfig)
	t.Setenv("LORE_ORG", "acme-hub")
	err := setupMismatchErr(context.Background(), home, t.TempDir(),
		orgresolve.SetupHint{Name: "agency", Org: "agency", Why: "folder ~/www/projects/**"}, "acme-hub")
	if errs.CodeOf(err) != errs.SetupOrgMismatch || !strings.Contains(err.Error(), "this repo is org acme-hub (from LORE_ORG) but setup agency (folder ~/www/projects/**) is org agency") {
		t.Fatalf("got %v", err)
	}
}

// TestWhoamiSetupRefusal: a --setup (or LORE_SETUP) naming no setup makes
// whoami print its setup row "refused" and then fail E_SETUP_NOT_FOUND
// (ONB 7.2), as whoami does for an org refusal; a known name is no error.
func TestWhoamiSetupRefusal(t *testing.T) {
	t.Setenv(opssetup.EnvSetup, "")
	home := writeSetupsConfig(t, activeConfig)
	opts := Options{Home: home, WorkDir: t.TempDir()}
	_, lines, err := whoamiSetup(op.WithSetup(context.Background(), "nosuch"), opts, false, []string{"org x"})
	if !errs.Has(err, errs.SetupNotFound) {
		t.Fatalf("err = %v, want E_SETUP_NOT_FOUND", err)
	}
	if len(lines) < 2 || !strings.Contains(lines[1], "refused") {
		t.Errorf("lines = %q, want a refused setup row", lines)
	}
	t.Setenv(opssetup.EnvSetup, "nosuch")
	if _, _, err := whoamiSetup(context.Background(), opts, false, nil); !errs.Has(err, errs.SetupNotFound) {
		t.Errorf("LORE_SETUP=nosuch: err = %v, want E_SETUP_NOT_FOUND", err)
	}
	if s, _, err := whoamiSetup(op.WithSetup(context.Background(), "hub"), opts, false, nil); err != nil || s.Name != "hub" {
		t.Errorf("--setup hub: %+v, %v", s, err)
	}
}

// TestOrgFolderStoreFresh (A11 bug 3): the join's roster store is read
// from the orgs[] entry as config.yaml holds it now (org.import wrote it
// in the same apply), not from the process's org resolution, which ran
// before the entry existed.
func TestOrgFolderStoreFresh(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, `orgs:
  - name: agency
    identity: {key: "human:mira@agency.example"}
    stores:
      - url: git@github.com:agency/notes
      - url: git@github.com:agency/team
        org_folder: org
  - name: acme-hub
    stores:
      - url: git@github.com:acme/notes
`)
	got, ok, err := orgFolderStore(home, "agency")
	if err != nil || !ok || got.Name != "agency/org" || got.URL != "git@github.com:agency/team" || got.Folder != "org" {
		t.Fatalf("agency = %+v %v %v", got, ok, err)
	}
	if _, ok, err = orgFolderStore(home, "acme-hub"); ok || err != nil {
		t.Errorf("acme-hub (no org_folder) found = %v, %v", ok, err)
	}
	if _, ok, err = orgFolderStore(home, "nosuch"); ok || err != nil {
		t.Errorf("nosuch found = %v, %v", ok, err)
	}
}

// TestDefaultSetupReasonOnce pins that the default step's detail is the
// value only, so the printers that add their own "default:" prefix say
// "default: true" once (setup list once printed "default: default: true").
func TestDefaultSetupReasonOnce(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, activeConfig)
	q := SetupQuery{Home: home, Dir: "/srv/x", UserHome: "/home/mira",
		Getenv:   func(string) string { return "" },
		TopLevel: func(context.Context, string) string { return "" },
		RepoID:   func(string) string { return "" },
		Resolve:  func(string) (string, error) { return "", errors.New("no links") }}
	res, err := ResolveSetup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Active.Why != opssetup.WhyDefault {
		t.Fatalf("want the default setup, got %+v", res.Active)
	}
	text := opssetup.ListOut{Active: res.Active}.Text()
	if !strings.Contains(text, "active: personal (default: true)\n") {
		t.Fatalf("active line: %q", text)
	}
	if strings.Count(text, "default") != 1 {
		t.Fatalf("default said more than once: %q", text)
	}
	if h := setupHint(res); h.Why != "" && strings.Count(h.Why, "default") != 1 {
		t.Fatalf("hint why: %q", h.Why)
	}
}

// TestSetupExplicitGate (BUGFIX-18): a --setup or LORE_SETUP naming no
// enabled setup refuses every repo op, reads included, through App.Run;
// a real name and no name pass (a folder tie only refuses writes).
func TestSetupExplicitGate(t *testing.T) {
	t.Setenv(opssetup.EnvSetup, "")
	home := writeSetupsConfig(t, activeConfig+"  - name: old\n    kind: solo\n    disabled: true\n")
	a, err := New(Options{Home: home, RepoDir: gitRepo(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	bg := context.Background()
	for _, name := range []string{"nope", "old"} {
		if err := a.setupExplicitGate(op.WithSetup(bg, name)); !errs.Has(err, errs.SetupNotFound) {
			t.Errorf("--setup %s: err = %v, want E_SETUP_NOT_FOUND", name, err)
		}
	}
	for _, name := range []string{"", "hub"} {
		if err := a.setupExplicitGate(op.WithSetup(bg, name)); err != nil {
			t.Errorf("--setup %q: err = %v, want none", name, err)
		}
	}
	if err := a.setupExplicitGate(bg); err != nil {
		t.Errorf("nothing named: err = %v, want none", err)
	}
	e, ok := a.Registry().Lookup("memory.list")
	if !ok {
		t.Fatal("no memory.list")
	}
	if _, err := a.Run(op.WithSetup(bg, "nope"), e, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{}); !errs.Has(err, errs.SetupNotFound) {
		t.Errorf("memory list --setup nope: err = %v, want E_SETUP_NOT_FOUND", err)
	}
	if _, err := a.Run(op.WithSetup(bg, "hub"), e, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{}); err != nil {
		t.Errorf("memory list --setup hub: %v", err)
	}
	t.Setenv(opssetup.EnvSetup, "nope")
	if _, err := a.Run(bg, e, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{}); !errs.Has(err, errs.SetupNotFound) {
		t.Errorf("LORE_SETUP=nope memory list: err = %v, want E_SETUP_NOT_FOUND", err)
	}
}

// TestSetupNotFoundNamesSetups (INTEG-MIGREST-5): an unknown --setup names
// the setups this machine has (disabled ones too: they exist), and with
// none it says so and hints at the guided setup instead of "0 setups".
func TestSetupNotFoundNamesSetups(t *testing.T) {
	t.Setenv(opssetup.EnvSetup, "")
	home := writeSetupsConfig(t, activeConfig+"  - name: old\n    kind: solo\n    disabled: true\n")
	_, err := ResolveSetup(context.Background(), SetupQuery{Home: home, Dir: t.TempDir(), Flag: "nope"})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.SetupNotFound || !strings.HasSuffix(e.Message, "; the setups on this machine: "+setupNames(t, home)) || e.Hint != "lore setup list" {
		t.Fatalf("with setups: %v", err)
	}
	_, err = ResolveSetup(context.Background(), SetupQuery{Home: t.TempDir(), Dir: t.TempDir(), Flag: "nope"})
	if !errors.As(err, &e) || e.Message != `no setup named "nope"; this machine has no setups` || e.Hint != "lore setup makes one; drop --setup to run without one" {
		t.Fatalf("with none: %v", err)
	}
}

// setupNames is the configured setup names in file order, comma-joined.
func setupNames(t *testing.T, home string) string {
	t.Helper()
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(cfg.Setups))
	for i, s := range cfg.Setups {
		names[i] = s.Name
	}
	if len(names) < 2 {
		t.Fatalf("fixture has %d setups, want several", len(names))
	}
	return strings.Join(names, ", ")
}
