package main

import (
	"strings"
	"testing"
)

// TestEnvScopeDefaults: row commands honour LORE_REPO and LORE_PROJECT_ID
// as v0.1.17 did (flag > env): an unknown value is refused naming it, an
// explicit flag wins over the variable, unset changes nothing.
func TestEnvScopeDefaults(t *testing.T) {
	f := newCoreFixture(t)
	base := f.env
	with := func(kv ...string) { f.env = append(append([]string(nil), base...), kv...) }
	defer func() { f.env = base }()

	with("LORE_REPO=nosuchrepo")
	if r := f.run(e2eBin, "memory", "add", "--body", "scoped note"); r.code == 0 || !strings.Contains(r.stderr, "nosuchrepo") {
		t.Fatalf("LORE_REPO is honoured: %+v", r)
	}
	with("LORE_PROJECT_ID=prj_nope")
	if r := f.run(e2eBin, "memory", "list"); r.code == 0 || !strings.Contains(r.stderr, "prj_nope") {
		t.Fatalf("LORE_PROJECT_ID is honoured: %+v", r)
	}
	f.env = base
	f.mount()
	pid := f.projectID()
	with("LORE_PROJECT_ID=prj_nope")
	if r := f.run(e2eBin, "memory", "list", "--project", pid); r.code != 0 {
		t.Fatalf("--project beats LORE_PROJECT_ID: %+v", r)
	}
	f.env = base
	f.lore("memory", "add", "--body", "unscoped note")
}
