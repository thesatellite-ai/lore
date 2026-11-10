package layers

// The value-set guard: a named string type that implements op.Enumerated
// (EnumValues) is the one source of the `enum` the OpenAPI document lists
// for its fields, and the setup page's contract test compares its own
// unions with that list. So EnumValues must list exactly the constants the
// type's package declares with that type: a new constant left out of the
// list would be a value the page never learns about, silently.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/doctor"
	opsreveal "github.com/thesatellite-ai/lore/ops/reveal"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// enumMethod is the op.Enumerated method name the source scan looks for.
const enumMethod = "EnumValues"

// enumerated maps "<module-relative package dir>.<Type>" to a value of
// every type that implements op.Enumerated. TestEnumValuesMatchConstants
// fails when the source has an EnumValues method this table lacks, so a
// new enumerated type is always pinned.
var enumerated = map[string]op.Enumerated{
	"logic/onboard.Kind":            onboard.Kind(""),
	"logic/onboard.Status":          onboard.Status(""),
	"logic/onboard.StepStatus":      onboard.StepStatus(""),
	"logic/onboard.QType":           onboard.QType(""),
	"logic/onboard.Level":           onboard.Level(""),
	"logic/onboard.HereState":       onboard.HereState(""),
	"logic/onboard.GitRoute":        onboard.GitRoute(""),
	"logic/onboard.LeftoverKind":    onboard.LeftoverKind(""),
	"ops/reveal.Target":             opsreveal.Target(""),
	"logic/toolprofile.SkillStatus": toolprofile.SkillStatus(""),
	"ops/doctor.Health":             doctor.Health(""),
	"ops/setup.FolderKind":          opssetup.FolderKind(""),
	"ops/setup.SourceKind":          opssetup.SourceKind(""),
}

// enumSkipDirs are folders that hold no root-module Go source.
var enumSkipDirs = map[string]bool{".git": true, "node_modules": true, "web": true, "docs": true, "testdata": true}

// enumSource is what the scan found for one package: the types with an
// EnumValues method, and every constant value declared with each named
// type.
type enumSource struct {
	methods map[string]bool
	consts  map[string][]string
}

// scanEnums parses every non-test Go file of the module.
func scanEnums(t *testing.T) map[string]*enumSource {
	t.Helper()
	root := filepath.Join("..", "..")
	out := map[string]*enumSource{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if enumSkipDirs[d.Name()] || (path != root && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		src := out[filepath.ToSlash(rel)]
		if src == nil {
			src = &enumSource{methods: map[string]bool{}, consts: map[string][]string{}}
			out[filepath.ToSlash(rel)] = src
		}
		collectEnums(f, src)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// collectEnums adds f's EnumValues receivers and typed string constants.
func collectEnums(f *ast.File, src *enumSource) {
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Name.Name != enumMethod || x.Recv == nil || len(x.Recv.List) != 1 {
				continue
			}
			if id, ok := x.Recv.List[0].Type.(*ast.Ident); ok {
				src.methods[id.Name] = true
			}
		case *ast.GenDecl:
			if x.Tok != token.CONST {
				continue
			}
			for _, s := range x.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok || vs.Type == nil {
					continue
				}
				id, ok := vs.Type.(*ast.Ident)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if s, err := strconv.Unquote(lit.Value); err == nil {
						src.consts[id.Name] = append(src.consts[id.Name], s)
					}
				}
			}
		}
	}
}

// TestEnumValuesMatchConstants: every op.Enumerated type is in the table,
// and its EnumValues is exactly its non-empty constants (order free, each
// once). An empty constant (an "unset" value) is never listed: such a
// field is omitempty and left out of the JSON.
func TestEnumValuesMatchConstants(t *testing.T) {
	found := map[string]bool{}
	for dir, src := range scanEnums(t) {
		for typ := range src.methods {
			key := dir + "." + typ
			found[key] = true
			e, ok := enumerated[key]
			if !ok {
				t.Errorf("%s has an EnumValues method but is not in internal/layers enumerated: add it so its value set is pinned", key)
				continue
			}
			var want []string
			for _, c := range src.consts[typ] {
				if c != "" {
					want = append(want, c)
				}
			}
			got := slices.Clone(e.EnumValues())
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s.EnumValues() = %v, the constants of the type are %v: list every constant once", key, got, want)
			}
		}
	}
	for key := range enumerated {
		if !found[key] {
			t.Errorf("enumerated lists %s, which has no EnumValues method in the source", key)
		}
	}
}
