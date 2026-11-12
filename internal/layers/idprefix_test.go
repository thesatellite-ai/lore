package layers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// idHint is an id prefix as help and messages name it: "tlt_…".
var idHint = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])([a-z]{2,4})_…`)

// badIDHints returns each "<prefix>_…" in a string literal of f (flag
// descriptions are struct-tag literals) whose prefix model/ids does not
// register.
func badIDHints(fset *token.FileSet, f *ast.File, known map[string]bool) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		for _, m := range idHint.FindAllStringSubmatch(lit.Value, -1) {
			if !known[m[1]] {
				out = append(out, itoa(fset.Position(lit.Pos()).Line)+": "+m[1]+"_…")
			}
		}
		return true
	})
	return out
}

// TestIDHintsAreRealPrefixes keeps every id hint in help and messages
// ("new tasklist (tlt_…)") on a prefix model/ids registers: the hints are
// hand-written in desc tags, and `lore trash restore --help` once named
// tasklists "tl_…" while their ids are "tlt_…" (bug hunt B3-11).
func TestIDHintsAreRealPrefixes(t *testing.T) {
	known := map[string]bool{}
	for _, p := range ids.Prefixes() {
		known[string(p)] = true
	}
	fset := token.NewFileSet()
	root := moduleRoot(t)
	for _, path := range files(t) {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range badIDHints(fset, f, known) {
			t.Errorf("%s:%s is not a registered id prefix (model/ids)", filepath.ToSlash(rel), bad)
		}
	}
}

// TestIDHintsCatches proves the rule on a sample.
func TestIDHintsCatches(t *testing.T) {
	src := "package x\ntype In struct {\n\tA string `desc:\"new tasklist (tl_…)\"`\n\tB string `desc:\"a task (tsk_…)\"`\n}\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := badIDHints(fset, f, map[string]bool{"tsk": true})
	if len(got) != 1 || got[0] != "3: tl_…" {
		t.Fatalf("got %v, want [3: tl_…]", got)
	}
}
