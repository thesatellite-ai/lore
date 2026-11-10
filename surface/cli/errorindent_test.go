package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestJSONErrorIndented (E-GAPS-1 S3): every --json error prints the same
// shape, the envelope indented with two spaces like v0.1.17's printJSON
// and the compat nouns' output, whether a host command (PrintError) or a
// generated command of any noun (printError, e.g. `lore row move --json`
// with no id) prints it.
func TestJSONErrorIndented(t *testing.T) {
	err := errs.New(errs.InvalidInput, "missing required field id").WithHint("pass the id")
	want, mErr := json.MarshalIndent(errs.EnvelopeOf(err), "", "  ")
	if mErr != nil {
		t.Fatal(mErr)
	}
	want = append(want, '\n')
	var out, eout bytes.Buffer
	if code := PrintError(&out, &eout, err, true); code != 20 || eout.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, eout.String())
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("host error:\n%s\nwant:\n%s", out.String(), want)
	}
	out.Reset()
	if code := printError(&out, &eout, err, true, Palette{}); code != 20 || !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("generated error: exit %d\n%s\nwant:\n%s", code, out.String(), want)
	}
}
