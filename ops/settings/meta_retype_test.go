package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// retypeKey is the key every retype test converts.
const retypeKey = "rt_key"

// retypedJSON is the stored JSON of retypeKey on row id ("" when the row
// has none).
func retypedJSON(t *testing.T, h *opstest.H, id string) string {
	t.Helper()
	var out settings.MetaGetOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpMetaGet, `{"id":`+opstest.Quote(id)+`}`))), &out); err != nil {
		t.Fatal(err)
	}
	for _, v := range out.Data {
		if v.Key == retypeKey {
			return v.JSON
		}
	}
	return ""
}

// retypedKey is retypeKey as meta keys --json lists it.
func retypedKey(t *testing.T, h *opstest.H) settings.MetaKey {
	t.Helper()
	var out settings.MetaKeysOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpMetaKeys, `{}`))), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range out.Data {
		if k.Name == retypeKey {
			return k
		}
	}
	t.Fatalf("no key %s", retypeKey)
	return settings.MetaKey{}
}

// typeHistory is the type_history of the repo key row named name.
func typeHistory(t *testing.T, h *opstest.H, name string) []map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", "meta_keys", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, rerr := os.ReadFile(filepath.Clean(f))
		if rerr != nil {
			t.Fatal(rerr)
		}
		var row struct {
			Name string              `json:"name"`
			Hist []map[string]string `json:"type_history"`
		}
		if uerr := json.Unmarshal(b, &row); uerr != nil {
			t.Fatal(uerr)
		}
		if row.Name == name {
			return row.Hist
		}
	}
	t.Fatalf("no key row %s", name)
	return nil
}

// retypeIn is a meta.edit input with --type and extra JSON members.
func retypeIn(key, typ, extra string) string {
	in := `{"id":` + opstest.Quote(key) + `,"type":` + opstest.Quote(typ)
	if extra != "" {
		in += "," + extra
	}
	return in + "}"
}

// seedKey registers retypeKey as typ and writes one memory per value.
func seedKey(t *testing.T, h *opstest.H, typ string, vals ...string) []string {
	t.Helper()
	h.MustYes(settings.OpMetaAdd, `{"name":"`+retypeKey+`","type":"`+typ+`"}`)
	ids := make([]string, 0, len(vals))
	for i, v := range vals {
		ids = append(ids, h.ID(h.MustYes(memAdd, `{"body":"row `+strconv.Itoa(i)+`","meta":[`+opstest.Quote(retypeKey+"="+v)+`]}`)))
	}
	return ids
}

// TestMetaRetypeConversionMatrix (METADATA 3.3, R12, G-META-7): each
// source type × target type, one value per case; a value that fits is
// stored in the target's canonical JSON, one that does not fit refuses
// the whole call (E_META_TYPE naming the row) and nothing changes: the
// key keeps its type and the row keeps its value.
func TestMetaRetypeConversionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, val, to string
		want          string // canonical JSON after; "" = does not fit
	}{
		{"text", "3", "number", `3`},
		{"text", "2.5", "number", `2.5`},
		{"text", "about 4", "number", ""},
		{"text", "true", "bool", `true`},
		{"text", "maybe", "bool", ""},
		{"text", "2026-01-02", "date", `"2026-01-02"`},
		{"text", "soon", "date", ""},
		{"text", "a@example.com", "email", `"a@example.com"`},
		{"text", "not mail", "email", ""},
		{"text", "https://example.com/p", "url", `"https://example.com/p"`},
		{"text", "10.0.0.1", "ip", `"10.0.0.1"`},
		{"text", "host name", "ip", ""},
		{"text", "a", "list", `["a"]`},
		{"text", "x", "json", `"x"`},
		{"number", "3", "text", `"3"`},
		{"number", "3", "bool", ""},
		{"number", "3", "json", `3`},
		{"bool", "true", "text", `"true"`},
		{"date", "2026-01-02", "text", `"2026-01-02"`},
		{"list", "a,b", "text", `"a,b"`},
		{"json", `{"a":1}`, "text", `"{\"a\":1}"`},
		{"json", `{"a":1}`, "number", ""},
	}
	for _, c := range cases {
		t.Run(c.from+"_to_"+c.to+"_"+strings.ReplaceAll(c.val, " ", "_"), func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			id := seedKey(t, h, c.from, c.val)[0]
			before := retypedJSON(t, h, id)
			if c.want == "" {
				_, err := h.RunYes(settings.OpMetaEdit, retypeIn(retypeKey, c.to, ""))
				if !errs.Has(err, errs.MetaType) || !strings.Contains(err.Error(), id) {
					t.Fatalf("misfit with --yes: %v (want E_META_TYPE naming %s)", err, id)
				}
				if k := retypedKey(t, h); k.Type != c.from {
					t.Errorf("key type %s after a refused retype", k.Type)
				}
				if got := retypedJSON(t, h, id); got != before {
					t.Errorf("row value %s -> %s after a refused retype", before, got)
				}
				return
			}
			h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, c.to, ""))
			if k := retypedKey(t, h); k.Type != c.to {
				t.Errorf("key type %s, want %s", k.Type, c.to)
			}
			if got := retypedJSON(t, h, id); got != c.want {
				t.Errorf("value %s -> %s, want %s", before, got, c.want)
			}
		})
	}
}

