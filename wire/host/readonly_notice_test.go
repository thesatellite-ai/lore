package host

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/op"
)

type noticeIn struct{}

type noticeOut struct{}

// TestReadOnlyNoticeHook: the line prints once, before the first CLI
// read; never for a write (the engine refuses it with E_OLD_LORE), never
// for a REST request (no command), and nothing when the project is
// writable (FORMAT_EVOLUTION rule 2).
func TestReadOnlyNoticeHook(t *testing.T) {
	fn := func(context.Context, noticeIn) (noticeOut, error) { return noticeOut{}, nil }
	read := op.Define(op.Spec{Name: "demo.list", Summary: "s", Permission: op.PermRead, Access: op.AccessRead}, fn)
	write := op.Define(op.Spec{Name: "demo.add", Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite}, fn)
	cmd := &cobra.Command{Use: "x"}
	cases := []struct {
		name   string
		line   string
		runs   []*Run
		want   string
		called int
	}{
		{"read twice", "lore: read-only here (x)", []*Run{{Entry: read, Cmd: cmd}, {Entry: read, Cmd: cmd}}, "lore: read-only here (x)\n", 1},
		{"write", "lore: read-only here (x)", []*Run{{Entry: write, Cmd: cmd}}, "", 0},
		{"rest read", "lore: read-only here (x)", []*Run{{Entry: read}}, "", 0},
		{"writable", "", []*Run{{Entry: read, Cmd: cmd}}, "", 1},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		called := 0
		h := ReadOnlyNoticeHook(&buf, func(context.Context) string { called++; return c.line })
		for _, r := range c.runs {
			if err := h.Before(context.Background(), r); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		}
		if buf.String() != c.want || called != c.called {
			t.Errorf("%s: printed %q (notice called %d), want %q (%d)", c.name, buf.String(), called, c.want, c.called)
		}
	}
}
