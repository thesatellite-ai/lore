package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestVarsSetRefusesCycle (SEC 8.3, CLI_REFERENCE vars set errors): a
// value that closes a loop of variables is refused E_REF_CYCLE with the
// chain, at set time and with --allow-dangling too, and nothing is
// written: a self loop, two variables, three, a loop through an alias
// (an alias and its name count as one); a chain without a loop, a
// diamond, and a dangling name with --allow-dangling are accepted.
func TestVarsSetRefusesCycle(t *testing.T) {
	t.Parallel()
	set := func(h *opstest.H, name, val, extra string) error {
		in := `{"name":"` + name + `","value":` + opstest.Quote(val)
		if extra != "" {
			in += "," + extra
		}
		_, err := h.RunYes(settings.OpVarsSet, in+"}")
		return err
	}
	cases := []struct {
		name  string
		setup [][2]string
		last  [2]string
		extra string
		chain string // "" = accepted
	}{
		{"self", nil, [2]string{"a", "x ${var:a}"}, "", "var:a -> var:a"},
		{"self allow-dangling", nil, [2]string{"a", "${var:a}"}, `"allow_dangling":true`, "var:a -> var:a"},
		{"two", [][2]string{{"a", "1"}, {"b", "${var:a}"}}, [2]string{"a", "${var:b}"}, "", "var:a -> var:b -> var:a"},
		{"three", [][2]string{{"c", "1"}, {"b", "${var:c}"}, {"a", "${var:b}"}}, [2]string{"c", "${var:a}"}, "", "var:c -> var:a -> var:b -> var:c"},
		{"chain no loop", [][2]string{{"c", "1"}, {"b", "${var:c}"}}, [2]string{"a", "${var:b}-${var:c}"}, "", ""},
		{"diamond", [][2]string{{"d", "1"}, {"b", "${var:d}"}, {"c", "${var:d}"}}, [2]string{"a", "${var:b}${var:c}"}, "", ""},
		{"dangling allowed", nil, [2]string{"a", "${var:nope}"}, `"allow_dangling":true`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			for _, s := range c.setup {
				if err := set(h, s[0], s[1], ""); err != nil {
					t.Fatalf("setup %s: %v", s[0], err)
				}
			}
			before := opstest.Text(h.Must(settings.OpVarsList, `{}`))
			err := set(h, c.last[0], c.last[1], c.extra)
			if c.chain == "" {
				if err != nil {
					t.Fatalf("%s=%s: %v", c.last[0], c.last[1], err)
				}
				return
			}
			if !errs.Has(err, errs.RefCycle) || !strings.Contains(err.Error(), c.chain) {
				t.Fatalf("%s=%s: %v, want E_REF_CYCLE naming %s", c.last[0], c.last[1], err, c.chain)
			}
			if after := opstest.Text(h.Must(settings.OpVarsList, `{}`)); after != before {
				t.Fatalf("a refused set wrote:\n%s\nbefore:\n%s", after, before)
			}
		})
	}
	// an alias counts as its variable
	h := opstest.New(t)
	if err := set(h, "a", "1", ""); err != nil {
		t.Fatal(err)
	}
	h.MustYes(settings.OpVarsRename, `{"id":"a","new_name":"a2"}`)
	if err := set(h, "b", "${var:a}", ""); err != nil {
		t.Fatal(err)
	}
	if err := set(h, "a2", "${var:b}", ""); !errs.Has(err, errs.RefCycle) {
		t.Fatalf("loop through an alias: %v", err)
	}
}
