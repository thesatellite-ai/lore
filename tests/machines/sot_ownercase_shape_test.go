//go:build machines && !windows

package machines

// sot_ownercase_shape_test.go — the owner's machine as reported on
// 2026-11-09 (the source-of-truth inventory rows 1-2): one person with two
// names in ~/.lore/config.yaml and two stale actor copies.
//
//	config.yaml   identity: {name: Pat One, key: human:pat@example.com} (the old top level)
//	              setups[personal].identity: {name: Pat Two, key: …}   (the setup, the source)
//	has-lore      .lore/data/actors/<act>.json   display_name Pat One  (written while only the top level answered)
//	me (folder)   .lore/data/actors/<act>.json   display_name Pat Two  (written after the setup got its identity)
//
// It is built on the fresh shape by ownerCase (not registered with
// RegisterShape: a registered shape runs in every sweep and lifecycle,
// and this one is a deliberate conflict the doctor must ask about).

import (
	"os"
	"path/filepath"
	"testing"
)

// The owner case's names and key.
const (
	OwnerTopName   = "Pat One"
	OwnerSetupName = "Pat Two"
	// OwnerKey is the person's key, quoted for YAML (the brief's bare
	// pat@example.com is refused by every command: E_CONFIG_INVALID
	// TEAM-029, a key is human:<email>).
	OwnerKey   = `"human:pat@example.com"`
	OwnerSetup = "personal"
	// OwnerStore is the personal store's name (a local folder).
	OwnerStore = "me"
)

// ownerTopOnly is config.yaml before the setup had its own identity: the
// top level is all that answers.
const ownerTopOnly = `identity: {name: ` + OwnerTopName + `, key: ` + OwnerKey + `}
setups:
  - name: ` + OwnerSetup + `
    kind: solo
    default: true
    folders:
      - ~/code/**
`

// ownerConfig is the owner's config.yaml: both blocks, two names.
const ownerConfig = `identity: {name: ` + OwnerTopName + `, key: ` + OwnerKey + `}
setups:
  - name: ` + OwnerSetup + `
    kind: solo
    default: true
    identity:
      name: ` + OwnerSetupName + `
      key: ` + OwnerKey + `
    folders:
      - ~/code/**
`

// ownerCase turns a fresh machine into the owner's: the repo copy of the
// actor is written under the top-level name, then the setup gets its own
// identity and the personal store's copy is written under that name.
// It returns the personal store's folder.
func ownerCase(t *testing.T, m *Machine) string {
	t.Helper()
	b := &builder{t: t, m: m}
	b.loreHome()
	cfg := filepath.Join(m.Home, ".lore", "config.yaml")
	b.file(cfg, ownerTopOnly, 0o600)
	repo := m.Repos[RepoLore]
	b.lore(repo, "memory", "add", "--body", "written while only the top level answered")
	b.file(cfg, ownerConfig, 0o600)
	me := filepath.Join(m.Dir, "me-store")
	b.lore(repo, "store", "init", OwnerStore, "file://"+me, "--yes")
	b.lore(repo, "memory", "add", "--body", "written after the setup got its identity", "--to", OwnerStore)
	// The config the owner has, whatever the commands above wrote to it.
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("owner case config.yaml after building:\n%s", raw)
	return me
}
