package rowfs

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
)

// TestDecodeImportableEscapedMarkers (g-sync note): a row file written by
// another JSON encoder spells '<' and '>' as < / > (Go's
// json.Marshal does by default), so the raw bytes never hold "<<<<<<<"
// while the decoded text still holds a conflict block. The import decode
// refuses it the same way in every spelling (lower or upper hex, mixed
// with raw characters, an escaped space after a raw marker), and still
// accepts escaped text that is not a block; Read lists such a file as a
// problem instead of importing it as knowledge.
func TestDecodeImportableEscapedMarkers(t *testing.T) {
	g := newGen(t)
	block := "<<<<<<< 0001\nmine\n=======\ntheirs\n>>>>>>> 0002"
	spell := map[string]func(string) string{
		"raw":   func(s string) string { return s },
		"lower": func(s string) string { return strings.NewReplacer("<", esc("u003c"), ">", esc("u003e")).Replace(s) },
		"upper": func(s string) string { return strings.NewReplacer("<", esc("u003C"), ">", esc("u003E")).Replace(s) },
		"mixed": func(s string) string {
			return strings.Replace(s, "<<<<<<<", "<"+esc("u003c")+"<"+esc("u003c")+"<"+esc("u003c")+"<", 1)
		},
		"escaped space": func(s string) string {
			return strings.Replace(s, "<<<<<<< ", "<<<<<<<"+esc("u0020"), 1)
		},
	}
	for name, enc := range spell {
		for _, c := range []struct {
			label, body string
			refused     bool
		}{
			{"block", block, true},
			{"block inside text", "before\n" + block + "\nafter", true},
			{"plain less-than", "a < b and c > d", false},
			{"one marker-like line", "<<<<<<< not a block", false},
		} {
			t.Run(name+"/"+c.label, func(t *testing.T) {
				id := newID(t)
				doc := g.row(id, c.body, hlc.HLC{}).Doc
				// re-spell only inside the body value: the envelope keys hold no '<'
				spelled := strings.Replace(string(doc), jsonText(t, c.body), enc(jsonText(t, c.body)), 1)
				if spelled == string(doc) && name != "raw" && strings.Contains(c.body, "<<<<<<< ") {
					t.Fatalf("the spelling did not change the document: %q -> %q in %s", jsonText(t, c.body), enc(jsonText(t, c.body)), doc)
				}
				rel := RowPath(memories, id)
				_, err := decodeImportable(rel, []byte(spelled))
				if c.refused != (err != nil) {
					t.Fatalf("refused=%v, want %v (%v)\n%s", err != nil, c.refused, err, spelled)
				}
				if c.refused && (!errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "field body holds unresolved conflict markers")) {
					t.Fatalf("error %v", err)
				}
			})
		}
	}

	ctx := t.Context()
	f := folder(t, openRoot(t), FolderRoot)
	marked := newID(t)
	doc := string(g.row(marked, block, hlc.HLC{}).Doc)
	writeRaw(t, f, RowPath(memories, marked), strings.Replace(doc, jsonText(t, block), strings.NewReplacer("<", esc("u003c"), ">", esc("u003e")).Replace(jsonText(t, block)), 1))
	rows, _, err := f.Read(ctx, "")
	if err != nil || len(rows) != 0 || len(f.ReadProblems()) != 1 {
		t.Fatalf("an escaped marked file was imported: %d rows, problems %+v, %v", len(rows), f.ReadProblems(), err)
	}
}

// esc is a JSON escape: a backslash, then code (u003c is '<').
func esc(code string) string { return string(rune(0x5c)) + code }

// jsonText is s as the canonical writer spells it inside a JSON string
// (newlines escaped), without the quotes.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	return strings.ReplaceAll(s, "\n", `\n`)
}