// TestMetaRetypeRefusals (G-META-7): each refusal answers its code and
// writes nothing (the key keeps its type and every value).
func TestMetaRetypeRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(h *opstest.H)
		in    string
		code  errs.Code
	}{
		{"same type", nil, retypeIn(retypeKey, "text", ""), errs.InvalidInput},
		{"unknown type word", nil, retypeIn(retypeKey, "blob", ""), errs.MetaType},
		{"list with an item type", nil, retypeIn(retypeKey, "list<number>", ""), errs.MetaType},
		{"null", nil, retypeIn(retypeKey, "null", ""), errs.MetaType},
		{"ref target", nil, retypeIn(retypeKey, "ref", ""), errs.MetaType},
		{"secret_ref target", nil, retypeIn(retypeKey, "secret_ref", ""), errs.MetaType},
		{"with --strict", nil, retypeIn(retypeKey, "number", `"strict":true`), errs.InvalidInput},
		{"with --label", nil, retypeIn(retypeKey, "number", `"label":"Effort"`), errs.InvalidInput},
		{"with --values", nil, retypeIn(retypeKey, "number", `"values":"1,2"`), errs.InvalidInput},
		{"with --merge-into", func(h *opstest.H) { h.MustYes(settings.OpMetaAdd, `{"name":"other","type":"number"}`) },
			retypeIn(retypeKey, "number", `"merge_into":"other"`), errs.InvalidInput},
		{"--dry-run alone", nil, `{"id":"` + retypeKey + `","dry_run":true}`, errs.InvalidInput},
		{"--keep-as-text alone", nil, `{"id":"` + retypeKey + `","keep_as_text":true}`, errs.InvalidInput},
		{"--all alone", nil, `{"id":"` + retypeKey + `","all":true}`, errs.InvalidInput},
		{"bounds do not fit", func(h *opstest.H) {
			h.MustYes(settings.OpMetaAdd, `{"name":"bounded","type":"number"}`)
			h.MustYes(settings.OpMetaEdit, `{"id":"bounded","min":"1","max":"9"}`)
		}, retypeIn("bounded", "text", ""), errs.Code("")},
		{"default does not fit", func(h *opstest.H) {
			h.MustYes(settings.OpMetaEdit, `{"id":"`+retypeKey+`","default":"abc"}`)
		}, retypeIn(retypeKey, "number", ""), errs.MetaType},
		{"allowed values", func(h *opstest.H) {
			h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","values":"low,high"}`)
		}, retypeIn("tier", "number", ""), errs.MetaType},
		{"allowed values in use", func(h *opstest.H) {
			h.MustYes(settings.OpMetaAdd, `{"name":"tier2","type":"text","values":"low,high"}`)
			h.MustYes(memAdd, `{"body":"tiered","meta":["tier2=low"]}`)
		}, retypeIn("tier2", "list", ""), errs.MetaType},
		{"merged key", func(h *opstest.H) {
			h.MustYes(settings.OpMetaAdd, `{"name":"survivor","type":"text"}`)
			h.MustYes(settings.OpMetaEdit, `{"id":"`+retypeKey+`","merge_into":"survivor"}`)
		}, retypeIn(retypeKey, "number", ""), errs.MetaRetired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			id := seedKey(t, h, "text", "3")[0]
			if c.setup != nil {
				c.setup(h)
			}
			before, typ := retypedJSON(t, h, id), retypedKey(t, h).Type
			_, err := h.RunYes(settings.OpMetaEdit, c.in)
			if err == nil {
				t.Fatalf("%s: no error", c.in)
			}
			if c.code != "" && !errs.Has(err, c.code) {
				t.Fatalf("%s: %v, want %s", c.in, err, c.code)
			}
			if got := retypedKey(t, h).Type; got != typ {
				t.Errorf("key type %s -> %s after a refusal", typ, got)
			}
			if got := retypedJSON(t, h, id); got != before {
				t.Errorf("row value %s -> %s after a refusal", before, got)
			}
		})
	}
}

