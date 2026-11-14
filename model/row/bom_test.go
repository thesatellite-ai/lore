package row

import "testing"

// TestDecodeIgnoresBOM (I-15): a row file saved with a UTF-8 byte order
// mark (Windows Notepad) decodes like the same file without it; before it
// was "row: not a JSON object" on every read.
func TestDecodeIgnoresBOM(t *testing.T) {
	doc := []byte(`{"title":"x","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","_v":2,"_table":"memories","_hlc":"2026-11-04T10:42:07.123Z/0000/"}`)
	plain, perr := Decode(doc)
	if perr != nil {
		t.Fatal(perr)
	}
	bom := append([]byte{0xEF, 0xBB, 0xBF}, doc...)
	got, err := Decode(bom)
	if err != nil || got.ID != plain.ID {
		t.Fatalf("with BOM: %v / %v, without: %v / %v", got.ID, err, plain.ID, perr)
	}
	if !HasBOM(bom) || HasBOM(doc) || string(TrimBOM(bom)) != string(doc) {
		t.Fatal("HasBOM/TrimBOM")
	}
}
