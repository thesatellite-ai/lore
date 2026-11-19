package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/ops/orgcmd"
)

// importWritesHost is an org Host whose Import writes what a real import
// of a store url writes in the lore home (the org entry in config.yaml and
// the store clone under stores/) and ends as a setup read's Admit does.
type importWritesHost struct {
	orgcmd.Host // only Import is called
	home        string
	t           *testing.T
}

func (h importWritesHost) Import(_ context.Context, _ orgcmd.ImportIn, _ bool) (orgcmd.Report, error) {
	clone := filepath.Join(h.home, "stores", "acme-co", "lore-team")
	if err := os.MkdirAll(clone, 0o750); err != nil {
		h.t.Fatal(err)
	}
	for p, b := range map[string]string{
		filepath.Join(clone, "lore-store.yaml"): "store: lore-team\n",
		filepath.Join(h.home, "config.yaml"):    "orgs:\n  - name: acme\n",
	} {
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		if err := os.WriteFile(p, []byte(b), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
	return orgcmd.Report{}, errProbeAdmitted
}

// TestJoinPlanPrefillLeavesHome: the join plan's roster prefill (JoinPerson
// on a store url no entry has) imports the org to read the roster; the
// home's org entry and store clone are put back as they were, whatever the
// read found (devco D-655: the entry went, config.yaml and the clone of
// stores/<owner>/<repo> stayed after a plan never applied).
func TestJoinPlanPrefillLeavesHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "stores", "_me"), 0o750); err != nil {
		t.Fatal(err)
	}
	a := &App{opts: Options{Home: home}}
	a.opts.OrgCommands = importWritesHost{home: home, t: t}
	sp := setupProber{a: a}
	sp.JoinPerson(context.Background(), "acme", "git@github-acme:acme-co/lore-team.git", "kim@acme.example")
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err == nil {
		t.Errorf("the plan's prefill left config.yaml")
	}
	if _, err := os.Stat(filepath.Join(home, "stores", "acme-co")); err == nil {
		t.Errorf("the plan's prefill left the store clone stores/acme-co/lore-team")
	}
	if _, err := os.Stat(filepath.Join(home, "stores", "_me")); err != nil {
		t.Errorf("the personal store folder there before went: %v", err)
	}
}
