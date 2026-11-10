package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
)

// getRow reads a stored row by id through the App.
func getRow(t *testing.T, h *opstest.H, id string) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{ID: aid})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// labelOf is the name label a row's meta entry for keyID carries ("" none).
func labelOf(t *testing.T, r row.Row, keyID string) string {
	t.Helper()
	for k, v := range r.Meta {
		if k.String() != keyID {
			continue
		}
		e, err := meta.DecodeEntry(v)
		if err != nil {
			t.Fatal(err)
		}
		return e.Name
	}
	return ""
}

// TestMetaRenameRewriteRows pins METADATA 3.12 rename --rewrite-rows: the
// label is rewritten on every live row using the key, in one batch, with
// hlc / updated_at / updated_by untouched; a row without the key and a
// trashed row are left alone; the count names only changed rows; plain
// rename leaves labels to refresh later.
func TestMetaRenameRewriteRows(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	kid := h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"clnt","type":"text"}`))
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["clnt=acme"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["clnt=beta"]}`))
	c := h.ID(h.MustYes(memAdd, `{"body":"c"}`))
	d := h.ID(h.MustYes(memAdd, `{"body":"d","meta":["clnt=gone"]}`))
	h.MustYes("memory.delete", `{"id":"`+d+`"}`)
	beforeA, beforeC := getRow(t, h, a), getRow(t, h, c)

	// plain rename: registry only, labels stay
	h.MustYes(settings.OpMetaRename, `{"id":"clnt","new_name":"customer"}`)
	if got := labelOf(t, getRow(t, h, a), kid); got != "clnt" {
		t.Fatalf("plain rename rewrote the label: %q", got)
	}
	// --rewrite-rows: a and b relabelled, one batch
	out := opstest.Text(h.MustYes(settings.OpMetaRename, `{"id":"customer","new_name":"client","rewrite_rows":true}`))
	if !strings.Contains(out, "rewrote 2 row labels") || strings.Count(out, "batch bat_") != 1 {
		t.Fatalf("rewrite output %q", out)
	}
	for _, id := range []string{a, b} {
		if got := labelOf(t, getRow(t, h, id), kid); got != "client" {
			t.Errorf("%s label %q", id, got)
		}
	}
	afterA := getRow(t, h, a)
	if afterA.HLC != beforeA.HLC || !afterA.UpdatedAt.Equal(beforeA.UpdatedAt) || afterA.UpdatedBy != beforeA.UpdatedBy {
		t.Errorf("label-only write moved the audit fields: %v/%v → %v/%v", beforeA.HLC, beforeA.UpdatedAt, afterA.HLC, afterA.UpdatedAt)
	}
	if got := getRow(t, h, c); got.HLC != beforeC.HLC || len(got.Meta) != 0 {
		t.Errorf("row without the key touched")
	}
	if got := labelOf(t, getRow(t, h, d), kid); got == "client" {
		t.Errorf("trashed row relabelled: %q", got)
	}
	// values unchanged
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+b+`"}`)); got != "client\tbeta\n" {
		t.Errorf("value after rewrite %q", got)
	}
	// again with the labels already right: nothing to rewrite
	out = opstest.Text(h.MustYes(settings.OpMetaRename, `{"id":"client","new_name":"client2","rewrite_rows":true}`))
	if !strings.Contains(out, "rewrote 2 row labels") {
		t.Errorf("second rewrite %q", out)
	}
	h.Fails(settings.OpMetaRename, `{"id":"client2","new_name":"Bad-Name","rewrite_rows":true}`, errs.MetaKey)
}

