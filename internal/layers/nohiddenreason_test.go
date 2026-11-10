package layers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// hiddenReasonWraps returns the positions of `errs.Wrap(errs.CodeOf(x), msg,
// x)` calls whose msg does not already carry x's text (no `.Error()` call
// in it). Such a call keeps x's code but prints only msg: the surfaces
// (errs.EnvelopeOf: the CLI line, --json and every REST body) show Message
// and Hint and never Cause, by design (model/errs/error.go, Cause "may hold
// paths or driver text"), so the refusal's reason was lost. errs.Prefix
// keeps a lore error's message and hint behind msg and still hides a
// non-lore cause (INTEG-QUESTIONS-2). The walk is on the AST, so a call
// split over several lines is found too.
func hiddenReasonWraps(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSel(call.Fun, "errs", "Wrap") || len(call.Args) != 3 {
			return true
		}
		code, ok := call.Args[0].(*ast.CallExpr)
		if !ok || !isSel(code.Fun, "errs", "CodeOf") || len(code.Args) != 1 {
			return true
		}
		inner, ok1 := code.Args[0].(*ast.Ident)
		cause, ok2 := call.Args[2].(*ast.Ident)
		if !ok1 || !ok2 || inner.Name != cause.Name || callsError(call.Args[1]) {
			return true
		}
		out = append(out, fset.Position(call.Pos()))
		return true
	})
	return out
}

// isSel reports whether e is the selector pkg.name.
func isSel(e ast.Expr, pkg, name string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != name {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// callsError reports whether e calls a method named Error anywhere.
func callsError(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Error" {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestNoHiddenReason fails on a context-only errs.Wrap of a lore error in
// hand-written, non-test code; use errs.Prefix(msg, err) there.
func TestNoHiddenReason(t *testing.T) {
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
		for _, pos := range hiddenReasonWraps(fset, f) {
			t.Errorf("%s:%d: errs.Wrap(errs.CodeOf(err), msg, err) hides err's reason from the CLI and REST; use errs.Prefix(msg, err)", filepath.ToSlash(rel), pos.Line)
		}
	}
}

// TestNoHiddenReasonCatches proves the rule on a sample, a call split over
// lines included, and that a msg carrying err.Error() or another cause is
// left alone.
func TestNoHiddenReasonCatches(t *testing.T) {
	src := `package x
func a(err error) error { return errs.Wrap(errs.CodeOf(err), "ctx", err) }
func b(err error) error {
	return errs.Wrap(
		errs.CodeOf(err),
		"ctx "+name(),
		err)
}
func c(err error) error { return errs.Wrap(errs.CodeOf(err), "ctx: "+err.Error(), err) }
func d(err, other error) error { return errs.Wrap(errs.CodeOf(err), "ctx", other) }
func e(err error) error { return errs.Prefix("ctx", err) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, p := range hiddenReasonWraps(fset, f) {
		lines = append(lines, p.Line)
	}
	if len(lines) != 2 || lines[0] != 2 || lines[1] != 4 {
		t.Fatalf("flagged lines %v, want [2 4]", lines)
	}
}
