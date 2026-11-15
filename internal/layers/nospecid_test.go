package layers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"testing"
)

// literalSpecID is an internal spec or decision id in parentheses as it
// leaked into errors, hints, notes and policy docs: "(DQ4)", "(TSQ14)",
// "(TEAM-030)", "(BATCH 5)", "(PROVENANCE 11.6)". People who never read
// the specs see these strings (devco:T-16), so no string literal in
// hand-written, non-test code may carry one; comments may.
var literalSpecID = regexp.MustCompile(`\(([A-Z][A-Z_]*-?[0-9][0-9.]*[a-z]?(-[0-9]+)?|[A-Z][A-Z_]{2,} [0-9][0-9.]*)\)`)

// specIDAllowlist names "<module-relative file>#<func>" places whose
// literal is read only by developers (a registry rule a build fails on).
var specIDAllowlist = map[string]bool{
	// op.Define's own naming rule: it fails `go test` for a developer who
	// added a root-level verb, never a user's command.
	"op/rules.go#checkWords": true,
}

// TestNoSpecIDInStrings walks every string literal of the module's
// non-test Go files.
func TestNoSpecIDInStrings(t *testing.T) {
	fset := token.NewFileSet()
	root := moduleRoot(t)
	used := map[string]bool{}
	for _, path := range files(t) {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			m := literalSpecID.FindString(lit.Value)
			if m == "" {
				return true
			}
			key := rel + "#" + enclosingFunc(f, lit.Pos())
			if specIDAllowlist[key] {
				used[key] = true
				return true
			}
			t.Errorf("%s:%d: string carries the spec id %s (allowlist key %q)", rel, fset.Position(lit.Pos()).Line, m, key)
			return true
		})
	}
	for key := range specIDAllowlist {
		if !used[key] {
			t.Errorf("specIDAllowlist entry %q matches nothing; remove it", key)
		}
	}
}
