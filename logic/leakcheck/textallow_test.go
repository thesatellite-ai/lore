package leakcheck

import (
	"strings"
	"testing"
)

// #129 (devco:D-906): a text allow lets exactly one passage of one note
// through, in one file when it names one; any other run of the same note,
// another note, another file or a changed passage still blocks.
func TestTextAllowCoversOnlyItsPassage(t *testing.T) {
	const (
		note    = "dec_0000000000000000000000000000a001"
		other   = "dec_0000000000000000000000000000a002"
		copyTxt = "Your order has been received and a support agent will contact you within two business days."
		secret  = "the client pays the reseller margin of twelve percent on every renewal quietly"
	)
	idx := NewIndex([]IndexRow{
		{ID: note, Table: "decisions", Label: "[acme]", Texts: []string{"Approved wording: \"" + copyTxt + "\" Also: " + secret + "."}},
		{ID: other, Table: "decisions", Label: "[acme]", Texts: []string{"Banner, again: " + copyTxt}},
	}, nil)
	allow := TextAllow{ID: "sal_1", RowID: note, Hash: PassageHashText(copyTxt), Path: "copy.txt", Reason: "approved public copy", By: "Ann"}
	check := func(path, content string, allows ...TextAllow) (blocking, allowed []Finding) {
		fs := Check([]Item{{Where: "commit " + path, Path: path, Content: []byte(content)}}, idx, Options{TextAllows: allows})
		for _, f := range fs {
			if f.Rule != RuleText {
				continue
			}
			if f.Warn {
				allowed = append(allowed, f)
			} else {
				blocking = append(blocking, f)
			}
		}
		return blocking, allowed
	}
	onlyNote := TextAllow{ID: "sal_2", RowID: other, Hash: PassageHashText(copyTxt), Reason: "approved public copy"}

	b, _ := check("copy.txt", copyTxt+"\n")
	if len(b) != 2 {
		t.Fatalf("without an allow the passage blocks for both notes, got %+v", b)
	}
	if b[0].Hash != PassageHashText(copyTxt) {
		t.Fatalf("the finding's hash is not the passage hash an allow names: %d", b[0].Hash)
	}
	b, a := check("copy.txt", copyTxt+"\n", allow, onlyNote)
	if len(b) != 0 || len(a) != 2 || !strings.Contains(a[0].Describe(false)+a[1].Describe(false), "allowed by sal_1 (approved public copy; by Ann)") {
		t.Fatalf("the allowed passage still blocks or is not named: blocking %+v allowed %+v", b, a)
	}
	if b, _ = check("other.txt", copyTxt+"\n", allow, onlyNote); len(b) != 1 || b[0].RowID != note {
		t.Fatalf("an allow narrowed to copy.txt let the passage through in other.txt: %+v", b)
	}
	if b, _ = check("copy.txt", copyTxt+"\n", allow); len(b) != 1 || b[0].RowID != other {
		t.Fatalf("an allow for one note hid another note holding the same words: %+v", b)
	}
	// a second run of the same note in the same file, shorter than the
	// allowed one, still blocks (the allowed run must not hide it)
	if b, _ = check("copy.txt", copyTxt+"\n\nInternal: "+secret+"\n", allow, onlyNote); len(b) != 1 || b[0].RowID != note || b[0].Line != 3 {
		t.Fatalf("an uncovered run of the allowed note was hidden: %+v", b)
	}
	// the shipped passage grows by words the note also has: another run,
	// another hash, so the allow stops applying
	if b, _ = check("copy.txt", "Approved wording: "+copyTxt+"\n", allow, onlyNote); len(b) != 1 || b[0].RowID != note {
		t.Fatalf("a changed passage was still allowed: %+v", b)
	}
}
