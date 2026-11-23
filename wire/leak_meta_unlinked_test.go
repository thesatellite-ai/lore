package wire

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLeakIndexRowsWithoutMeta (#138, devco:D-916): a row of an org store
// the repo is not linked to is indexed by its id and prose only: none of
// its metadata values becomes a text or a token, and an internal key row
// of such a store gives no key name. With metadata, the same row keeps
// its tokens (the date among them, which the exact-token rule then skips).
func TestLeakIndexRowsWithoutMeta(t *testing.T) {
	t.Parallel()
	const memID = "mem_01a1a67a33b7777587cca0cfc9e5d8b1"
	kid, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	entry := func(raw string) value.JSON {
		j, err := value.JSONOf([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	r := leakTestRow(t, memID, map[string]string{"body": "the billing service runs nightly at two in the morning"})
	r.Meta = map[ids.ID[ids.MetaKey]]value.JSON{
		kid:     entry(`{"name":"fact_verified_at","scope":"store","v":"2026-11-22"}`),
		hostKey: entry(`{"name":"vpn_host","scope":"store","v":"vpn-7.internal.acme.example"}`),
	}
	keyRow := leakTestRow(t, kid.String(), map[string]string{"name": "fact_verified_at"})
	keyRow.Fields["internal"] = value.BoolOf(true)

	with := leakIndexRows([]row.Row{r}, schema.TableMemories, "acme/ops", "store acme/ops", nil, true)
	if len(with) != 1 || len(with[0].Tokens) != 2 {
		t.Fatalf("with metadata: want the row with 2 tokens, got %+v", with)
	}
	without := leakIndexRows([]row.Row{r}, schema.TableMemories, "acme/ops", "store acme/ops", nil, false)
	if len(without) != 1 || without[0].ID != memID || len(without[0].Tokens) != 0 || len(without[0].Texts) != 1 {
		t.Fatalf("without metadata: want the row with its body only, got %+v", without)
	}
	for _, s := range without[0].Texts {
		if strings.Contains(s, "2026-11-22") || strings.Contains(s, "vpn-7") {
			t.Fatalf("a metadata value of an unlinked store was indexed: %q", s)
		}
	}
	if k := leakIndexRows([]row.Row{keyRow}, schema.TableMetaKeys, "acme/ops", "store acme/ops", nil, false); len(k) != 1 || len(k[0].KeyNames) != 0 {
		t.Fatalf("an internal key name of an unlinked store was indexed: %+v", k)
	}
	if k := leakIndexRows([]row.Row{keyRow}, schema.TableMetaKeys, "acme/ops", "store acme/ops", nil, true); len(k) != 1 || len(k[0].KeyNames) != 1 {
		t.Fatalf("an internal key name of a linked store was not indexed: %+v", k)
	}

	// end to end: the date passes with or without metadata; the host
	// blocks only when the store feeds its metadata; the id always blocks
	check := func(rows []leakcheck.IndexRow, text string) int {
		return len(leakcheck.Blocking(leakcheck.Check([]leakcheck.Item{{Where: "main.go", Path: "main.go", Content: []byte(text)}}, leakcheck.NewIndex(rows, nil), leakcheck.Options{})))
	}
	if n := check(with, "// written before 2026-11-22, when the retry loop moved"); n != 0 {
		t.Fatalf("a plain date blocked (%d findings)", n)
	}
	if n := check(with, "// connect through vpn-7.internal.acme.example"); n != 1 {
		t.Fatalf("a host of a linked store's metadata: %d findings, want 1", n)
	}
	if n := check(without, "// connect through vpn-7.internal.acme.example"); n != 0 {
		t.Fatalf("a host of an unlinked store's metadata blocked (%d findings)", n)
	}
	if n := check(without, "// see "+memID); n != 1 {
		t.Fatalf("the id of an unlinked store's row: %d findings, want 1", n)
	}
}

// TestLeakScopeNoMetaIsEveryUnlinkedOrgFolder (#138): in a client repo the
// scope names every org-store folder (attached or extra) that no link of
// the repo points at, so the index reads their ids and text but not their
// metadata values; a linked folder and a store outside the org (the
// personal store) are not named.
func TestLeakScopeNoMetaIsEveryUnlinkedOrgFolder(t *testing.T) {
	home := t.TempDir()
	fakeClone(t, home, scopeInternalURL, "org", "client_ops", "billing")
	fakeClone(t, home, scopeSharedURL, "client_app")
	a := &App{opts: Options{Home: home}}
	attached := []attach.Store{
		{Name: "acme", URL: scopeSharedURL, Folder: "client_app"},
		{Name: "acme/org", URL: scopeInternalURL, Folder: "org"},
		{Name: "me", URL: "file:///tmp/personal-store", Folder: ""},
	}
	names := func(m map[store.Source]bool) []string {
		var out []string
		for k := range m {
			out = append(out, string(k))
		}
		slices.Sort(out)
		return out
	}
	// linked to the shared store only: every internal folder is unlinked
	s := &orgcfg.Situation{Result: orgresolve.Result{Org: scopeOrg(), Links: []orgresolve.Link{scopeLink(t, scopeSharedURL, "client_app", true)}}}
	sc := a.leakScopeOf(s, true, attached)
	if got, want := names(sc.NoMeta), []string{"acme/billing", "acme/client_ops", "acme/org"}; !slices.Equal(got, want) {
		t.Fatalf("no-meta %v, want %v", got, want)
	}
	// a second link to client_ops: that folder feeds its metadata again
	s.Result.Links = append(s.Result.Links, scopeLink(t, scopeInternalURL, "client_ops", false))
	sc = a.leakScopeOf(s, true, attached)
	if got, want := names(sc.NoMeta), []string{"acme/billing", "acme/org"}; !slices.Equal(got, want) {
		t.Fatalf("with a link to client_ops: no-meta %v, want %v", got, want)
	}
	if own := a.leakScopeOf(s, false, attached); len(own.NoMeta) != 0 {
		t.Fatalf("an own repo got a no-meta set: %v", own.NoMeta)
	}
}
