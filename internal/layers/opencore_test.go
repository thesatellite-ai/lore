package layers

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The open-core boundary (issue #12, open core and enterprise split, option
// B): the public core is everything outside ee/, and no file outside ee/
// may import a package under ee/, so the public tree still builds when ee/
// (and web/) move to the private enterprise module.
//
// TestImportLayers already checks this per package through `go list`, but
// `go list` sees only the files of the host's build: it misses _test.go
// files, files behind a build tag or another GOOS, and packages that do not
// compile. This guard reads the source of every .go file instead, with
// go/parser (so a multi-line import block, an aliased or a blank import are
// all seen), and is stricter: wire and cmd count as outside ee/ too.

// eeImportPath is the import path of the enterprise folder.
const eeImportPath = modulePath + "/" + eeDir

// openCoreKnownEEImports lists the imports of ee/ outside ee/ that are
// allowed, as "<module-relative file> -> <import path>". The public tree
// has none: the guard fails on any import of ee/ at all, and on a listed
// one that no longer exists, so the list stays the true set.
var openCoreKnownEEImports = map[string]bool{}

// opencoreSkipDirs are folders that hold no hand-written Go of this module.
var opencoreSkipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "testdata": true}

// isEEImport reports whether an import path names ee/ or a package below it
// (never a sibling such as ee2/ or eex/).
func isEEImport(path string) bool {
	return path == eeImportPath || strings.HasPrefix(path, eeImportPath+"/")
}

// inEE reports whether a module-relative slash path lies under ee/.
func inEE(rel string) bool {
	return rel == eeDir || strings.HasPrefix(rel, eeDir+"/")
}

// eeImportsOutsideEE walks root (a module root) and returns every import of
// ee/ written in a .go file outside ee/, as "<relative file> -> <import>",
// sorted. A folder with its own go.mod below root is another module and is
// skipped. A file that does not parse is an error, never silently skipped,
// because skipping it would let an import through.
func eeImportsOutsideEE(root string) ([]string, error) {
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path == root {
				return nil
			}
			if opencoreSkipDirs[d.Name()] || inEE(rel) {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if isEEImport(imp) {
				found = append(found, rel+" -> "+imp)
			}
		}
		return nil
	})
	slices.Sort(found)
	return found, err
}

// TestOpenCoreImportsNoEnterprise fails when a file outside ee/ imports ee/
// and is not one of today's known seams, or when a known seam is gone.
func TestOpenCoreImportsNoEnterprise(t *testing.T) {
	found, err := eeImportsOutsideEE(moduleRoot(t))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range found {
		seen[f] = true
		if !openCoreKnownEEImports[f] {
			t.Errorf("open core imports enterprise code: %s (only ee/ may import ee/; hand the value in through a seam the open core owns, as surface/setupweb.UI does)", f)
		}
	}
	for k := range openCoreKnownEEImports {
		if !seen[k] {
			t.Errorf("known ee/ import %q no longer exists: remove it from openCoreKnownEEImports", k)
		}
	}
}

// TestOpenCoreGuardCatches runs the scanner over a synthetic module so the
// guard can never pass vacuously: every import shape is caught, and the
// imports it must not flag are not flagged.
func TestOpenCoreGuardCatches(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	q := strconv.Quote
	write("go.mod", "module "+modulePath+"\n")
	// Caught: every way of writing an import.
	write("ops/multi/multi.go", "package multi\n\nimport (\n\t\"context\"\n\n\t\"github.com/google/uuid\"\n\n\t"+q(eeImportPath+"/webui")+"\n)\n")
	write("surface/single/single.go", "package single\n\nimport "+q(eeImportPath+"/webui")+"\n")
	write("engine/alias/alias.go", "package alias\n\nimport (\n\tui "+q(eeImportPath+"/webui")+"\n\t_ "+q(eeImportPath+"/admin")+"\n)\n")
	write("logic/tagged/tagged.go", "//go:build ignore\n\npackage tagged\n\nimport "+q(eeImportPath)+"\n")
	write("ops/multi/multi_test.go", "package multi\n\nimport (\n\t\"testing\"\n\t"+q(eeImportPath+"/webui/sub")+"\n)\n")
	write("wire/seam.go", "package wire\n\nimport "+q(eeImportPath+"/webui")+"\n")
	// Not caught: ee/ itself, siblings named like ee, other modules, data.
	write("ee/webui/webui.go", "package webui\n\nimport "+q(eeImportPath+"/admin")+"\n")
	write("ee/admin/admin.go", "package admin\n\nimport "+q(modulePath+"/surface/setupweb")+"\n")
	write("ops/sibling/sibling.go", "package sibling\n\nimport (\n\t"+q(modulePath+"/eex/x")+"\n\t"+q(modulePath+"/ee2")+"\n\t"+q(modulePath+"/ops/ee")+"\n)\n")
	write("nested/go.mod", "module example.com/nested\n")
	write("nested/n.go", "package n\n\nimport "+q(eeImportPath+"/webui")+"\n")
	write("web/node_modules/x/x.go", "package x\n\nimport "+q(eeImportPath+"/webui")+"\n")
	write("ops/data/testdata/t.go", "package t\n\nimport "+q(eeImportPath+"/webui")+"\n")
	write("docs/notes.md", "import "+q(eeImportPath+"/webui")+"\n")

	got, err := eeImportsOutsideEE(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"engine/alias/alias.go -> " + eeImportPath + "/admin",
		"engine/alias/alias.go -> " + eeImportPath + "/webui",
		"logic/tagged/tagged.go -> " + eeImportPath,
		"ops/multi/multi.go -> " + eeImportPath + "/webui",
		"ops/multi/multi_test.go -> " + eeImportPath + "/webui/sub",
		"surface/single/single.go -> " + eeImportPath + "/webui",
		"wire/seam.go -> " + eeImportPath + "/webui",
	}
	if !slices.Equal(got, want) {
		t.Errorf("scan found\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// A file that does not parse is an error, never a silent pass.
	write("ops/broken/broken.go", "package broken\n\nimport (\n\t"+q(eeImportPath+"/webui")+"\n")
	if _, err := eeImportsOutsideEE(root); err == nil {
		t.Error("an unparsable file outside ee/ was skipped silently")
	}
}
