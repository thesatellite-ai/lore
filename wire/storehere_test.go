package wire

// storehere_test.go — STORE layout 1.5: a store's checkout is never a code
// repo. `cd <store>/<folder> && lore task list` works on that folder (at
// the top, the root folder), a read there changes no file of the store,
// a repo-only command is refused naming the store, and a clone of the
// store lore does not use is refused with the command that makes lore use
// it.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// treeOf lists every path under dir (files and folders) with its size,
// .git excluded: what a read must leave exactly as it was.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(dir, p)
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		out = append(out, rel+" "+info.Mode().String()+" "+strconv.FormatInt(info.Size(), 10)+" "+info.ModTime().String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestStoreCheckoutIsNoCodeRepo(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := openWith(t, gitRepo(t), Options{Home: home})
	if _, err := run(t, repo, opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, repo, opsstores.OpFolderAdd, `{"store":"kb","folder":"client_a"}`, true); err != nil {
		t.Fatalf("folder add: %v", err)
	}
	clone := filepath.Join(home, gitbackend.StoresDir, "acme", "kb")
	folder := filepath.Join(clone, "client_a")
	if _, err := os.Stat(filepath.Join(clone, "lore-store.yaml")); err != nil {
		t.Fatalf("the store clone is not at %s: %v", clone, err)
	}

	// a write in the folder lands in that folder, committed in the clone
	in := openWith(t, folder, Options{Home: home})
	out, err := run(t, in, opstask.OpAdd, `{"title":"call the client back"}`, true)
	if err != nil {
		t.Fatalf("task add in the store folder: %v", err)
	}
	var added struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(out), &added); err != nil || added.Data.ID == "" {
		t.Fatalf("task add output %s: %v", out, err)
	}
	id := added.Data.ID
	if !strings.Contains(gitIn(t, clone, nil, "log", "-1", "--name-only", "--format="), "client_a/") {
		t.Fatalf("the task was not committed under client_a/: %s", gitIn(t, clone, nil, "log", "-1", "--name-only"))
	}
	if st := gitIn(t, clone, nil, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Fatalf("the clone is not clean after a write: %q", st)
	}

	// a read in the folder lists it and changes nothing in the store
	before := treeOf(t, clone)
	list := openWith(t, folder, Options{Home: home})
	got, err := run(t, list, opstask.OpList, `{}`, false)
	if err != nil {
		t.Fatalf("task list in the store folder: %v", err)
	}
	if !strings.Contains(got, id) {
		t.Fatalf("task list in client_a/ misses %s: %s", id, got)
	}
	if after := treeOf(t, clone); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("a read changed the store's files:\nbefore %v\nafter  %v", before, after)
	}
	for _, p := range []string{filepath.Join(clone, ".lore", "cache"), filepath.Join(folder, ".lore", "cache"), filepath.Join(clone, ".gitignore")} {
		if _, serr := os.Stat(p); serr == nil {
			t.Fatalf("lore made %s inside the store", p)
		}
	}

	// at the store top the command works on the root folder
	top := openWith(t, clone, Options{Home: home})
	got, err = run(t, top, opstask.OpList, `{}`, false)
	if err != nil {
		t.Fatalf("task list at the store top: %v", err)
	}
	if strings.Contains(got, id) {
		t.Fatalf("the root folder lists client_a's task: %s", got)
	}

	// a command that edits a code repo is refused, naming the store
	if _, err = run(t, top, opsstores.OpAdd, `{"name":"other","url":"`+url+`"}`, true); !errs.Has(err, errs.NotAStore) || !strings.Contains(err.Error(), "lore store") {
		t.Fatalf("store add inside a store: %v", err)
	}

	// a clone lore does not use is refused with the next step
	mine := filepath.Join(t.TempDir(), "my-kb")
	gitIn(t, filepath.Dir(mine), nil, "clone", "-q", url, mine)
	foreign := openWith(t, filepath.Join(mine, "client_a"), Options{Home: home})
	_, err = run(t, foreign, opstask.OpList, `{}`, false)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Refused || !strings.Contains(e.Message, mine) || !strings.Contains(e.Hint, "lore store use "+mine) {
		t.Fatalf("a clone lore does not use: %v (hint %q)", err, hintOf(err))
	}
}

// hintOf is err's hint ("" when it has none).
func hintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}
