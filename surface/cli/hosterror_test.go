package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestJSONRequested: --json is seen anywhere before "--", with cobra's
// bool spellings; after "--" it is an operand.
func TestJSONRequested(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"memory", "show", "x"}, false},
		{[]string{"memory", "show", "x", "--json"}, true},
		{[]string{"--json", "memory"}, true},
		{[]string{"x", "--json=true"}, true},
		{[]string{"x", "--json=1"}, true},
		{[]string{"x", "--json=false"}, false},
		{[]string{"x", "--json", "--json=false"}, false}, // last one wins, as cobra
		{[]string{"x", "--json=yes"}, false},             // cobra refuses it
		{[]string{"x", "--", "--json"}, false},
		{[]string{"x", "--jsonx"}, false},
		{[]string{"x", "-json"}, false},
	} {
		if got := JSONRequested(c.args); got != c.want {
			t.Errorf("%q: %v, want %v", c.args, got, c.want)
		}
	}
}

// TestPrintErrorHost: the host printer writes the generated commands'
// envelope on stdout with --json (nothing on stderr) and the ERROR line
// on stderr without it (nothing on stdout); both return the class exit.
func TestPrintErrorHost(t *testing.T) {
	err := errs.New(errs.RootRefused, "lore refuses to run as root").WithHint("run as your user")
	var out, eout bytes.Buffer
	code := PrintError(&out, &eout, err, true)
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
			Exit    int    `json:"exit"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &env); jerr != nil || eout.Len() != 0 {
		t.Fatalf("json: %q / %q: %v", out.String(), eout.String(), jerr)
	}
	if env.Error.Code != string(errs.RootRefused) || env.Error.Hint != "run as your user" || env.Error.Exit != code || code == 0 {
		t.Fatalf("envelope %+v exit %d", env, code)
	}
	out.Reset()
	eout.Reset()
	if c := PrintError(&out, &eout, err, false); c != code || out.Len() != 0 ||
		!strings.HasPrefix(eout.String(), "ERROR: [E_ROOT_REFUSED] lore refuses to run as root\n  hint: ") {
		t.Fatalf("text: %d %q %q", c, out.String(), eout.String())
	}
}
