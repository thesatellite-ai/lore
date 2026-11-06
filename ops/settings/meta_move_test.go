package settings_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMetaMoveAdoptRehome: meta move keeps the id and the values, asks,
// refuses a taken name and an inward move to the repo; adopt moves a
// place's key rows (refused on a name clash); rehome reports a clash
// (METADATA 3.9, 3.18, OV58).
func TestMetaMoveAdoptRehome(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo) // a client repo refuses store keys' values (FA-META-7)
	id := h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"retry_count","type":"text","values":"one,two","to":"kb/web"}`))
	h.MustYes(memAdd, `{"body":"a","meta":["retry_count=one"]}`)
	h.Fails(settings.OpMetaMove, `{"id":"retry_count","to":"kb/org"}`, errs.NeedsConfirm)
	h.Fails(settings.OpMetaMove, `{"id":"retry_count","to":"repo"}`, errs.InvalidInput)
	if got := h.JSON(h.MustYes(settings.OpMetaMove, `{"id":"retry_count","to":"kb/org"}`)); !strings.Contains(got, `"values":2`) {
		t.Fatalf("move %s", got)
	}
	keys := h.JSON(h.Must(settings.OpMetaKeys, `{"keys":["retry_count"]}`))
	if !strings.Contains(keys, id) || !strings.Contains(keys, `"store":"kb/org"`) || !strings.Contains(keys, `"scope":"org"`) || !strings.Contains(keys, `"rows":1`) {
		t.Errorf("after move %s", keys)
	}
	web := h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"retry_count","type":"text","to":"kb/web"}`))
	h.Fails(settings.OpMetaMove, `{"id":"`+id+`","to":"kb/web"}`, errs.MetaTaken)
	if got := h.JSON(h.Must(settings.OpMetaRehome, `{}`)); !strings.Contains(got, "retry_count: ") {
		t.Errorf("rehome clash %s", got)
	}
	h.Fails(settings.OpMetaAdopt, `{"from":"kb/web","to":"kb/org"}`, errs.MetaTaken)
	h.MustYes(settings.OpMetaRename, `{"id":"`+web+`","new_name":"retries"}`)
	h.Fails(settings.OpMetaAdopt, `{"from":"kb/web","to":"kb/org"}`, errs.NeedsConfirm)
	if got := h.JSON(h.MustYes(settings.OpMetaAdopt, `{"from":"kb/web","to":"kb/org"}`)); !strings.Contains(got, `"retries"`) {
		t.Errorf("adopt %s", got)
	}
}

// TestMetaLoosenAndTrashUse: --no-internal asks and writes a changed_by
// entry; a key used only by a trashed row is still used (OV58, RX7).
func TestMetaLoosenAndTrashUse(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	id := h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"margin","type":"text","to":"kb"}`))
	h.Fails(settings.OpMetaEdit, `{"id":"margin","no_internal":true}`, errs.NeedsConfirm)
	h.MustYes(settings.OpMetaEdit, `{"id":"margin","no_internal":true}`)
	kid, err := ids.Parse[ids.MetaKey](id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Store: "kb", Table: schema.TableMetaKeys, ID: kid.Any()})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := r.Fields["changed_by"].MarshalJSON()
	if err != nil || !strings.Contains(string(raw), `"field":"internal"`) {
		t.Errorf("changed_by %s %v", raw, err)
	}
	h.MustYes(settings.OpMetaAdd, `{"name":"gone","type":"text"}`)
	mem := h.ID(h.MustYes(memAdd, `{"body":"x","meta":["gone=1"]}`))
	h.MustYes("memory.delete", `{"id":"`+mem+`"}`)
	h.Fails(settings.OpMetaForget, `{"keys":"gone","confirm":true}`, errs.MetaInUse)
	if got := h.JSON(h.Must(settings.OpMetaKeys, `{"unused":true}`)); strings.Contains(got, `"gone"`) {
		t.Errorf("unused lists a key a trashed row uses: %s", got)
	}
}