// TestMetaRetypeModes (G-META-7): without --yes it asks and writes
// nothing; --dry-run reports what converts and what does not fit and
// writes nothing; --keep-as-text converts the rows that fit and leaves the
// misfits' values untouched; --all lists every pair (the default lists 5).
func TestMetaRetypeModes(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	vals := []string{"1", "2", "3", "4", "5", "6", "7", "about 8"}
	ids := seedKey(t, h, "text", vals...)
	misfit := ids[len(ids)-1]
	unchanged := func(what string) {
		t.Helper()
		if k := retypedKey(t, h); k.Type != "text" {
			t.Fatalf("%s: key type %s", what, k.Type)
		}
		for i, id := range ids {
			if got := retypedJSON(t, h, id); got != opstest.Quote(vals[i]) {
				t.Fatalf("%s: row %d = %s", what, i, got)
			}
		}
	}

	h.Fails(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"keep_as_text":true`), errs.NeedsConfirm)
	unchanged("no --yes")

	dry := opstest.Text(h.Must(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"dry_run":true,"keep_as_text":true`)))
	for _, want := range []string{"text → number", "7 values convert", `"1" → 1`, "--all lists every one", "1 values do not fit", misfit, "dry run: nothing written"} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry run lacks %q:\n%s", want, dry)
		}
	}
	if strings.Contains(dry, `"6" → 6`) {
		t.Errorf("dry run without --all lists more than 5 pairs:\n%s", dry)
	}
	unchanged("--dry-run")
	all := opstest.Text(h.Must(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"dry_run":true,"keep_as_text":true,"all":true`)))
	if !strings.Contains(all, `"7" → 7`) || strings.Contains(all, "--all lists every one") {
		t.Errorf("--all does not list every pair:\n%s", all)
	}
	unchanged("--dry-run --all")
	h.Fails(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"dry_run":true`), errs.MetaType)

	out := opstest.Text(h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"keep_as_text":true`)))
	if !strings.Contains(out, "7 values converted in batch bat_") {
		t.Errorf("convert output:\n%s", out)
	}
	if k := retypedKey(t, h); k.Type != "number" {
		t.Fatalf("key type %s after --keep-as-text", k.Type)
	}
	for i, id := range ids[:7] {
		if got := retypedJSON(t, h, id); got != vals[i] {
			t.Errorf("row %d = %s, want %s", i, got, vals[i])
		}
	}
	if got := retypedJSON(t, h, misfit); got != `"about 8"` {
		t.Errorf("misfit kept as text: %s", got)
	}
}

// TestMetaRetypeSensitiveNeverPrinted (OQ2, 3.20, G-META-7): a sensitive
// key's values reach no output of --type: not the dry run (with --all),
// not the misfit refusal, not the conversion, text or JSON.
func TestMetaRetypeSensitiveNeverPrinted(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	const fits, misfits = "48213", "hunterpass9label"
	h.MustYes(settings.OpMetaAdd, `{"name":"`+retypeKey+`","type":"text","sensitive":true}`)
	for _, v := range []string{fits, misfits} {
		h.MustYes(memAdd, `{"body":"sens","meta":[`+opstest.Quote(retypeKey+"="+v)+`]}`)
	}
	check := func(what, s string) {
		t.Helper()
		for _, v := range []string{fits, misfits} {
			if strings.Contains(s, v) {
				t.Errorf("%s prints a sensitive value:\n%s", what, s)
			}
		}
	}
	res := h.Must(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"dry_run":true,"keep_as_text":true,"all":true`))
	check("dry run --all", opstest.Text(res)+h.JSON(res))
	if !strings.Contains(opstest.Text(res), "1 values convert") {
		t.Errorf("dry run: %s", opstest.Text(res))
	}
	_, err := h.RunYes(settings.OpMetaEdit, retypeIn(retypeKey, "number", ""))
	if !errs.Has(err, errs.MetaType) {
		t.Fatalf("misfit: %v", err)
	}
	check("misfit refusal", err.Error())
	res = h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, "number", `"keep_as_text":true,"all":true`))
	check("conversion", opstest.Text(res)+h.JSON(res))
}

