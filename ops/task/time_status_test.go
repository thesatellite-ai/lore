package task

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusWritesGoThroughSetStatus is the D2 source scan (design 4.1,
// build step 4): outside time_status.go no file of this package may write
// the status field: no `work.FieldStatus: …` map key and no
// `x[work.FieldStatus] = …` assignment. It walks the Go syntax tree, so a
// write split over lines is still seen.
func TestStatusWritesGoThroughSetStatus(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	isStatus := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "FieldStatus" {
			return true
		}
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Value == `"status"`
	}
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "time_status.go" {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatal(perr)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				mt, isMap := x.Type.(*ast.MapType)
				if !isMap || !writesValue(mt.Value) {
					return true
				}
				for _, el := range x.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok && isStatus(kv.Key) {
						t.Errorf("%s: status written in a map literal; use setStatus (D2)", fset.Position(kv.Pos()))
					}
				}
			case *ast.AssignStmt:
				for _, l := range x.Lhs {
					if ix, ok := l.(*ast.IndexExpr); ok && isStatus(ix.Index) {
						t.Errorf("%s: status assigned directly; use setStatus (D2)", fset.Position(ix.Pos()))
					}
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no files")
	}
}

// writesValue reports a map value type a patch is built from: string (fed
// to work.Texts) or value.Value; lookup tables ([]string sets) are not.
func writesValue(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name == "string"
	case *ast.SelectorExpr:
		return v.Sel.Name == "Value"
	}
	return false
}
