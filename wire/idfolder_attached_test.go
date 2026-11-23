package wire

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	oprow "github.com/thesatellite-ai/lore/ops/row"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestByIDFindsRowMovedOutOfAttachedFolder (devco:D-923, bug hunt H-181):
// `lore row move --id X --to <store>/<other folder>` leaves a tombstone in
// the folder the row left. A repo attaching only that folder then answered
// every command by id (show, meta set, edit, archive) with "X was deleted
// from <store>": the tombstone stopped the search before the store's other
// folders were looked in, and a store attached through stores.yaml outside
// any org had its other folders never looked in at all. Now the live copy
// in the folder the row moved to answers, and an id no folder holds live
// still answers the tombstone.
func TestByIDFindsRowMovedOutOfAttachedFolder(t *testing.T) {
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
	for _, f := range []string{"product", "billing"} {
		if _, err := run(t, writer, opsstores.OpFolderAdd, `{"store":"kb","folder":"`+f+`"}`, true); err != nil {
			t.Fatalf("folder add %s: %v", f, err)
		}
	}
	// the reader attaches the store at folder product only
	readerDir := gitRepo(t)
	withProject(t, openWith(t, readerDir, Options{Home: home}))
	if _, err := run(t, openWith(t, readerDir, Options{Home: home}), opsstores.OpAdd, `{"name":"kb","url":"`+url+`","folder":"product"}`, true); err != nil {
		t.Fatalf("store add --folder product: %v", err)
	}
	reader := func() *App { return openWith(t, readerDir, Options{Home: home}) }
	if _, err := run(t, reader(), opssettings.OpMetaAdd, `{"name":"owner","type":"text","to":"kb"}`, true); err != nil {
		t.Fatalf("meta add: %v", err)
	}
	out, err := run(t, reader(), opsmemory.OpAdd, `{"body":"the invoice queue drains every ten minutes","to":"kb"}`, true)
	if err != nil {
		t.Fatalf("memory add --to kb: %v", err)
	}
	mem := idOf(t, out)
	// the writer (attaching the store whole) moves it to the other folder
	if _, err := run(t, writer, oprow.OpMove, `{"id":"`+mem+`","to":"kb/billing"}`, true); err != nil {
		t.Fatalf("row move: %v", err)
	}
	for _, s := range []struct{ op, in string }{
		{oprow.OpShow, `{"id":"` + mem + `"}`},
		{opssettings.OpMetaSet, `{"id":"` + mem + `","pair":"owner=dana"}`},
		{opsmemory.OpEdit, `{"id":"` + mem + `","body":"the invoice queue drains every hour"}`},
	} {
		if _, err := run(t, reader(), s.op, s.in, true); err != nil {
			t.Errorf("%s by id from the repo attaching kb/product after the move to kb/billing: %v", s.op, err)
		}
	}
	// the edit landed in the folder the row moved to
	var hits []string
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, filepath.Join("billing", "memories", mem+".json")) {
			hits = append(hits, p)
		}
		return nil
	})
	if len(hits) != 1 {
		t.Fatalf("kb/billing holds %d copies of the moved row, want 1", len(hits))
	}
	b, err := os.ReadFile(hits[0])
	if err != nil || !strings.Contains(string(b), "every hour") || !strings.Contains(string(b), "dana") {
		t.Errorf("kb/billing's copy lacks the edit and the meta value (err %v): %s", err, b)
	}
	// a deleted row (no live copy anywhere) still answers its tombstone: in
	// the trash, as before the fix
	out, err = run(t, reader(), opsmemory.OpAdd, `{"body":"the cache warms at nine","to":"kb"}`, true)
	if err != nil {
		t.Fatalf("memory add: %v", err)
	}
	gone := idOf(t, out)
	if _, err := run(t, reader(), opsmemory.OpDelete, `{"id":"`+gone+`"}`, true); err != nil {
		t.Fatalf("memory delete: %v", err)
	}
	_, err = run(t, reader(), opsmemory.OpEdit, `{"id":"`+gone+`","body":"x"}`, true)
	if !errs.Has(err, errs.InTrash) {
		t.Errorf("edit of a deleted row: %v; want E_IN_TRASH", err)
	}
}

// TestUnattachedNoteNamesTheReadByID (devco:D-923): a write to an org
// store's folder this repo does not attach, given the read that reaches
// the row (a row move's `lore show --store <folder> --id <id>`), says the
// row leaves this repo's lists, that every command by id still finds it,
// and prints that read; any other write keeps the store add form only.
func TestUnattachedNoteNamesTheReadByID(t *testing.T) {
	t.Parallel()
	const read = "lore show --store acme/billing --id mem_01a2079feafc74e5a2bd3ef9e701bb3a"
	var b bytes.Buffer
	unattachedNote(&b, "acme", "acme/billing", "billing", read)
	for _, want := range []string{"lists here will not show its rows", "every command by id still finds a row there", "--store acme/billing reads the folder: " + read + ";", "--folder billing --yes"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("note misses %q: %s", want, b.String())
		}
	}
	b.Reset()
	for _, other := range []string{"", "lore memory list --store acme/billing"} {
		unattachedNote(&b, "acme", "acme/billing", "billing", other)
		if strings.Contains(b.String(), "every command by id") {
			t.Errorf("a note for a write that names no row by id (read %q) speaks of one: %s", other, b.String())
		}
		b.Reset()
	}
}
