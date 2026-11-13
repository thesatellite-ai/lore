package tasklist_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
)

// runArgs runs an op the way the CLI does: positional arguments and flags
// through op.ArgsDecoder.
func runArgs(t *testing.T, h *opstest.H, name string, args []string, flags map[string][]string) (op.Result, error) {
	t.Helper()
	e, ok := h.App.Registry().Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	return h.App.Run(context.Background(), e, op.ArgsDecoder{Args: args, Flags: flags}, op.Caller{Actor: actor})
}

// TestAddTitleNamed (U-4, reverting U-3's positional title): `lore
// tasklist add` takes --title like every other add, the body optional by
// --body; v0.1.17's `--title X "<body>"` keeps working quietly. A bare
// positional is no title, a title missing or blank and a body given twice
// are E_INVALID_INPUT.
func TestAddTitleNamed(t *testing.T) {
	h := opstest.New(t)
	cases := []struct {
		name        string
		args        []string
		flags       map[string][]string
		title, body string
	}{
		{"--title", nil, map[string][]string{"title": {"Sprint 4"}}, "Sprint 4", ""},
		{"--title --body", nil, map[string][]string{"title": {"Sprint 7"}, "body": {"b7"}}, "Sprint 7", "b7"},
		{"--title, positional body (v0.1.17)", []string{"b8"}, map[string][]string{"title": {"Sprint 8"}}, "Sprint 8", "b8"},
	}
	for _, c := range cases {
		res, err := runArgs(t, h, "tasklist.add", c.args, c.flags)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got struct {
			Data struct{ Title, Body string }
		}
		if err := json.Unmarshal([]byte(h.JSON(res)), &got); err != nil {
			t.Fatal(err)
		}
		if got.Data.Title != c.title || got.Data.Body != c.body {
			t.Errorf("%s: title %q body %q, want %q %q", c.name, got.Data.Title, got.Data.Body, c.title, c.body)
		}
	}
	for name, in := range map[string]struct {
		args  []string
		flags map[string][]string
	}{
		"no title":        {nil, map[string][]string{"body": {"b"}}},
		"positional only": {[]string{"Sprint 4"}, nil},
		"blank title":     {nil, map[string][]string{"title": {"  "}}},
		"body twice":      {[]string{"a"}, map[string][]string{"title": {"t"}, "body": {"c"}}},
		"too many args":   {[]string{"a", "b"}, map[string][]string{"title": {"t"}}},
	} {
		if _, err := runArgs(t, h, "tasklist.add", in.args, in.flags); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := runArgs(t, h, "tasklist.add", nil, nil); err == nil || !strings.Contains(err.Error(), "missing required field title") {
		t.Errorf("no input: %v", err)
	}
}
