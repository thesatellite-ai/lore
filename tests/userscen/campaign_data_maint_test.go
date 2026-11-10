//go:build userscen

package userscen

// campaign_data_maint_test.go — the maintenance commands of metadata and
// resources: history from git, forget, rebuild and rehome previews, a key
// moved to the personal store and adopted back, a duplicate resource
// merged (dry run first), a bulk delete by --where and its trash restore.

import (
	"strings"
	"testing"
)

// TestCampaignData_MetaHistoryForgetRebuild: dated changes from git
// history; forget needs --confirm and refuses a key in use; rebuild and
// rehome previews write nothing.
func TestCampaignData_MetaHistoryForgetRebuild(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "history row")
	c.MustLore("meta", "set", m, "stage=draft")
	c.CommitAll("draft")
	c.MustLore("meta", "set", m, "stage=final")
	c.CommitAll("final")
	h := c.MustLore("meta", "history", m, "stage").Stdout
	if !strings.Contains(h, "draft") || !strings.Contains(h, "final") {
		t.Errorf("meta history: %s", h)
	}
	c.MustLore("meta", "add", "unused_key", "--type", "text")
	cdRefused(t, c.Lore("meta", "forget", "unused_key", "--json"), "E_NEEDS_CONFIRM")
	if r := c.Lore("meta", "forget", "stage", "--confirm", "--json"); r.Code == 0 {
		t.Errorf("forget of a key in use succeeded: %s", r)
	}
	c.MustLore("meta", "forget", "unused_key", "--confirm")
	if strings.Contains(c.MustLore("meta", "keys").Stdout, "unused_key") {
		t.Error("forgotten key still listed")
	}
	before := cdTreeHash(t, c)
	c.MustLore("meta", "rebuild", "--dry-run")
	c.MustLore("meta", "rehome", "--dry-run")
	c.MustLore("resource", "rehome")
	if cdTreeHash(t, c) != before {
		t.Error("a preview wrote to the tree")
	}
}

// TestCampaignData_MetaMoveAdopt: a repo key row moves to the personal
// store (id kept, values unchanged); adopting it back is refused up front,
// by the dry run too.
func TestCampaignData_MetaMoveAdopt(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "moving key")
	c.MustLore("meta", "set", m, "focus=deep")
	keyID := func() string {
		got := cdMetaShowJSON(t, c, m)
		if len(got.Data) != 1 {
			t.Fatalf("meta show: %+v", got)
		}
		return got.Data[0].KeyID
	}
	before := keyID()
	cdRefused(t, c.Lore("meta", "move", "focus", "--to", "me", "--json"), "E_NEEDS_CONFIRM")
	c.MustLore("meta", "move", "focus", "--to", "me", "--yes")
	if keyID() != before {
		t.Error("meta move changed the key id")
	}
	if got := cdMetaOf(t, c, m); got["focus"] != "deep" {
		t.Errorf("meta move changed the value: %v", got)
	}
	// the key row left the repo, and a row never returns to a place it
	// left: the dry run must say so as the real run does (it said "would
	// adopt"), and nothing moves
	tree := cdTreeHash(t, c)
	cdRefused(t, c.Lore("meta", "adopt", "--from", "me", "--dry-run", "--json"), "E_UNSUPPORTED")
	cdRefused(t, c.Lore("meta", "adopt", "--from", "me", "--yes", "--json"), "E_UNSUPPORTED")
	if cdTreeHash(t, c) != tree || keyID() != before {
		t.Error("a refused adopt changed the tree or the key id")
	}
}

// TestCampaignData_ResourceMergeAndBulkDelete: merge a duplicate (dry run
// writes nothing; the real one moves refs and archives the duplicate);
// delete every match of --where, then bring them back from the trash.
func TestCampaignData_ResourceMergeAndBulkDelete(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner", "--set", "ip=10.0.0.1")
	c.MustLore("resource", "add", "host", "web1_old", "--set", "provider=hetzner", "--set", "cores=4")
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=web1_old")
	before := cdTreeHash(t, c)
	c.MustLore("resource", "merge", "host/web1_old", "--into", "host/web1", "--dry-run")
	if cdTreeHash(t, c) != before {
		t.Fatal("merge --dry-run wrote to the tree")
	}
	cdRefused(t, c.Lore("resource", "merge", "host/web1_old", "--into", "service/api", "--yes", "--json"), "E_RESOURCE_KIND_MISMATCH")
	c.MustLore("resource", "merge", "host/web1_old", "--into", "host/web1", "--yes")
	if got := resShow(t, c, "service/api"); got.Values["runs_on"] != "web1" {
		t.Errorf("after merge api.runs_on = %q", got.Values["runs_on"])
	}
	if got := resShow(t, c, "host/web1"); got.Values["cores"] != "4" || got.Values["ip"] != "10.0.0.1" {
		t.Errorf("merge did not fill the survivor: %+v", got.Values)
	}
	if n := cdResNames(t, c, "host"); cdHas(n, "web1_old") {
		t.Errorf("the duplicate is still live: %v", n)
	}
	c.MustLore("resource", "add", "host", "tmp1", "--set", "provider=aws")
	c.MustLore("resource", "add", "host", "tmp2", "--set", "provider=aws")
	// two rows are under batch.preview_rows, so the bulk form does not ask
	// (ops/settings/resources.go OpResDelete); --dry-run first shows them
	c.MustLore("resource", "delete", "--kind", "host", "--where", "provider=aws", "--dry-run").Says(t, "tmp1", "tmp2")
	ids := []string{resShow(t, c, "host/tmp1").ID, resShow(t, c, "host/tmp2").ID}
	c.MustLore("resource", "delete", "--kind", "host", "--where", "provider=aws", "--yes")
	if n := cdResNames(t, c, "host"); cdHas(n, "tmp1") || cdHas(n, "tmp2") || !cdHas(n, "web1") {
		t.Fatalf("after bulk delete: %v", n)
	}
	for _, id := range ids {
		c.MustLore("trash", "restore", id, "--yes")
	}
	if n := cdResNames(t, c, "host"); !cdHas(n, "tmp1") || !cdHas(n, "tmp2") {
		t.Errorf("after restore: %v", n)
	}
}
