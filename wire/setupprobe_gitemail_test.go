package wire

// identity.git_email outside a repo (devco D-530): the person's global
// address is theirs for their other work and is never what the check asks
// to change. A setup with no folders passes (lore sets each store clone's
// git user itself, D-514); an includeIf file with the setup's address
// passes whatever its condition and wherever its relative path points
// (git reads it next to the config file that names it); a setup folder no
// includeIf covers warns, and the fix names an includeIf file, never
// git config --global.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

func TestGitEmailNeverAsksForGlobalD530(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	loreHome := t.TempDir()
	// The global config lives outside $HOME (GIT_CONFIG_GLOBAL), as in a
	// copied or relocated git setup: a relative include path is read next
	// to it, not in $HOME.
	cfgDir := filepath.Join(t.TempDir(), "gitcfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(cfgDir, "gitconfig")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(loreHome, "config.yaml"), "orgs:\n  - name: northwind\n    identity:\n      key: human:rhea@northwind.example\n")
	write(filepath.Join(cfgDir, "work.inc"), "[user]\n\temail = rhea@northwind.example\n")
	env := append(testenv.HomeEnv(home), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1")
	p := probe.New(probe.Options{Home: home, TmpDir: t.TempDir(), Env: env})
	a := &App{aliasCache: newAliasCache(nil), prefillCache: newPrefillCache(nil)}
	t.Cleanup(func() { _ = a.Close() })
	a.opts.Home = loreHome
	sp := setupProber{a: a, p: p}
	run := func(folders ...string) opssetup.ProbeResult {
		e := onboard.SetupEntry{Name: "northwind-ops", Kind: onboard.KindAdmin, Org: "northwind", Folders: folders}
		return sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: onboard.CheckIdentityGitEmail, Level: onboard.LevelWarn}, Setup: e}})[0]
	}

	// No includeIf at all, no folders: store commits only, lore authors them.
	write(global, "[user]\n\temail = rhea@personal.example\n")
	if r := run(); r.Status != onboard.StatusOK || !strings.Contains(r.Detail, "no folders") {
		t.Errorf("no folders, personal global = %+v, want ok (lore sets the store clone's user)", r)
	}
	// A folder with no includeIf: warn, naming the folder, not the global.
	if r := run("~/code/northwind/*"); r.Status == onboard.StatusOK || !strings.Contains(r.Detail, "~/code/northwind/*") || strings.Contains(r.Detail, "--global") {
		t.Errorf("uncovered folder = %+v, want a warning naming the folder and no --global", r)
	}
	// gitdir: rule with a relative path next to GIT_CONFIG_GLOBAL covers it.
	write(global, "[user]\n\temail = rhea@personal.example\n[includeIf \"gitdir:~/code/northwind/\"]\n\tpath = work.inc\n")
	if r := run("~/code/northwind/*"); r.Status != onboard.StatusOK || !strings.Contains(r.Detail, "includeIf gitdir:~/code/northwind/") {
		t.Errorf("folder under an includeIf with a relative path = %+v, want ok naming the rule", r)
	}
	if r := run("~/elsewhere"); r.Status == onboard.StatusOK {
		t.Errorf("folder outside the includeIf = %+v, want a warning", r)
	}
	// A remote-based condition (no folder) passes a setup with no folders.
	write(global, "[user]\n\temail = rhea@personal.example\n[includeIf \"hasconfig:remote.*.url:git@nw-alias:*/**\"]\n\tpath = work.inc\n")
	if r := run(); r.Status != onboard.StatusOK || !strings.Contains(r.Detail, "hasconfig:remote.*.url:git@nw-alias:*/**") {
		t.Errorf("hasconfig includeIf = %+v, want ok naming the rule", r)
	}
	// The shown fix never touches the global identity.
	f := onboard.FixFor(onboard.CheckIdentityGitEmail, onboard.StepIdentity, onboard.FixDefault)
	if strings.Contains(f.Text, "--global") || strings.Contains(strings.Join(f.Command, " "), "--global") {
		t.Errorf("git_email fix = %+v, want no --global", f)
	}
}
