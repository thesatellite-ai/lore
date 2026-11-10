package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/memory"
)

// v017MemoryKeys is the key order of v0.1.17's memory JSON (the ent Memory
// struct at tag v0.1.17, dbent/gen/ent/memory.go); unset optional columns
// were omitted, embedding and edges always printed.
var v017MemoryKeys = []string{"id", "created_at", "updated_at", "trust_score", "confidence", "last_accessed_at", "last_validated_at", "archived_at", "source_kind", "source_ref", "project_id", "repo_id", "kind", "body", "valid_at", "valid_until", "tx_at", "superseded_by_id", "created_by_actor_id", "validated_by_actor_id", "embedding_model_id", "embedding_dim", "embedding", "edges", "created_by", "updated_by"} // the last two: INTEG-AUDIT-1 additions

// TestMemoryJSONKeysMatchV017 (B11 port of saas/cmd/cli
// memory_json_compat_test.go TestMemoryJSONByteCompat, which compared the
// output with the lore.db ent copy that is gone in 0.1.18): memory show and
// list --json print v0.1.17's keys in its order for every row shape (source
// ref, kind, HTML and unicode body, long body, archived, superseded), the
// set columns present, embedding and edges always, the body bytes kept
// exactly, and list oldest first (v0.1.17 order).
func TestMemoryJSONKeysMatchV017(t *testing.T) {
	h := opstest.New(t)
	long := strings.Repeat("line of text\n", 200) + "end"
	html := `<b>bold</b> & "quoted" 'single' — é 漢字 😀 \ backslash`
	type shape struct {
		in   string
		must []string // keys this row must carry
		body string
	}
	enc := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	shapes := map[string]shape{
		"plain":      {`{"body":"the printer is downstairs"}`, nil, "the printer is downstairs"},
		"source ref": {`{"body":"from the PR","source":"agent-proposal","source_ref":"pr#42"}`, []string{"source_ref"}, "from the PR"},
		"kind":       {`{"body":"how to deploy","kind":"procedural"}`, []string{"kind"}, "how to deploy"},
		"html":       {`{"body":` + enc(html) + `}`, nil, html},
		"long":       {`{"body":` + enc(long) + `}`, nil, long},
	}
	ids := map[string]string{}
	var order []string
	for _, name := range []string{"plain", "source ref", "kind", "html", "long"} {
		ids[name] = h.ID(h.Must(memory.OpAdd, shapes[name].in))
		order = append(order, ids[name])
	}
	ids["archived"] = h.ID(h.Must(memory.OpAdd, `{"body":"old news"}`))
	h.Must(memory.OpArchive, `{"id":"`+ids["archived"]+`"}`)
	shapes["archived"] = shape{must: []string{"archived_at"}, body: "old news"}
	ids["superseded"] = h.ID(h.Must(memory.OpAdd, `{"body":"v1 of the fact"}`))
	shapes["superseded"] = shape{must: []string{"superseded_by_id"}, body: "v1 of the fact"}
	h.ID(h.Must(memory.OpAdd, `{"body":"v2 of the fact","supersedes":"`+ids["superseded"]+`"}`))
	order = append(order, ids["superseded"]) // archived rows are left out of a default list

	for name, id := range ids {
		raw := h.JSON(h.Must(memory.OpShow, `{"id":"`+id+`"}`))
		var env struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Kind != "memory.show" {
			t.Fatalf("%s: %v %s", name, err, raw)
		}
		got := b11Keys(t, env.Data)
		if !inOrder(got, v017MemoryKeys) {
			t.Errorf("%s: keys %v are not in v0.1.17's order %v", name, got, v017MemoryKeys)
		}
		for _, k := range append([]string{"id", "created_at", "body", "embedding", "edges", "created_by", "updated_by"}, shapes[name].must...) {
			if !contains(got, k) {
				t.Errorf("%s: key %s missing: %v", name, k, got)
			}
		}
		var row struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(env.Data, &row); err != nil || row.Body != shapes[name].body {
			t.Errorf("%s: body %q, want %q (%v)", name, row.Body, shapes[name].body, err)
		}
	}
	raw := h.JSON(h.Must(memory.OpList, ``))
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, r := range list.Data {
		pos[r.ID] = i
	}
	for i := 1; i < len(order); i++ {
		a, okA := pos[order[i-1]]
		b, okB := pos[order[i]]
		if !okA || !okB || a > b {
			t.Fatalf("list is not oldest first or misses a row: %v\n%s", order, raw)
		}
	}
}

// b11Keys lists a JSON object's keys in document order.
func b11Keys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		k, ok := tok.(string)
		if !ok {
			t.Fatalf("object key %v is not a string", tok)
		}
		out = append(out, k)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// inOrder reports whether got is a subsequence of want.
func inOrder(got, want []string) bool {
	j := 0
	for _, g := range got {
		for j < len(want) && want[j] != g {
			j++
		}
		if j == len(want) {
			return false
		}
		j++
	}
	return true
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
