//go:build machines && !windows

package machines

// sot_ownercase_test.go — the owner's case (sot_ownercase_shape_test.go)
// against the target behaviour of SOURCE_OF_TRUTH rows 1-2 (owner
// decisions 2026-11-09):
//
//	1. doctor names both names and offers one fix per name; it never picks,
//	   and a config write leaves both blocks as they are
//	2. the keep fix (lore identity migrate --keep setup) leaves the setup
//	   the only identity: no top-level block, whoami says the kept name
//	3. lore actor show --id <act> lists both stores; the copy written under the
//	   other name is stale until the next write to its store
//	4. after a write to each store both copies are current with the kept
//	   name (CLI and REST)

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// sotActorShow is the part of actor show --json the owner case reads.
type sotActorShow struct {
	Data struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Copies      []struct {
			Store       string `json:"store"`
			DisplayName string `json:"display_name"`
			State       string `json:"state"`
		} `json:"copies"`
	} `json:"data"`
}

// sotCopyCurrent is actorsource.StateCurrent; sotCopySource is a store
// that holds the source itself (a roster), also current.
const (
	sotCopyCurrent = "current"
	sotCopySource  = "source"
)

// sotOwnerConfig is the identity part of config.yaml.
type sotOwnerConfig struct {
	Identity *struct {
		Name string `yaml:"name"`
		Key  string `yaml:"key"`
	} `yaml:"identity"`
	Setups []struct {
		Name     string `yaml:"name"`
		Identity *struct {
			Name string `yaml:"name"`
			Key  string `yaml:"key"`
		} `yaml:"identity"`
	} `yaml:"setups"`
}

func sotReadOwnerConfig(t *testing.T, m *Machine) sotOwnerConfig {
	t.Helper()
	var c sotOwnerConfig
	if err := yaml.Unmarshal([]byte(csConfig(t, m)), &c); err != nil {
		t.Fatalf("config.yaml: %v\n%s", err, csConfig(t, m))
	}
	return c
}

// setupName is the owner setup's identity name ("" when it has none).
func (c sotOwnerConfig) setupName() string {
	for _, s := range c.Setups {
		if s.Name == OwnerSetup && s.Identity != nil {
			return s.Identity.Name
		}
	}
	return ""
}

func TestSotOwnerCase(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	ownerCase(t, m)
	repo := m.Repos[RepoLore]
	act := sotActorID(t, m, repo)

	// 1. doctor asks: both names, a keep fix for each, nothing picked.
	doc := sotOut(m.Lore(t, repo, "doctor"))
	for _, want := range []string{OwnerTopName, OwnerSetupName, "--keep setup", "--keep top"} {
		if !strings.Contains(doc, want) {
			t.Errorf("doctor on two names lacks %q (it must name both and offer to keep either):\n%s", want, doc)
		}
	}
	// A config write (which migrates a top-level copy that agrees) must
	// leave two different names alone.
	lsRun(t, m, "", "config", "set", "live.autopush", "true")
	if c := sotReadOwnerConfig(t, m); c.Identity == nil || c.Identity.Name != OwnerTopName || c.setupName() != OwnerSetupName {
		t.Errorf("a config write picked a name silently: config.yaml is now\n%s", csConfig(t, m))
	}

	// 2. the keep fix: the setup is the only identity.
	lsRun(t, m, repo, "identity", "migrate", "--keep", "setup", "--setup", OwnerSetup)
	c := sotReadOwnerConfig(t, m)
	if c.Identity != nil && (c.Identity.Name != "" || c.Identity.Key != "") {
		t.Errorf("after the keep fix a top-level identity is left:\n%s", csConfig(t, m))
	}
	if c.setupName() != OwnerSetupName {
		t.Errorf("after the keep fix the setup's identity name is %q, want %q:\n%s", c.setupName(), OwnerSetupName, csConfig(t, m))
	}
	if got := lsRun(t, m, repo, "whoami"); !strings.Contains(got, OwnerSetupName) || strings.Contains(got, OwnerTopName) {
		t.Errorf("whoami after the keep fix: %s", got)
	}
	// The identity question is gone. The old name may still appear only in
	// the actor_copies warning: the repo's actor copy was written as
	// OwnerTopName and stays stale until step 4 refreshes it (step 3).
	doc = sotOut(m.Lore(t, repo, "doctor"))
	if strings.Contains(doc, "--keep") || strings.Contains(doc, "identity.top_level") || strings.Contains(doc, "identity_source") {
		t.Errorf("doctor after the keep fix still asks:\n%s", doc)
	}
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, OwnerTopName) && !strings.Contains(line, "actor_copies") {
			t.Errorf("doctor after the keep fix still names %q outside the stale actor copy: %q\n%s", OwnerTopName, line, doc)
		}
	}

	// 3. actor show lists both stores; the copy written as Pat One is stale.
	show := func(when string) sotActorShow {
		t.Helper()
		var s sotActorShow
		got := lsRun(t, m, repo, "actor", "show", act)
		if err := json.Unmarshal([]byte(got), &s); err != nil {
			t.Fatalf("[%s] actor show: %v: %s", when, err, got)
		}
		stores := map[string]bool{}
		for _, cp := range s.Data.Copies {
			stores[cp.Store] = true
		}
		if !stores["repo"] || !stores[OwnerStore] {
			t.Errorf("[%s] actor show lists the stores %v, want repo and %s:\n%s", when, stores, OwnerStore, got)
		}
		return s
	}
	s := show("after keep")
	if s.Data.DisplayName != OwnerSetupName {
		t.Errorf("actor show after the keep fix shows %q, want the source's %q", s.Data.DisplayName, OwnerSetupName)
	}
	staleRepo := false
	for _, cp := range s.Data.Copies {
		if cp.Store == "repo" && cp.DisplayName == OwnerTopName && cp.State != sotCopyCurrent && cp.State != sotCopySource {
			staleRepo = true
		}
	}
	if !staleRepo {
		t.Errorf("actor show after the keep fix does not mark the repo copy (%s) stale: %+v", OwnerTopName, s.Data.Copies)
	}

	// 4. a write to each store refreshes its copy: both current, kept name.
	lsRun(t, m, repo, "memory", "add", "--body", "a write to the repo store")
	lsRun(t, m, repo, "memory", "add", "--body", "a write to the personal store", "--to", OwnerStore)
	s = show("after a write to each store")
	for _, cp := range s.Data.Copies {
		if cp.DisplayName != OwnerSetupName || (cp.State != sotCopyCurrent && cp.State != sotCopySource) {
			t.Errorf("after a write to each store the %s copy is %q (%s), want %q current", cp.Store, cp.DisplayName, cp.State, OwnerSetupName)
		}
	}
	api := csStartAPI(t, m, repo)
	if got := sotAPICall(t, api, "GET", "/v1/actors/"+act, "", sotStatusOK); !strings.Contains(got, OwnerSetupName) || strings.Contains(got, `"`+OwnerTopName+`"`) {
		t.Errorf("REST actor show after the refresh: %s", got)
	}
}
