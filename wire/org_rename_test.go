package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// renameConfig is a hand-kept ~/.lore/config.yaml: comments on the head,
// an entry, a key, the defaults and repo_orgs, and a second org whose
// references must not move.
const renameConfig = `# my lore config (hand kept)
orgs:
  # the firm I work for
  - name: agency # label only
    defaults:
      client_repos: {memory: agency/notes, task: agency} # where client rows go
  - name: globex
repo_orgs:
  # chosen with lore org use
  prj_01a1a5aca7f57659be7320c5f4b70011: agency
  prj_01a1a5aca7f57659be7320c5f4b70022: {org: agency, remotes: [github.com/acme/app]} # pinned
  prj_01a1a5aca7f57659be7320c5f4b70033: globex
# trailing note
`

// TestOrgRenameKeepsCommentsAndJSON (later 80): `lore org rename` on
// cmd/lore outside any repo rewrites only the label, the entry's own
// defaults.client_repos targets and its repo_orgs choices; every comment
// of the hand-kept file survives, the other org is untouched, the file
// stays 0600, and a refused rename (unknown entry, name taken, bad name,
// same name) leaves the file byte for byte. The --json shape is v0.1.17's
// (from, to, id omitted when not recorded, id_recorded, repo_orgs,
// defaults, config).
func TestOrgRenameKeepsCommentsAndJSON(t *testing.T) {
	t.Setenv("LORE_ORG", "")
	dir := t.TempDir() // not a git repo: the id is not recorded
	home := filepath.Join(t.TempDir(), ".lore")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(home), []byte(renameConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h := orgHostAt(t, dir, home)
	ctx := context.Background()

	refusals := []struct {
		name     string
		old, new string
		code     errs.Code
	}{
		{"unknown entry", "nope", "x", errs.NotFound},
		{"name taken", "agency", "globex", errs.InvalidInput},
		{"bad name", "agency", "Bad Name", errs.InvalidInput},
		{"empty name", "agency", "", errs.InvalidInput},
	}
	for _, c := range refusals {
		_, err := h.Rename(ctx, opsorgcmd.RenameIn{Old: c.old, New: c.new})
		wantCode(t, err, c.code, c.name)
		if got := readConfig(t, home); got != renameConfig {
			t.Fatalf("%s: a refused rename changed config.yaml:\n%s", c.name, got)
		}
	}

	out, err := h.Rename(ctx, opsorgcmd.RenameIn{Old: "agency", New: "northwind"})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	got := readConfig(t, home)
	for _, want := range []string{
		"# my lore config (hand kept)", "# the firm I work for", "# label only", "# where client rows go",
		"# chosen with lore org use", "# pinned", "# trailing note",
		"name: northwind", "memory: northwind/notes", "task: northwind", "70011: northwind", "org: northwind",
		"name: globex", "70033: globex",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("config after rename lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "agency") {
		t.Fatalf("the old label is still in config.yaml:\n%s", got)
	}
	if fi, err := os.Stat(configPath(home)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.yaml mode after rename: %v %v", fi, err)
	}
	if out.RepoOrgs != 2 || out.Defaults != 2 || out.IDRecorded || out.ID != "" {
		t.Fatalf("rename counts: %+v", out)
	}
	text := out.Text()
	for _, want := range []string{"✓ org entry agency is now northwind (this machine)", "id not recorded", "2 project choice(s)", "2 defaults.client_repos target(s)", "rows are unchanged"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text lacks %q:\n%s", want, text)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(b, &shape); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range shape {
		keys = append(keys, k)
	}
	want := map[string]bool{"from": true, "to": true, "id_recorded": true, "repo_orgs": true, "defaults": true, "config": true}
	if len(shape) != len(want) {
		t.Fatalf("--json keys %v, want %v (id omitted when not recorded, no lines)", keys, want)
	}
	for k := range want {
		if _, ok := shape[k]; !ok {
			t.Fatalf("--json lacks %q: %s", k, b)
		}
	}

	// Renaming back is a plain second rename (nothing remembers the old
	// label); the comments still hold (the yaml is re-indented, not
	// rewritten).
	if _, err := h.Rename(ctx, opsorgcmd.RenameIn{Old: "northwind", New: "agency"}); err != nil {
		t.Fatalf("rename back: %v", err)
	}
	got = readConfig(t, home)
	if strings.Contains(got, "northwind") || strings.Count(got, "# ") != 7 || !strings.Contains(got, "70022: {org: agency") {
		t.Fatalf("rename there and back:\n%s", got)
	}
}
