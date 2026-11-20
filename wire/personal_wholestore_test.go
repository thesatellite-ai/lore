package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestPersonalStoreReadCoversEveryFolder pins devco D-756: with the
// personal store on a git repository and notes written to two of its
// folders (`--to me/alpha`, `--to me/beta`), a read naming the whole store
// (`--store me`, no folder) answers from every folder, in a plain folder,
// in a git repo lore is not set up in and in a repo set up with lore. Before
// it the read opened the store's root alone: search found nothing, sql
// returned no row, and sql in a git repo with no .lore refused
// E_NOT_A_STORE, while `--store me/alpha` found the alpha note.
func TestPersonalStoreReadCoversEveryFolder(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	bare := filepath.Join(t.TempDir(), "me.git")
	gitIn(t, t.TempDir(), nil, "init", "-q", "--bare", "-b", "main", bare)
	plain := t.TempDir()
	a := newTestAppHome(t, plain, home)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: opsstores.PersonalName, URL: bare, RepoOptional: true}); err != nil {
		t.Fatalf("store init me on a bare repo: %v", err)
	}
	notes := map[string]string{"alpha": "zebra alpha note", "beta": "zebra beta note"}
	for folder, body := range notes {
		add, _ := a.Registry().Lookup("memory.add")
		in := `{"body":"` + body + `","to":"me/` + folder + `","create":true}`
		if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(in)}, op.Caller{Yes: true}); err != nil {
			t.Fatalf("memory add --to me/%s: %v", folder, err)
		}
	}
	untouched := t.TempDir()
	gitInitIn(t, untouched)
	setUp := t.TempDir()
	gitInitIn(t, setUp)
	// a repo lore is set up in (it has a .lore folder)
	if err := os.MkdirAll(filepath.Join(setUp, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	reads := []struct{ name, op, in string }{
		{"search --store me zebra", opssearch.OpRun, `{"query":"zebra","store":"me"}`},
		{"sql run --store me", opssql.OpRun, `{"query":"SELECT source, body FROM memories_all ORDER BY body","store":"me"}`},
		{"memory list --store me", "memory.list", `{"store":"me"}`},
	}
	for _, place := range []struct{ name, dir string }{{"a plain folder", plain}, {"a git repo lore is not set up in", untouched}, {"a repo lore is set up in", setUp}} {
		for _, r := range reads {
			b := newTestAppHome(t, place.dir, home)
			e, ok := b.Registry().Lookup(r.op)
			if !ok {
				t.Fatalf("no op %s", r.op)
			}
			res, err := b.Run(ctx, e, op.JSONDecoder{Body: []byte(r.in)}, op.Caller{})
			if err != nil {
				t.Fatalf("%s in %s: %v", r.name, place.name, err)
			}
			out, jerr := res.JSON()
			if jerr != nil {
				t.Fatal(jerr)
			}
			for folder, body := range notes {
				// a search snippet marks the matched word (<b>zebra</b>), so
				// the check is on the words after it
				if !strings.Contains(string(out), strings.TrimPrefix(body, "zebra ")) {
					t.Errorf("%s in %s misses the note in me/%s (%q): %s", r.name, place.name, folder, body, out)
				}
			}
		}
	}
}
