package orgcfg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRouterStubsAreConcurrencySafe (devco D-774): a test Router stub that
// appended the asked url to a captured slice raced when GitLinks.prefetch
// asked it from several goroutines. No func literal of the Router shape
// (context.Context, string) ([]string, []string) in this package's tests
// may call append; it records through routeLog (routelog_test.go) instead.
func TestRouterStubsAreConcurrencySafe(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if name == "routelog_test.go" {
			continue // the guarded recorder itself
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.FuncLit)
			if !ok || !routerShaped(src, fset, lit.Type) {
				return true
			}
			ast.Inspect(lit.Body, func(m ast.Node) bool {
				if call, ok := m.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "append" {
						t.Errorf("%s: a Router stub appends without a lock (prefetch asks it from several goroutines); record through routeLog", fset.Position(call.Pos()))
					}
				}
				return true
			})
			return true
		})
	}
}

// routerShaped reports whether ft spells the Router signature.
func routerShaped(src []byte, fset *token.FileSet, ft *ast.FuncType) bool {
	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}
	if ft.Params == nil || ft.Results == nil || len(ft.Results.List) == 0 {
		return false
	}
	var params, results []string
	for _, p := range ft.Params.List {
		for range max(len(p.Names), 1) {
			params = append(params, text(p.Type))
		}
	}
	for _, r := range ft.Results.List {
		for range max(len(r.Names), 1) {
			results = append(results, text(r.Type))
		}
	}
	return strings.Join(params, ",") == "context.Context,string" && strings.Join(results, ",") == "[]string,[]string"
}
