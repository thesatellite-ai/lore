package settings

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestValueSets (OV19, RES-OQ4): --set, --set-json and --set-stdin fold
// into one field=value list: a JSON string is unquoted, other JSON is kept
// raw, stdin loses only trailing newlines; malformed pairs, invalid JSON,
// empty or oversized stdin are E_INVALID_INPUT.
func TestValueSets(t *testing.T) {
	old := resStdin
	t.Cleanup(func() { resStdin = old })
	cases := []struct {
		name    string
		sets    []string
		js      []string
		stdinTo string
		stdin   string
		want    string // joined by |
		bad     bool
	}{
		{"set only", []string{"a=1", "b=x=y"}, nil, "", "", "a=1|b=x=y", false},
		{"json string unquoted", nil, []string{`os="linux"`}, "", "", "os=linux", false},
		{"json list kept", nil, []string{`tags=["a","b"]`}, "", "", `tags=["a","b"]`, false},
		{"json number", nil, []string{`port=8080`}, "", "", "port=8080", false},
		{"json unicode string", nil, []string{`name="Zürich ✓"`}, "", "", "name=Zürich ✓", false},
		{"json not json", nil, []string{`os=linux`}, "", "", "", true},
		{"json no field", nil, []string{`="x"`}, "", "", "", true},
		{"json no value", nil, []string{`os=`}, "", "", "", true},
		{"json no separator", nil, []string{`os`}, "", "", "", true},
		{"stdin", []string{"a=1"}, nil, "token", "s3cr3t-value\n", "a=1|token=s3cr3t-value", false},
		{"stdin crlf trimmed, inner newline kept", nil, nil, "notes", "line1\nline2\r\n\r\n", "notes=line1\nline2", false},
		{"stdin empty", nil, nil, "token", "\n", "", true},
		{"stdin too big", nil, nil, "token", strings.Repeat("x", maxResStdin+1), "", true},
		{"stdin at the limit", nil, nil, "token", strings.Repeat("x", maxResStdin), "token=" + strings.Repeat("x", maxResStdin), false},
	}
	for _, c := range cases {
		resStdin = strings.NewReader(c.stdin)
		got, err := valueSets(c.sets, c.js, c.stdinTo)
		if c.bad {
			if !errs.Has(err, errs.InvalidInput) {
				t.Errorf("%s: want E_INVALID_INPUT, got %v", c.name, err)
			}
			continue
		}
		if err != nil || strings.Join(got, "|") != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
}
