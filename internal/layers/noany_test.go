package layers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// anyAllowlist names "file:line" positions where a written `any` or
// `interface{}` is forced by a stdlib signature. Each entry needs a comment
// at the site saying why. Empty today: the stdlib boundaries the code meets
// (json.Token, reflect.Value.Interface, fmt) return `any` without the code
// ever writing the word, and are narrowed on the same line.
var anyAllowlist = map[string]bool{}

// anyUses returns the positions in src where `any` or an empty interface is
// written as a type, ignoring type-parameter constraints (`[T any]`), where
// `any` means "no constraint", not "untyped value".
func anyUses(fset *token.FileSet, f *ast.File) []token.Position {
	type span struct{ from, to token.Pos }
	var skip []span
	ast.Inspect(f, func(n ast.Node) bool {
		var tp *ast.FieldList
		switch x := n.(type) {
		case *ast.FuncType:
			tp = x.TypeParams
		case *ast.TypeSpec:
			tp = x.TypeParams
		}
		if tp != nil {
			skip = append(skip, span{tp.Pos(), tp.End()})
		}
		return true
	})
	inSkip := func(p token.Pos) bool {
		for _, s := range skip {
			if p >= s.from && p < s.to {
				return true
			}
		}
		return false
	}
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == "any" && !inSkip(x.Pos()) {
				out = append(out, fset.Position(x.Pos()))
			}
		case *ast.InterfaceType:
			if len(x.Methods.List) == 0 && !inSkip(x.Pos()) {
				out = append(out, fset.Position(x.Pos()))
			}
		}
		return true
	})
	return out
}

// TestNoAny fails on any written `any` / `interface{}` in hand-written,
// non-test code outside the allowlist.
func TestNoAny(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range files(t) {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, pos := range anyUses(fset, f) {
			key := pos.Filename + ":" + itoa(pos.Line)
			if !anyAllowlist[key] {
				t.Errorf("%s: `any` or interface{} in hand-written code", key)
			}
		}
	}
}

// TestNoAnyCatches proves the AST rule on a sample.
func TestNoAnyCatches(t *testing.T) {
	src := `package x
func ok[T any](v T) T { return v }
type Box[T any] struct{ v T }
var a any
func f(v interface{}) {}
func g() map[string]any { return nil }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range anyUses(fset, f) {
		lines = append(lines, itoa(p.Line))
	}
	if strings.Join(lines, ",") != "4,5,6" {
		t.Fatalf("flagged lines %v, want 4,5,6", lines)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