// TestMetaFoldWithValues pins OV21: --merge-into on keys with allowed
// values moves each row's value by name (the survivor gains the union of
// the names), the survivor takes the protective flags, the old key is
// retired; folding a store-level key into a repo-level key works the same.
func TestMetaFoldWithValues(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)
	h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","values":"gold,silver"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"level","type":"text","values":"silver,bronze","to":"kb","sensitive":true}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["level=bronze"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["level=silver","tier=silver"]}`))
	fold := opstest.Text(h.MustYes(settings.OpMetaEdit, `{"id":"level","merge_into":"tier"}`))
	if !strings.Contains(fold, "2 rows rewritten") {
		t.Fatalf("fold %q", fold)
	}
	for id, want := range map[string]string{a: "tier\t•••\n", b: "tier\t•••\n"} {
		if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); got != want {
			t.Errorf("%s after fold %q, want %q (sensitive carried to the survivor)", id, got, want)
		}
	}
	keys := h.JSON(h.Must(settings.OpMetaKeys, `{"keys":["tier"]}`))
	for _, v := range []string{"gold", "silver", "bronze"} {
		if !strings.Contains(keys, v) {
			t.Errorf("survivor lacks value %s: %s", v, keys)
		}
	}
	h.Fails(settings.OpMetaEdit, `{"id":"level","merge_into":"tier"}`, errs.MetaRetired)
}

// TestMetaBulkListKeyAndJSONLines pins OV21: meta set --where on a list key
// matches any element, and a JSON-lines import writes every record in one
// batch (an unknown field refuses the whole file).
func TestMetaBulkListKeyAndJSONLines(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"pages","type":"list"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["pages=invoices,billing"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["pages=billing"]}`))
	c := h.ID(h.MustYes(memAdd, `{"body":"c","meta":["pages=home"]}`))
	set := opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"owner=finance","where":["pages=billing"]}`))
	if !strings.Contains(set, "on 2 rows; batch bat_") {
		t.Fatalf("bulk set on a list key %q", set)
	}
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+c+`"}`)); strings.Contains(got, "owner") {
		t.Errorf("non-matching row got owner: %q", got)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "m.jsonl")
	body := `{"id":"` + a + `","key":"owner","value":"ops"}` + "\n" + `{"id":"` + b + `","key":"owner","value":"sales"}` + "\n"
	if err := os.WriteFile(good, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	imp := opstest.Text(h.MustYes(settings.OpMetaImport, `{"file":"`+filepath.ToSlash(good)+`"}`))
	if batches := batchIDs.FindAllString(imp, -1); len(batches) == 0 || !allSame(batches) {
		t.Errorf("import used more than one batch: %q", imp)
	}
	for id, want := range map[string]string{a: "owner\tops", b: "owner\tsales"} {
		if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); !strings.Contains(got, want) {
			t.Errorf("%s after import %q", id, got)
		}
	}
	bad := filepath.Join(dir, "bad.ndjson")
	if err := os.WriteFile(bad, []byte(`{"id":"`+a+`","key":"owner","value":"x","extra":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Fails(settings.OpMetaImport, `{"file":"`+filepath.ToSlash(bad)+`"}`, errs.InvalidInput)
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+a+`"}`)); !strings.Contains(got, "owner\tops") {
		t.Errorf("a refused import wrote: %q", got)
	}
}

// batchIDs matches a batch id in command output.
var batchIDs = regexp.MustCompile(`bat_[0-9a-f]{32}`)

func allSame(xs []string) bool {
	for _, x := range xs {
		if x != xs[0] {
			return false
		}
	}
	return true
}

