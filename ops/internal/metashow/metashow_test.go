package metashow_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
)

// nounCase is one knowledge noun: its add input (with the search word in
// a searched field) and whether its search finds that field.
type nounCase struct {
	noun string
	add  func(word string) string
}

func titled(word string) string {
	return `"title":` + opstest.Quote("t "+word) + `,"body":` + opstest.Quote("body "+word)
}

func named(word string) string {
	return `"name":` + opstest.Quote("n "+word) + `,"body":` + opstest.Quote("body "+word)
}

func bodied(word string) string { return `"body":` + opstest.Quote("body "+word) }

// nouns is every knowledge noun whose show/list/search carry metadata
// (G-CONTENT-3); handoff's add needs the person it is for (forActor).
func nouns(forActor string) []nounCase {
	return []nounCase{
		{"rule", bodied}, {"memory", bodied}, {"decision", titled}, {"hotfix", titled},
		{"pattern", titled}, {"playbook", titled}, {"prompt", titled}, {"incident", titled},
		{"architecturenote", titled}, {"cookbookrecipe", titled}, {"suggestion", titled},
		{"snapshot", titled}, {"behaviour", named}, {"workflow", named}, {"workspace", named},
		{"tastepref", bodied},
		{"techdoc", func(word string) string {
			return `"name":` + opstest.Quote("n-"+word) + `,"base_url":"https://docs.example","description":` + opstest.Quote("about "+word)
		}},
		{"handoff", func(word string) string { return bodied(word) + `,"for":"` + forActor + `"` }},
	}
}

// metaOf reads a JSON object's "meta" and "meta_masked" (present tells
// whether the "meta" key exists at all).
func metaOf(t *testing.T, obj map[string]json.RawMessage) (meta map[string]string, masked []string, present bool) {
	t.Helper()
	raw, ok := obj["meta"]
	if !ok {
		_, hasMasked := obj["meta_masked"]
		if hasMasked {
			t.Fatal("meta_masked without meta")
		}
		return nil, nil, false
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("meta is not a name: value object: %s", raw)
	}
	if m, ok := obj["meta_masked"]; ok {
		if err := json.Unmarshal(m, &masked); err != nil {
			t.Fatal(err)
		}
	}
	return meta, masked, true
}

// objects decodes a result's "data" (an object or a list of objects).
func objects(t *testing.T, js string) []map[string]json.RawMessage {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(js), &env); err != nil {
		t.Fatalf("not a JSON envelope: %s", js)
	}
	var many []map[string]json.RawMessage
	if json.Unmarshal(env.Data, &many) == nil {
		return many
	}
	var one map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &one); err != nil {
		t.Fatalf("data is neither an object nor a list: %s", env.Data)
	}
	return []map[string]json.RawMessage{one}
}

// byID picks the object whose "id" is id.
func byID(t *testing.T, objs []map[string]json.RawMessage, id string) map[string]json.RawMessage {
	t.Helper()
	for _, o := range objs {
		var got string
		if json.Unmarshal(o["id"], &got) == nil && got == id {
			return o
		}
	}
	t.Fatalf("no object with id %s", id)
	return nil
}

// TestMetadataOnEveryRead (MQ13, METADATA 3.17/3.20, G-CONTENT-3): for
// every knowledge noun, a row with metadata carries it by name on show
// (data.meta and a sorted "meta:" text block), on its row of list --json
// and on its search hit; a sensitive value is masked whether the key was
// sensitive when the value was written (the entry's label) or turned
// sensitive afterwards (the key row's flag), and is never printed in clear;
// a row without metadata carries no meta / meta_masked keys and no text
// block, so its envelope is the v0.1.17 one.
func TestMetadataOnEveryRead(t *testing.T) {
	h := opstest.New(t)
	h.Must("meta.add", `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.Must("meta.add", `{"name":"late_secret","type":"text"}`)
	me := h.Seed(schema.TableActors, map[string]string{"display_name": `"Pat"`, "stable_key": `"human:pat@example.test"`})
	const secret, late = "10.0.0.1", "s3cr3t-later"
	type made struct{ noun, plain, withMeta string }
	var rows []made
	for _, c := range nouns(me) {
		plainIn := `{` + c.add("plainword"+c.noun) + `}`
		metaIn := `{` + c.add("metaword"+c.noun) + `,"meta":["zeta=last","jira=HUB-1","vpn_host=` + secret + `","late_secret=` + late + `"]}`
		rows = append(rows, made{c.noun, h.ID(h.Must(c.noun+".add", plainIn)), h.ID(h.Must(c.noun+".add", metaIn))})
	}
	// late_secret turns sensitive after its values were written: entries
	// carry no sensitive label, only the key row's flag says so.
	h.MustYes("meta.edit", `{"id":"late_secret","sensitive":true}`)

	wantMeta := map[string]string{"jira": "HUB-1", "zeta": "last", "vpn_host": "•••", "late_secret": "•••"}
	wantMasked := []string{"late_secret", "vpn_host"}
	check := func(t *testing.T, where string, obj map[string]json.RawMessage, withMeta bool) {
		t.Helper()
		meta, masked, present := metaOf(t, obj)
		if !withMeta {
			if present {
				t.Fatalf("%s: a row without metadata carries meta: %v", where, meta)
			}
			return
		}
		if !present || len(meta) != len(wantMeta) {
			t.Fatalf("%s: meta %v, want %v", where, meta, wantMeta)
		}
		for k, v := range wantMeta {
			if meta[k] != v {
				t.Fatalf("%s: meta %s = %q, want %q", where, k, meta[k], v)
			}
		}
		if !slices.Equal(masked, wantMasked) {
			t.Fatalf("%s: meta_masked %v, want %v", where, masked, wantMasked)
		}
	}
	for _, r := range rows {
		t.Run(r.noun, func(t *testing.T) {
			for _, c := range []struct {
				id       string
				withMeta bool
				word     string
			}{{r.plain, false, "plainword" + r.noun}, {r.withMeta, true, "metaword" + r.noun}} {
				show := h.Must(r.noun+".show", `{"id":"`+c.id+`"}`)
				showJSON := h.JSON(show)
				check(t, "show", objects(t, showJSON)[0], c.withMeta)
				text := opstest.Text(show)
				block := "meta:\n  jira: HUB-1\n  late_secret: •••   (sensitive)\n  vpn_host: •••   (sensitive)\n  zeta: last\n"
				if c.withMeta != strings.Contains(text, block) {
					t.Fatalf("show text (meta %v) block mismatch:\n%s", c.withMeta, text)
				}
				if !c.withMeta && strings.Contains(text, "meta:") {
					t.Fatalf("show text of a row without metadata has a meta block:\n%s", text)
				}
				listJSON := h.JSON(h.Must(r.noun+".list", `{}`))
				check(t, "list", byID(t, objects(t, listJSON), c.id), c.withMeta)
				searchJSON := h.JSON(h.Must(r.noun+".search", `{"query":"`+c.word+`"}`))
				check(t, "search", byID(t, objects(t, searchJSON), c.id), c.withMeta)
				for _, out := range []string{showJSON, text, listJSON, searchJSON} {
					if strings.Contains(out, secret) || strings.Contains(out, late) {
						t.Fatalf("a sensitive value was printed in clear:\n%s", out)
					}
				}
			}
		})
	}
}
