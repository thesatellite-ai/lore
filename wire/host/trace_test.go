package host

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// fakeGit answers every command with out and err.
type fakeGit struct {
	out []byte
	err error
}

func (f fakeGit) Run(context.Context, gitref.Invocation) ([]byte, error) { return f.out, f.err }

// TestTraceGit: off, nothing is printed; on (flag or LORE_TRACE=1), one
// line per command with its arguments and outcome; the output and error
// pass through unchanged either way (CLI_REFERENCE 1.1 --trace).
func TestTraceGit(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		flag bool
		env  string
		err  error
		want string // "" = no line
	}{
		{"off", false, "", nil, ""},
		{"env other value", false, "true", nil, ""},
		{"flag", true, "", nil, "trace: git log -n1 ("},
		{"env", false, "1", boom, ", error)\n"},
	}
	for _, c := range cases {
		t.Setenv(EnvTrace, c.env)
		var buf bytes.Buffer
		flag := c.flag
		g := TraceGit{Inner: fakeGit{out: []byte("x"), err: c.err}, On: TraceOn(&flag), W: &buf}
		out, err := g.Run(context.Background(), gitref.Invocation{Args: []string{"log", "-n1"}})
		if string(out) != "x" || !errors.Is(err, c.err) && c.err != nil || c.err == nil && err != nil {
			t.Fatalf("%s: passthrough %q %v", c.name, out, err)
		}
		got := buf.String()
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: printed %q, want %q", c.name, got, c.want)
		}
	}
}