// TestMetaGetMasksRegistrySensitive pins METADATA 3.20 "one masker":
// masking is decided by the entry's label or the registry's flag,
// whichever says so. A row whose entry predates the key turning sensitive
// (or whose entry kept its old flags through a fold) must still print •••.
func TestMetaGetMasksRegistrySensitive(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_host","type":"text"}`)
	id := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["vpn_host=vpn-7-probe"]}`))
	h.MustYes(settings.OpMetaEdit, `{"id":"vpn_host","sensitive":true}`)
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); strings.Contains(got, "vpn-7-probe") {
		t.Errorf("meta get after the key turned sensitive: %q", got)
	}
	// every read path of ops/settings, text and --json (the class, not
	// one instance): get, show, export csv + json, lint (a rule the value
	// breaks would quote it), the set echo
	h.MustYes(settings.OpMetaEdit, `{"id":"vpn_host","pattern":"^zz"}`)
	reads := []struct{ name, in string }{
		{settings.OpMetaGet, `{"id":"` + id + `"}`},
		{settings.OpMetaShow, `{"id":"` + id + `"}`},
		{settings.OpMetaExport, `{}`},
		{settings.OpMetaExport, `{"format":"json"}`},
		{settings.OpMetaLint, `{}`},
	}
	for _, r := range reads {
		res := h.Must(r.name, r.in)
		if got := opstest.Text(res) + h.JSON(res); strings.Contains(got, "vpn-7-probe") {
			t.Errorf("%s %s leaks the registry-sensitive value: %s", r.name, r.in, got)
		}
	}
	echo := opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"`+id+`","pair":"vpn_host=vpn-8-probe"}`))
	if strings.Contains(echo, "vpn-8-probe") || !strings.Contains(echo, "•••") {
		t.Errorf("set echo %q", echo)
	}
	// a strict sensitive key refuses without quoting the value
	h.MustYes(settings.OpMetaEdit, `{"id":"vpn_host","strict":true}`)
	if msg := h.Fails(settings.OpMetaSet, `{"id":"`+id+`","pair":"vpn_host=vpn-10-probe"}`, errs.MetaValue); strings.Contains(msg, "vpn-10-probe") {
		t.Errorf("strict refusal leaks: %q", msg)
	}
	// an entry-flag sensitive value breaking a rule: lint masks it too
	h.MustYes(settings.OpMetaAdd, `{"name":"token_hint","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaSet, `{"id":"`+id+`","pair":"token_hint=vpn-9-probe"}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"token_hint","values":"a,b"}`)
	res := h.Must(settings.OpMetaLint, `{}`)
	if got := opstest.Text(res) + h.JSON(res); strings.Contains(got, "vpn-9-probe") || !strings.Contains(got, "token_hint") {
		t.Errorf("lint on a sensitive key: %s", got)
	}
}

// TestMetaHistoryFolderStore pins OV62: meta history needs git history; on
// a plain folder store it is E_UNSUPPORTED.
func TestMetaHistoryFolderStore(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	id := h.ID(h.MustYes(memAdd, `{"body":"in the folder store","to":"kb","meta":["client=acme"]}`))
	h.Fails(settings.OpMetaHistory, `{"id":"`+id+`"}`, errs.Unsupported)
	_ = store.SourceRepo
}

// TestMetaAdoptWithValues pins OV58: adopt moves a place's key rows with
// their allowed values (the moved key still takes its values by name).
func TestMetaAdoptWithValues(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)
	h.MustYes(settings.OpMetaAdd, `{"name":"env","type":"text","values":"prod,dev","to":"kb/web"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["env=prod"]}`))
	out := h.JSON(h.MustYes(settings.OpMetaAdopt, `{"from":"kb/web","to":"kb/org"}`))
	if !strings.Contains(out, `"env"`) {
		t.Fatalf("adopt %s", out)
	}
	keys := h.JSON(h.Must(settings.OpMetaKeys, `{"keys":["env"]}`))
	if !strings.Contains(keys, `"store":"kb/org"`) || !strings.Contains(keys, "prod") || !strings.Contains(keys, "dev") {
		t.Errorf("after adopt %s", keys)
	}
	h.MustYes(memAdd, `{"body":"b","meta":["env=dev"]}`)
	h.Fails(memAdd, `{"body":"c","meta":["env=staging"]}`, errs.MetaValue)
	if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+a+`"}`)); got != "env\tprod\n" {
		t.Errorf("value after adopt %q", got)
	}
}
