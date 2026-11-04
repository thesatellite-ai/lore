package refpoint

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// fakeVars resolves ${var:name} from a map; every var gets its own id.
type fakeVars struct {
	vals map[string]string
	ids  map[string]ids.AnyID
}

func (f fakeVars) Resolve(r refs.Ref) (refs.Resolved, error) {
	v, ok := f.vals[r.Target]
	if !ok {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	return refs.Resolved{Text: v, ID: f.ids[r.Target]}, nil
}

func newVars(t *testing.T, vals map[string]string) fakeVars {
	t.Helper()
	f := fakeVars{vals: vals, ids: map[string]ids.AnyID{}}
	for k := range vals {
		id, err := ids.New[ids.Var]()
		if err != nil {
			t.Fatal(err)
		}
		f.ids[k] = id.Any()
	}
	return f
}

func tokenScanner(t *testing.T) scan.Scanner {
	t.Helper()
	s, err := scan.NewRegexScanner([]scan.Rule{{ID: "fake-token", Regex: `tok_[a-z0-9]{12}`, Source: scan.SourceStore}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const secretRef = "${secret:gopass://acme/hub/db?holder=mch_0cd630f0&label=mira}"

func TestExpandPoints(t *testing.T) {
	vars := newVars(t, map[string]string{
		"host": "db.example", "mine": "personal.example", "priv": "private.example",
		"ref": "${secret:gopass://acme/x}", "leak": "tok_abcdefghijkl",
	})
	locate := func(id ids.AnyID) Target {
		switch id {
		case vars.ids["host"], vars.ids["ref"], vars.ids["leak"]:
			return Target{Shareable: true, Reaches: true}
		case vars.ids["mine"]:
			return Target{Personal: true, Shareable: true}
		}
		return Target{Reaches: true}
	}
	env := func(n string) (string, bool) {
		return map[string]string{"HOME_DIR": "/home/x", "API_TOKEN": "t"}[n], n == "HOME_DIR" || n == "API_TOKEN"
	}
	text := "h=${var:host} m=${var:mine} p=${var:priv} s=" + secretRef + " r=${var:ref} e=${env:HOME_DIR} k=${env:API_TOKEN} x=$${var:host}"
	for _, tc := range []struct {
		point Point
		want  string
	}{
		{Show, "h=db.example m=personal.example p=private.example s=" + Mask + " r=" + Mask + " e=/home/x k=" + Mask + " x=${var:host}"},
		{Render, "h=db.example m=${var:mine} p=${var:priv} s=" + secretRef + " r=${secret:gopass://acme/x} e=${env:HOME_DIR} k=${env:API_TOKEN} x=${var:host}"},
		{Client, "h=db.example m=${var:mine} p=${var:priv} s=" + Redacted + " r=" + Redacted + " e=${env:HOME_DIR} k=${env:API_TOKEN} x=${var:host}"},
	} {
		got, err := Expand(Options{Point: tc.point, Resolver: vars, Locate: locate, Env: env, Scanner: tokenScanner(t)}, "body", text)
		if err != nil {
			t.Fatalf("%s: %v", tc.point, err)
		}
		if got.Text != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.point, got.Text, tc.want)
		}
		if tc.point == Client && strings.Contains(got.Text, "holder") {
			t.Errorf("client render leaks the holder: %q", got.Text)
		}
	}

	// 13.2: a var that brings a token in is masked in show, refused in render.
	got, err := Expand(Options{Point: Show, Resolver: vars, Locate: locate, Scanner: tokenScanner(t)}, "body", "k=${var:leak}")
	if err != nil || got.Text != "k="+Mask {
		t.Fatalf("show leak: %q %v", got.Text, err)
	}
	for _, p := range []Point{Render, Client} {
		_, err = Expand(Options{Point: p, Resolver: vars, Locate: locate, Scanner: tokenScanner(t)}, "body", "k=${var:leak}")
		if !errs.Has(err, errs.SecretDetected) || !strings.Contains(err.Error(), "${var:leak}") {
			t.Fatalf("%s leak: want E_SECRET_DETECTED naming ${var:leak}, got %v", p, err)
		}
	}
	// A token already in the stored text passed the write scan: not refused again.
	if _, err = Expand(Options{Point: Render, Resolver: vars, Locate: locate, Scanner: tokenScanner(t)}, "body", "tok_abcdefghijkl ${var:host}"); err != nil {
		t.Fatalf("stored token refused: %v", err)
	}
	// No Locate: render keeps every var (fail closed).
	got, err = Expand(Options{Point: Render, Resolver: vars}, "body", "${var:host}")
	if err != nil || got.Text != "${var:host}" {
		t.Fatalf("fail closed: %q %v", got.Text, err)
	}
}