// TestMetaRetypeUndoAndHistory (G-META-7): each conversion appends one
// {type, until, by} entry to type_history (twice here: text → number →
// text); lore batch undo of a conversion restores the key's type and every
// value.
func TestMetaRetypeUndoAndHistory(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ids := seedKey(t, h, "text", "3", "4.5")
	res := h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, "number", ""))
	bid := h.Field(res, "batch")
	if !strings.HasPrefix(bid, "bat_") {
		t.Fatalf("no batch id in %s", h.JSON(res))
	}
	h.MustYes("batch.undo", `{"id":"`+bid+`"}`)
	if k := retypedKey(t, h); k.Type != "text" {
		t.Errorf("undo: key type %s", k.Type)
	}
	for i, want := range []string{`"3"`, `"4.5"`} {
		if got := retypedJSON(t, h, ids[i]); got != want {
			t.Errorf("undo: row %d = %s, want %s", i, got, want)
		}
	}
	if hist := typeHistory(t, h, retypeKey); len(hist) != 0 {
		t.Errorf("undo left type_history %v", hist)
	}

	h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, "number", ""))
	h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, "text", ""))
	hist := typeHistory(t, h, retypeKey)
	if len(hist) != 2 || hist[0]["type"] != "text" || hist[1]["type"] != "number" {
		t.Fatalf("type_history %v", hist)
	}
	for _, e := range hist {
		if e["until"] == "" || !strings.HasPrefix(e["by"], "act_") {
			t.Errorf("type_history entry %v lacks until/by", e)
		}
	}
	if got := retypedJSON(t, h, ids[1]); got != `"4.5"` {
		t.Errorf("round trip value %s", got)
	}
}

// TestMetaRetypeStoreKeyRowsEverywhere (G-META-7): a store key's values on
// rows in the store root and in a project folder of it are all converted
// by one call (the harness repo is a client repo, which takes no store
// key values: E_META_FOREIGN_KEY).
func TestMetaRetypeStoreKeyRowsEverywhere(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"`+retypeKey+`","type":"text","to":"kb"}`)
	repoRow := h.ID(h.MustYes(memAdd, `{"body":"in kb/web","to":"kb/web","meta":["`+retypeKey+`=7"]}`))
	storeRow := h.ID(h.MustYes(memAdd, `{"body":"in kb","to":"kb","meta":["`+retypeKey+`=8"]}`))
	out := opstest.Text(h.MustYes(settings.OpMetaEdit, retypeIn(retypeKey, "number", "")))
	if !strings.Contains(out, "2 values converted") {
		t.Fatalf("convert: %s", out)
	}
	for id, want := range map[string]string{repoRow: "7", storeRow: "8"} {
		if got := retypedJSON(t, h, id); got != want {
			t.Errorf("%s = %s, want %s", id, got, want)
		}
	}
}
