package wire

// readscope_test.go — lists and search read what the repo attaches (devco
// D-484, D-485): a store attached whole is every folder of it for rule
// list, task list and search, as for sql; in a store folder, search
// --all-repos covers every folder of the store.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

func TestListsReadEveryFolderOfAStoreAttachedWhole(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	// the writer attaches each folder (store folder add records it)
	writer := openWith(t, gitRepo(t), Options{Home: home})
	withProject(t, writer)
	if _, err := run(t, writer, opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	const ruleA, ruleB, taskB = "client a deploys on fridays", "client b deploys on mondays", "renew the client b deploy key"
	for _, f := range []string{"client_a", "client_b"} {
		if _, err := run(t, writer, opsstores.OpFolderAdd, `{"store":"kb","folder":"`+f+`"}`, true); err != nil {
			t.Fatalf("folder add %s: %v", f, err)
		}
	}
	for to, body := range map[string]string{"kb/client_a": ruleA, "kb/client_b": ruleB} {
		if _, err := run(t, writer, opsrule.OpAdd, `{"body":"`+body+`","to":"`+to+`"}`, true); err != nil {
			t.Fatalf("rule add --to %s: %v", to, err)
		}
	}
	if _, err := run(t, writer, opstask.OpAdd, `{"title":"`+taskB+`","to":"kb/client_b","commitment":"accepted"}`, true); err != nil {
		t.Fatalf("task add: %v", err)
	}

	// the reader attaches the store whole: no folder of it by name
	readerDir := gitRepo(t)
	withProject(t, openWith(t, readerDir, Options{Home: home}))
	if _, err := run(t, openWith(t, readerDir, Options{Home: home}), opsstores.OpAdd, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("store add: %v", err)
	}
	read := func(opName, body string) string {
		t.Helper()
		out, err := run(t, openWith(t, readerDir, Options{Home: home}), opName, body, false)
		if err != nil {
			t.Fatalf("%s %s in the repo attaching kb whole: %v", opName, body, err)
		}
		return out
	}
	for _, c := range []struct{ op, body string }{
		{opsrule.OpList, `{}`},
		{opsrule.OpList, `{"store":"kb"}`},
		{opssearch.OpRun, `{"query":"deploys"}`},
	} {
		got := read(c.op, c.body)
		if !strings.Contains(got, ruleA) || !strings.Contains(got, ruleB) {
			t.Errorf("%s %s does not read every folder of kb (sql does): %s", c.op, c.body, got)
		}
	}
	if got := read(opstask.OpList, `{}`); !strings.Contains(got, taskB) {
		t.Errorf("task list does not read kb/client_b: %s", got)
	}

	// D-485: in one folder of the store clone, --all-repos covers every
	// folder of the store; without it the read keeps its folder
	folderA := filepath.Join(home, gitbackend.StoresDir, "acme", "kb", "client_a")
	inFolder := func(body string) string {
		t.Helper()
		out, err := run(t, openWith(t, folderA, Options{Home: home}), opssearch.OpRun, body, false)
		if err != nil {
			t.Fatalf("search %s in client_a: %v", body, err)
		}
		return out
	}
	if got := inFolder(`{"query":"deploys"}`); !strings.Contains(got, ruleA) || strings.Contains(got, ruleB) {
		t.Errorf("search in client_a reads more or less than its folder: %s", got)
	}
	if got := inFolder(`{"query":"deploys","all_repos":true}`); !strings.Contains(got, ruleA) || !strings.Contains(got, ruleB) {
		t.Errorf("search --all-repos in client_a does not cover every folder of the store: %s", got)
	}
}

// TestListsKeepTheOrgEntryAttachToItsPlace (devco D-497): an owned repo
// with no link attaches its org's store through the org entry, at the
// store's root folder; a folder of that store is shown only in repos that
// attach it (STORE TARGETS), so lists there read the root alone. D-484
// widened that attach like a whole-store entry and listed every folder's
// rows. A stores.yaml entry with no folder stays whole
// (TestListsReadEveryFolderOfAStoreAttachedWhole).
func TestListsKeepTheOrgEntryAttachToItsPlace(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	writer := openWith(t, gitRepo(t), Options{Home: home})
	withProject(t, writer)
	if _, err := run(t, writer, opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, writer, opsstores.OpFolderAdd, `{"store":"kb","folder":"tips"}`, true); err != nil {
		t.Fatalf("folder add: %v", err)
	}
	const tip = "tips folder deploys on fridays"
	if _, err := run(t, writer, opsrule.OpAdd, `{"body":"`+tip+`","to":"kb/tips"}`, true); err != nil {
		t.Fatalf("rule add --to kb/tips: %v", err)
	}
	const root = "store root deploys on mondays"
	if _, err := run(t, writer, opsrule.OpAdd, `{"body":"`+root+`","to":"kb"}`, true); err != nil {
		t.Fatalf("rule add --to kb: %v", err)
	}
	listWith := func(org attach.Store) string {
		t.Helper()
		dir := gitRepo(t)
		opts := Options{Home: home, OrgStores: func(context.Context) ([]attach.Store, error) { return []attach.Store{org}, nil }}
		withProject(t, openWith(t, dir, opts))
		out, err := run(t, openWith(t, dir, opts), opsrule.OpList, `{}`, false)
		if err != nil {
			t.Fatalf("rule list with %s attached: %v", org.Name, err)
		}
		return out
	}
	if got := listWith(attach.Store{Name: "acme", URL: url}); strings.Contains(got, tip) || !strings.Contains(got, root) {
		t.Errorf("the org entry's root attach must list the root row and no folder this repo does not attach: %s", got)
	}
}
