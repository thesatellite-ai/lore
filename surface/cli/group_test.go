package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// The repeated group input (op/group.go, P3-TASK-1) on the command line:
// a flag typed after an item belongs to that item, one typed before the
// first item to the shared outer field.

type grpItem struct {
	Title string   `json:"title" arg:"0" desc:"item title"`
	Level string   `json:"level,omitempty" flag:"level" desc:"item level"`
	Tags  []string `json:"tags,omitempty" flag:"tag" desc:"item tags"`
	Done  bool     `json:"done,omitempty" flag:"done" desc:"item done"`
}

type grpIn struct {
	ID    string    `json:"id" arg:"0" desc:"the parent"`
	Items []grpItem `json:"items,omitempty" arg:"1" rest:"true" desc:"the items"`
	Level string    `json:"level,omitempty" flag:"level" desc:"shared level"`
	Tags  []string  `json:"tags,omitempty" flag:"tag" desc:"shared tags"`
	Done  bool      `json:"done,omitempty" flag:"done" desc:"shared done"`
	Key   string    `json:"key,omitempty" flag:"key" desc:"not an item flag"`
}

type grpOut struct {
	Echo string `json:"echo"`
}

// grpEcho prints the decoded input:
// id;level:tags:done;key;title:level:tags:done|…
func grpEcho(in grpIn) string {
	parts := make([]string, 0, len(in.Items))
	for _, it := range in.Items {
		parts = append(parts, it.Title+":"+it.Level+":"+strings.Join(it.Tags, ",")+":"+boolWord(it.Done))
	}
	return in.ID + ";" + in.Level + ":" + strings.Join(in.Tags, ",") + ":" + boolWord(in.Done) + ";" + in.Key + ";" + strings.Join(parts, "|")
}

func boolWord(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

func grpRegistry(t *testing.T) *op.Registry {
	t.Helper()
	split := op.Define(op.Spec{Name: "thing.split", Summary: "Split a thing", Permission: op.PermWrite, Access: op.AccessWrite},
		func(_ context.Context, in grpIn) (grpOut, error) { return grpOut{Echo: grpEcho(in)}, nil })
	reg, err := op.NewRegistry(split)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// grpRunner decodes and invokes, nothing else.
type grpRunner struct{}

func (grpRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, _ op.Caller) (op.Result, error) {
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	return e.Invoke(ctx, in)
}

// TestGroupFlags is the position matrix: shared before the first item,
// own after an item, the `=` form, repeated and bool item flags, a
// non-item flag after an item, `--` ending flags, and both together.
func TestGroupFlags(t *testing.T) {
	t.Parallel()
	reg := grpRegistry(t)
	cases := []struct {
		name string
		args []string
		echo string
	}{
		{"no items", []string{"p"}, "p;::n;;"},
		{"plain items", []string{"p", "a", "b"}, "p;::n;;a:::n|b:::n"},
		{"shared before", []string{"p", "--level", "low", "a", "b"}, "p;low::n;;a:::n|b:::n"},
		{"shared before id", []string{"--level", "low", "p", "a"}, "p;low::n;;a:::n"},
		{"own after", []string{"p", "a", "--level", "high", "b"}, "p;::n;;a:high::n|b:::n"},
		{"own on last", []string{"p", "a", "b", "--level=high"}, "p;::n;;a:::n|b:high::n"},
		{"shared and own", []string{"p", "--level", "low", "a", "--level", "high", "b"}, "p;low::n;;a:high::n|b:::n"},
		{"repeatable per item", []string{"p", "--tag", "s", "a", "--tag", "x", "--tag", "y", "b", "--tag", "z"}, "p;:s:n;;a::x,y:n|b::z:n"},
		{"bool per item", []string{"p", "a", "--done", "b"}, "p;::n;;a:::y|b:::n"},
		{"non-item flag after item", []string{"p", "a", "--key", "k", "b"}, "p;::n;k;a:::n|b:::n"},
		{"after --", []string{"p", "a", "--level", "high", "--", "--level"}, "p;::n;;a:high::n|--level:::n"},
	}
	for _, c := range cases {
		r := execute(Build(reg, grpRunner{}), "", append([]string{"thing", "split", "--json"}, c.args...)...)
		if r.exit != 0 {
			t.Fatalf("%s: exit %d stderr %q", c.name, r.exit, r.stderr)
		}
		var out grpOut
		if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if out.Echo != c.echo {
			t.Errorf("%s: %q want %q", c.name, out.Echo, c.echo)
		}
	}
}

// TestGroupFlagErrors: a scalar item flag twice on one item, or twice
// before the first item, is refused (never silently the last value), and
// the message names the item.
func TestGroupFlagErrors(t *testing.T) {
	t.Parallel()
	reg := grpRegistry(t)
	r := execute(Build(reg, grpRunner{}), "", "thing", "split", "p", "a", "--level", "x", "--level", "y")
	if r.exit != errs.ExitBadInput || !strings.Contains(r.stderr, `items 1 "a": --level takes one value`) {
		t.Errorf("twice on one item: exit %d stderr %q", r.exit, r.stderr)
	}
	r = execute(Build(reg, grpRunner{}), "", "thing", "split", "--level", "x", "--level", "y", "p", "a")
	if r.exit != errs.ExitBadInput || !strings.Contains(r.stderr, "--level takes one value") {
		t.Errorf("twice shared: exit %d stderr %q", r.exit, r.stderr)
	}
}

// TestGroupHelp: --help says where item flags apply.
func TestGroupHelp(t *testing.T) {
	t.Parallel()
	r := execute(Build(grpRegistry(t), grpRunner{}), "", "thing", "split", "--help")
	want := "--level --tag --done after an item apply to that item only; before the first item, to every item"
	if !strings.Contains(r.stdout, want) || !strings.Contains(r.stdout, "[items...]") {
		t.Errorf("help %q", r.stdout)
	}
}
