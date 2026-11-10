package leakcheck

import (
	"strings"
	"testing"
)

// A deliberate transfer (§6.4) may carry the text of exactly the row it
// carries, nothing else: F11 found v0.1.17's `lore share` refused by its
// own guard; `lore move` passes AllowRows the same way, and copies v0.1.17
// shared stay allowed through shared_as. Matrix: how the copy is allowed
// (AllowRows on the item, or shared_as on the internal row) × what the content quotes (only the source, the
// source plus a second internal note, only the second note) × where the
// content is (the copy's row file, another row file, a source file).

const (
	copyID  = "mem_0d000000000000000000000000000001"
	otherID = "mem_0e000000000000000000000000000002"
	other   = "their CTO prefers calls on tuesday mornings before the standup"
)

func copyPath(id string) string { return ".lore/data/memories/" + id + ".json" }

func rowFile(id, text string) []byte {
	return []byte(`{"id":"` + id + `","project_id":"` + repoPID + `","body":"` + text + `"}`)
}

func shareIndex(sharedAs []string) *Index {
	return NewIndex([]IndexRow{
		{ID: memID, Table: "memories", Label: "[northwind/acme]", Texts: []string{note}, SharedAs: sharedAs},
		{ID: otherID, Table: "memories", Label: "[northwind/acme]", Texts: []string{other}},
	}, nil)
}

func textRows(fs []Finding) []string {
	var out []string
	for _, f := range Blocking(fs) {
		if f.Rule == RuleText {
			out = append(out, f.RowID)
		}
	}
	return out
}

func TestSharedCopyCarriesOnlyItsSource(t *testing.T) {
	cases := []struct {
		name     string
		sharedAs []string
		item     Item
		want     []string // text findings (row ids), nil = clean
	}{
		{"share: copy allowed by AllowRows", nil,
			Item{Where: "share", Path: copyPath(copyID), Content: rowFile(copyID, note), AllowRows: []string{memID}}, nil},
		{"share: copy that also quotes a second note", nil,
			Item{Where: "share", Path: copyPath(copyID), Content: rowFile(copyID, note+" and "+other), AllowRows: []string{memID}}, []string{otherID}},
		{"share: AllowRows names another row", nil,
			Item{Where: "share", Path: copyPath(copyID), Content: rowFile(copyID, note), AllowRows: []string{otherID}}, []string{memID}},
		{"commit: copy recorded in shared_as", []string{copyID},
			Item{Where: copyPath(copyID), Path: copyPath(copyID), Content: rowFile(copyID, note)}, nil},
		{"commit: recorded copy that also quotes a second note", []string{copyID},
			Item{Where: copyPath(copyID), Path: copyPath(copyID), Content: rowFile(copyID, note+" "+other)}, []string{otherID}},
		{"commit: a different row file with the source text", []string{copyID},
			Item{Where: copyPath(clientMem), Path: copyPath(clientMem), Content: rowFile(clientMem, note)}, []string{memID}},
		{"commit: a source file quoting the shared note", []string{copyID},
			Item{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("// " + note)}, []string{memID}},
		{"commit: commit message quoting the shared note", []string{copyID},
			Item{Where: "commit message", Content: []byte(note)}, []string{memID}},
		{"commit: copy not recorded anywhere", nil,
			Item{Where: copyPath(copyID), Path: copyPath(copyID), Content: rowFile(copyID, note)}, []string{memID}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := textRows(Check([]Item{c.item}, shareIndex(c.sharedAs), Options{RepoProjectID: repoPID, ClientRepo: true}))
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("text findings %v, want %v", got, c.want)
			}
		})
	}
}

// clientMem is a repo row that is not a shared copy.
const clientMem = "mem_0f000000000000000000000000000003"

// Two internal rows holding the same words: allowing one (the shared
// source) must not hide the other. Before shingles kept every owner, the
// first row indexed owned the window alone.
func TestAllowingOneRowKeepsTheOtherWithSameWords(t *testing.T) {
	idx := NewIndex([]IndexRow{
		{ID: memID, Table: "memories", Label: "[a]", Texts: []string{note}},
		{ID: otherID, Table: "memories", Label: "[a]", Texts: []string{"quoted: " + note}},
	}, nil)
	it := Item{Where: "share", Path: copyPath(copyID), Content: rowFile(copyID, note), AllowRows: []string{memID}}
	got := textRows(Check([]Item{it}, idx, Options{}))
	if strings.Join(got, ",") != otherID {
		t.Fatalf("text findings %v, want only %s", got, otherID)
	}
}

// Findings carry the checked item's path, so a refusal can name the repo
// row that leaks (F10).
func TestFindingCarriesItemPath(t *testing.T) {
	it := Item{Where: "commit abc " + copyPath(clientMem), Path: copyPath(clientMem), Content: rowFile(clientMem, note)}
	fs := Blocking(Check([]Item{it}, shareIndex(nil), Options{}))
	if len(fs) != 1 || fs[0].Path != copyPath(clientMem) {
		t.Fatalf("got %+v", fs)
	}
}
