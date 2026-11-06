package settings

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRenamePairs (later 522, RES 3.14): pair shape x allowed values.
// A valid pair renames in place (order kept) and is returned; pairs apply
// in order, so a chain a=b,b=c works once b was renamed away and a later
// pair sees the earlier rename; a malformed pair, an old value not
// allowed and a new value already allowed are E_INVALID_INPUT naming the
// field and never touching the caller's list.
func TestRenamePairs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		raw      []string
		vals     []string
		want     []string
		pairs    int
		errWords string
	}{
		{"none", nil, []string{"a", "b"}, []string{"a", "b"}, 0, ""},
		{"one", []string{"b=beta"}, []string{"a", "b", "c"}, []string{"a", "beta", "c"}, 1, ""},
		{"two", []string{"a=alpha", "c=gamma"}, []string{"a", "b", "c"}, []string{"alpha", "b", "gamma"}, 2, ""},
		{"chain after rename", []string{"b=x", "a=b"}, []string{"a", "b"}, []string{"b", "x"}, 2, ""},
		{"rename an earlier new", []string{"a=x", "x=y"}, []string{"a"}, []string{"y"}, 2, ""},
		{"no equals", []string{"ab"}, []string{"a"}, nil, 0, "takes <old>=<new>"},
		{"empty old", []string{"=b"}, []string{"a"}, nil, 0, "takes <old>=<new>"},
		{"empty new", []string{"a="}, []string{"a"}, nil, 0, "takes <old>=<new>"},
		{"unknown old", []string{"z=y"}, []string{"a"}, nil, 0, "host.os has no allowed value"},
		{"new taken", []string{"a=b"}, []string{"a", "b"}, nil, 0, "host.os already allows"},
		{"value with equals", []string{"a=b=c"}, []string{"a"}, []string{"b=c"}, 1, ""},
	}
	for _, c := range cases {
		before := slices.Clone(c.vals)
		pairs, got, err := renamePairs("host.os", c.raw, c.vals)
		if !slices.Equal(c.vals, before) {
			t.Fatalf("%s: the caller's list changed: %v", c.name, c.vals)
		}
		if c.errWords != "" {
			if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), c.errWords) {
				t.Errorf("%s: err %v, want E_INVALID_INPUT with %q", c.name, err, c.errWords)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) || len(pairs) != c.pairs {
			t.Errorf("%s: got %v pairs %v err %v, want %v (%d pairs)", c.name, got, pairs, err, c.want, c.pairs)
		}
	}
}
