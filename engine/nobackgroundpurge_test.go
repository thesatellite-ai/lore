package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// purgeEntryPoints are the method names that write a purge: the engine's
// own (Purge, PurgeTrash, PurgeOlder, PurgeArchived) and the names the
// ops and wire layers forward them under.
var purgeEntryPoints = map[string]bool{"Purge": true, "PurgeTrash": true, "PurgeOlder": true, "PurgeArchived": true}

// purgeCallers is every "<module-relative file>#<func>" allowed to call a
// purge entry point: each one is a named, explicit purge command (DELETION
// 3.8, DQ3: no sync, CI step, doctor run or other background path ever
// writes a purge). The live signals ref's own Purge (claims and leases,
// not rows) is listed by its callers too. A new caller fails the test
// until it is shown to be an explicit command and added here.
var purgeCallers = map[string]bool{
	// op.Input.Purge() reads a delete's --purge flags (no write); the
	// delete itself purges only on an explicit --purge --confirm.
	"engine/purgeflags.go#bindInputPurge": true,
	// lore live purge (§6.6): the live signals ref, an explicit command.
	"ops/live/live.go#purge":      true,
	"wire/host/live_ref.go#Purge": true,
	// lore meta forget --confirm (key rows only).
	"ops/settings/meta_maint.go#metaForget": true,
	// lore setup trash purge <setup> --confirm: its machine.db step progress
	// rows (names, emails, store urls) go for good with the setup
	// (setupStore.PurgeProgress → purgeRow; setuplifecycle.go).
	"wire/setupstore.go#purgeRow": true,
	// lore trash purge <target|--older-than|--expired|--archived-before> --confirm.
	"ops/trash/trash.go#purge":             true,
	"ops/trash/trash.go#purgeArchived":     true,
	"wire/trash.go#PurgeOlder":             true,
	"wire/trash.go#PurgeTrash":             true,
	"wire/trash_archived.go#PurgeArchived": true,
}

// TestNoBackgroundPurge (DELETION 3.8, DQ3, later 424): the purge entry
// points are reached only from explicit purge commands. Name-based on the
// AST of every non-test file of the 0.1.18 module (the old modules saas,
// dbent and lace are skipped: they are deleted in phase E), so a new
// caller of any of the names fails loudly, never silently.
func TestNoBackgroundPurge(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	skip := map[string]bool{"saas": true, "dbent": true, "lace": true, "vendor": true, "tmp": true, ".git": true, "node_modules": true}
	var found []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if skip[rel] || (rel != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if isSel && purgeEntryPoints[sel.Sel.Name] {
					found = append(found, filepath.ToSlash(rel)+"#"+fd.Name.Name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	found = slices.Compact(found)
	if len(found) == 0 {
		t.Fatal("no purge caller found: the walk is broken (the explicit commands must show up)")
	}
	for _, c := range found {
		if !purgeCallers[c] {
			t.Errorf("purge entry point called from %s: not an allowlisted explicit purge command (DQ3: no background purge)", c)
		}
	}
	for c := range purgeCallers {
		if !slices.Contains(found, c) {
			t.Errorf("stale allowlist entry %s: it no longer calls a purge entry point", c)
		}
	}
}
