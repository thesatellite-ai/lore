package wire

// storeops_git_test.go — later 223 / 151 / 222: store init/add/list/remove
// against GIT stores (bare repos on disk reached through url.insteadOf,
// the same transport path as a hosted remote), stores.yaml comments kept;
// an unreachable remote is E_STORE_UNREACHABLE and a url with a password
// or a token-shaped user is refused (E_URL_USERINFO, on init, add and
// set-url) without echoing it, all writing nothing; a git store
// attached with branch: staging takes its writes on that branch of the
// remote; a file:// store with a branch fails the open with E_STORE_SCHEME.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// bareStore makes an empty bare repo at hosts/<path> and returns its
// https://example.test/<path> url.
func bareStore(t *testing.T, hosts, path string) (url, dir string) {
	t.Helper()
	dir = filepath.Join(hosts, filepath.FromSlash(path))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, nil, "init", "-q", "--bare", "-b", "main")
	return "https://" + histHost + "/" + path, dir
}

func TestStoreOpsGitMatrix(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	kbURL, kbDir := bareStore(t, hosts, "acme/kb")
	kb2URL, _ := bareStore(t, hosts, "acme/kb2")
	emptyURL, _ := bareStore(t, hosts, "acme/empty")

	// kb2 becomes a store from another checkout first (add attaches an existing store)
	other := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, other, opsstores.OpInit, `{"name":"kb2","url":"`+kb2URL+`"}`, true); err != nil {
		t.Fatalf("init kb2 elsewhere: %v", err)
	}

	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	const comment = "# kept: the team's git stores\n"
	writeStoresYAML(t, repo, comment+"stores: []\n")
	yamlPath := filepath.Join(repo, filepath.FromSlash(attach.FileName))

	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"`+kbURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if tree := gitIn(t, kbDir, nil, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, "lore-store.yaml") {
		t.Fatalf("init pushed no store marker: %q", tree)
	}
	if _, err := run(t, a, opsstores.OpAdd, `{"name":"kb2","url":"`+kb2URL+`"}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	listed, err := run(t, a, opsstores.OpList, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if i, j := strings.Index(listed, `"kb"`), strings.Index(listed, `"kb2"`); i < 0 || j < 0 || i > j {
		t.Fatalf("list = %s, want kb then kb2", listed)
	}
	if body := string(mustRead(t, yamlPath)); !strings.HasPrefix(body, comment) || !strings.Contains(body, kbURL) || !strings.Contains(body, kb2URL) {
		t.Fatalf("stores.yaml after init+add:\n%s", body)
	}

	secret := "pw" + strings.Repeat("7", 6)   // built at run time: no literal credential in source
	token := "ghp_" + strings.Repeat("A", 36) // a token-shaped user name (OV4)
	pwURL := "https://bob:" + secret + "@" + histHost + "/acme/kb"
	refusals := []struct {
		name, op, in string
		code         errs.Code
	}{
		{"unreachable remote", opsstores.OpAdd, `{"name":"gone","url":"https://` + histHost + `/acme/missing"}`, errs.StoreUnreachable},
		{"not a store", opsstores.OpAdd, `{"name":"plain","url":"` + emptyURL + `"}`, errs.NotAStore},
		{"add: password in the url", opsstores.OpAdd, `{"name":"pw","url":"` + pwURL + `"}`, errs.URLUserinfo},
		{"add: token as the user", opsstores.OpAdd, `{"name":"tok","url":"https://` + token + `@` + histHost + `/acme/kb"}`, errs.URLUserinfo},
		{"init: password in the url", opsstores.OpInit, `{"name":"pw","url":"` + pwURL + `"}`, errs.URLUserinfo},
		{"set-url: password in the url", opsstores.OpSetURL, `{"name":"kb","url":"` + pwURL + `"}`, errs.URLUserinfo},
	}
	for _, r := range refusals {
		t.Run(r.name, func(t *testing.T) {
			before := string(mustRead(t, yamlPath))
			_, rerr := run(t, a, r.op, r.in, true)
			if !errs.Has(rerr, r.code) {
				t.Fatalf("want %s, got %v", r.code, rerr)
			}
			if strings.Contains(rerr.Error(), secret) || strings.Contains(rerr.Error(), token) {
				t.Fatalf("the refusal echoes the credential: %v", rerr)
			}
			if after := string(mustRead(t, yamlPath)); after != before {
				t.Fatalf("a refusal changed stores.yaml:\n%s", after)
			}
		})
	}
	// an ssh login user is not a credential (kept by NormalizeURL)
	if _, err = cleanFileURL("git@github.com:acme/notes.git"); err != nil {
		t.Fatalf("scp form with a login user refused: %v", err)
	}

	for _, name := range []string{"kb2", "kb"} {
		if _, err = run(t, a, opsstores.OpRemove, `{"name":"`+name+`"}`, true); err != nil {
			t.Fatalf("remove %s: %v", name, err)
		}
	}
	body := string(mustRead(t, yamlPath))
	if !strings.HasPrefix(body, comment) || strings.Contains(body, kbURL) {
		t.Fatalf("stores.yaml after removing every store:\n%s", body)
	}
	if cfg, rerr := readStoresFile(repo); rerr != nil || len(cfg.Stores) != 0 {
		t.Fatalf("stores.yaml after removing every store does not read back empty: %+v %v\n%s", cfg, rerr, body)
	}
}

// TestGitStoreBranchEntry (later 222): a git store attached in stores.yaml
// with branch: staging takes a write on that branch: `lore sync` pushes it
// to the remote's staging branch and main does not move; a file:// store
// with a branch fails the open (E_STORE_SCHEME).
func TestGitStoreBranchEntry(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, dir := bareStore(t, hosts, "acme/stage")
	seed := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, seed, opsstores.OpInit, `{"name":"stage","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	mainBefore := gitIn(t, dir, nil, "rev-parse", "main")

	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: stage\n    url: "+url+"\n    branch: staging\n")
	a := openWith(t, repo, Options{})
	withProject(t, a)
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"on the staging branch","to":"stage"}`, true)
	if err != nil {
		t.Fatalf("write to the branch store: %v", err)
	}
	id := idOf(t, out)
	if _, err = a.SyncStores(background(), "stage"); err != nil { // `lore sync` pushes the store
		t.Fatalf("sync: %v", err)
	}
	tree := gitIn(t, dir, nil, "ls-tree", "-r", "--name-only", "staging")
	if !strings.Contains(tree, schema.TableMemories+"/"+id+".json") {
		t.Fatalf("the remote's staging branch lacks the row:\n%s", tree)
	}
	if got := gitIn(t, dir, nil, "rev-parse", "main"); got != mainBefore {
		t.Fatal("a staging write moved main")
	}

	folderRepo := gitRepo(t)
	writeStoresYAML(t, folderRepo, "stores:\n  - name: fs\n    url: "+folderStoreAt(t, folderStore, "internal")+"\n    branch: staging\n")
	b := openWith(t, folderRepo, Options{})
	if _, err = b.Engine(background()); !errs.Has(err, errs.StoreScheme) {
		t.Fatalf("file:// with a branch: %v, want E_STORE_SCHEME", err)
	}
}
