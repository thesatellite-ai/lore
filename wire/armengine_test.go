package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryAppOpenArmsTheEngine (devco D-694): each function that opens
// an App's state beside the full open (openDetached, openDetachedHere:
// one store or folder outside a repo, an org folder, the machine store)
// installs the App's hooks with armEngine, and so does the full open
// (openState). The store-only open of `--to <org>/<folder>` from a folder
// with no lore project set only the redact hook, so a member's write there
// skipped the role rule. open.go builds the states those opens return and
// is armed by its callers.
func TestEveryAppOpenArmsTheEngine(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	armed := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "open.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			opens, arms := fn.Name.Name == "openState", false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch name := calledName(c); name {
				case "openDetached", "openDetachedHere":
					opens = true
				case "armEngine":
					arms = true
				}
				return true
			})
			if opens {
				armed[f+": "+fn.Name.Name] = arms
			}
		}
	}
	if len(armed) < 2 {
		t.Fatalf("found %d App opens, want the full open and the opens beside it: %v", len(armed), armed)
	}
	for at, ok := range armed {
		if !ok {
			t.Errorf("%s opens an App state without armEngine: its writes skip the role rule (write guard)", at)
		}
	}
}

// calledName is the bare name of the function c calls (x.f or f).
func calledName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}
