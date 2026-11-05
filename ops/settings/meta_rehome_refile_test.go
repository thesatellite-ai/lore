package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaRehomeRefile (later 32, FA-META-13, METADATA 3.18): after the
// repo stops seeing the store folder that defined its rows' keys, meta
// rehome asks first (naming the counts, nothing written); --dry-run lists
// the missing key ids and plans the re-file (a
// visible key of the same name and type is reused, any other key is new
// in --to) and writes nothing; the real run re-files every value in one
// batch, after which every key the rows use is visible and the values
// read the same.
func TestMetaRehomeRefile(t *testing.T) {
	h := multiStore(t, ownRepo) // an own repo takes store keys' values (FA-META-7)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text","to":"kb/web"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"env","type":"text","to":"kb/web"}`)
	m1 := h.ID(h.MustYes(memAdd, `{"body":"one","meta":["client=acme","env=prod"]}`))
	m2 := h.ID(h.MustYes(memAdd, `{"body":"two","meta":["client=globex"]}`))
	reuse := h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text","to":"kb/org"}`))

	// the repo no longer attaches kb/web
	yaml, err := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(attach.FileName)))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	lines := strings.Split(string(yaml), "\n")
	for i := 0; i < len(lines); i++ {
		if strings.Contains(lines[i], "name: kb/web") {
			i += 2 // drop its url and folder lines
			continue
		}
		kept = append(kept, lines[i])
	}
	if err := os.WriteFile(filepath.Join(h.Repo, filepath.FromSlash(attach.FileName)), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	h = h.Reopen(ownRepo)

	decode := func(s string) settings.MetaRehomeOut {
		var o settings.MetaRehomeOut
		if err := json.Unmarshal([]byte(s), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	keysBefore := h.JSON(h.Must(settings.OpMetaKeys, `{}`))
	getBefore := h.JSON(h.Must(settings.OpMetaGet, `{"id":"`+m1+`"}`))
	// without --yes it asks, naming the counts, and writes nothing
	if msg := h.Fails(settings.OpMetaRehome, `{"to":"kb"}`, errs.NeedsConfirm); !strings.Contains(msg, "re-file 2 keys and rewrite 3 values") {
		t.Fatalf("question %q", msg)
	}

	dry := decode(h.JSON(h.Must(settings.OpMetaRehome, `{"to":"kb","dry_run":true}`)))
	if len(dry.Missing) != 3 { // m1 client, m1 env, m2 client
		t.Fatalf("dry run missing: %+v", dry.Missing)
	}
	plan := strings.Join(dry.Plan, "\n")
	if !dry.DryRun || dry.Rewritten != 0 || len(dry.Plan) != 2 || !strings.Contains(plan, "client") || !strings.Contains(plan, reuse+" in ") ||
		!strings.Contains(plan, "(reused)") || !strings.Contains(plan, "env") || !strings.Contains(plan, "a new text key in") {
		t.Fatalf("dry run: %+v", dry)
	}
	if h.JSON(h.Must(settings.OpMetaKeys, `{}`)) != keysBefore || h.JSON(h.Must(settings.OpMetaGet, `{"id":"`+m1+`"}`)) != getBefore {
		t.Fatal("the dry run wrote")
	}

	done := decode(h.JSON(h.MustYes(settings.OpMetaRehome, `{"to":"kb"}`)))
	if done.Rewritten != 2 || done.Batch == "" {
		t.Fatalf("re-file: %+v", done)
	}
	if again := decode(h.JSON(h.Must(settings.OpMetaRehome, `{}`))); len(again.Missing) != 0 {
		t.Fatalf("still missing after the re-file: %+v", again.Missing)
	}
	for id, want := range map[string]string{m1: "client\tacme\nenv\tprod\n", m2: "client\tglobex\n"} {
		if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); got != want {
			t.Errorf("%s values %q, want %q", id, got, want)
		}
	}
	if keys := h.JSON(h.Must(settings.OpMetaKeys, `{"keys":["env"]}`)); !strings.Contains(keys, `"store":"kb"`) {
		t.Errorf("env key not new in kb: %s", keys)
	}
}
