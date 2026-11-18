package wire

// identity.git_email with the includeIf rule in a file the global config
// pulls in with [include] path= (devco D-594): git follows the include
// when it commits, so the check must too. Before the fix the probe read
// --global without --includes and warned "no includeIf gitdir: rule for
// that folder sets …" while git used the setup's address there.

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

func TestGitEmailFollowsIncludedFileD594(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	loreHome := t.TempDir()
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
	// The top file only includes the person's main config (a copied or
	// sandboxed git setup); the includeIf lives in that main config, and
	// its relative path is read next to the file that names it.
	mainDir := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(mainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(mainDir, "work.inc"), "[user]\n\temail = rhea@northwind.example\n")
	write(filepath.Join(mainDir, "main.gitconfig"), "[user]\n\temail = rhea@personal.example\n[includeIf \"gitdir:~/code/northwind/\"]\n\tpath = work.inc\n")
	write(global, "[include]\n\tpath = "+filepath.Join(mainDir, "main.gitconfig")+"\n")
	env := append(testenv.HomeEnv(home), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1")
	p := probe.New(probe.Options{Home: home, TmpDir: t.TempDir(), Env: env})
	a := &App{aliasCache: newAliasCache(nil), prefillCache: newPrefillCache(nil)}
	t.Cleanup(func() { _ = a.Close() })
	a.opts.Home = loreHome
	sp := setupProber{a: a, p: p}
	run := func(folders ...string) opssetup.ProbeResult {
		e := onboard.SetupEntry{Name: "northwind", Kind: onboard.KindMember, Org: "northwind", Folders: folders}
		return sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: onboard.CheckIdentityGitEmail, Level: onboard.LevelWarn}, Setup: e}})[0]
	}

	// The global address set in the included file is git's global address.
	if id := p.GlobalIdentity(context.Background()); id.Email != "rhea@personal.example" {
		t.Errorf("GlobalIdentity = %+v, want the address set in the [include]d file", id)
	}
	// The includeIf inside the included file covers the folder.
	if r := run("~/code/northwind/*"); r.Status != onboard.StatusOK || !strings.Contains(r.Detail, "includeIf gitdir:~/code/northwind/") {
		t.Errorf("includeIf inside an [include]d file = %+v, want ok naming the rule", r)
	}
	// A folder it does not cover still warns.
	if r := run("~/elsewhere"); r.Status == onboard.StatusOK {
		t.Errorf("folder outside the included rule = %+v, want a warning", r)
	}
}
