package layers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// anyAllowlist names "<module-relative file>#<func>" places where a written
// `any` or `interface{}` is forced by a stdlib signature. Keys are by
// function, not line, so an unrelated edit above the site never breaks the
// guard, and module-relative so they work in every checkout. Each entry
// needs a comment at the site saying why and must narrow at once.
var anyAllowlist = map[string]bool{
	// (*sql.Rows).Scan takes ...any and a query's column count is known only
	// at run time; every cell is narrowed to value.Value in the same loop.
	"adapters/cache/sqlite/scan.go#scanAll": true,
}

// enclosingFunc names the top-level function (or method, as Type.Method)
// whose body holds pos, or "" for package-level code: a package-level `any`
// can never be allowlisted.
func enclosingFunc(f *ast.File, pos token.Pos) string {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || pos < fd.Pos() || pos >= fd.End() {
			continue
		}
		if fd.Recv != nil && len(fd.Recv.List) == 1 {
			t := fd.Recv.List[0].Type
			if st, isPtr := t.(*ast.StarExpr); isPtr {
				t = st.X
			}
			if id, isIdent := t.(*ast.Ident); isIdent {
				return id.Name + "." + fd.Name.Name
			}
		}
		return fd.Name.Name
	}
	return ""
}

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
		for _, pos := range anyUses(fset, f) {
			fn := enclosingFunc(f, fset.File(f.Pos()).Pos(pos.Offset))
			key := rel + "#" + fn
			if fn != "" && anyAllowlist[key] {
				used[key] = true
				continue
			}
			t.Errorf("%s:%d: `any` or interface{} in hand-written code (allowlist key %q)", rel, pos.Line, key)
		}
	}
	// A stale entry would silently allow a future `any` in that function.
	for key := range anyAllowlist {
		if !used[key] {
			t.Errorf("anyAllowlist entry %q matches nothing; remove it", key)
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

// moduleRoot is the directory of the root module, for module-relative keys.
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", modulePath).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
