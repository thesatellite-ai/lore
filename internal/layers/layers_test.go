package layers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// modulePath is the root module.
const modulePath = "github.com/thesatellite-ai/lore"

// Layer ranks by top-level folder. A package may import packages of a rank
// lower than or equal to its own. op sits between adapters and engine: the
// engine implements op.Runner, so op must never import the engine.
var ranks = map[string]int{
	"model": 0,
	// skills is the embedded agent skill bundle (go:embed; ONBOARDING_DESIGN
	// 4.7.1): data plus stdlib only, so it sits at L0 and any layer may
	// import it.
	"skills":   0,
	"schema":   10,
	"logic":    10,
	"store":    20,
	"adapters": 30,
	"op":       35,
	"engine":   40,
	"ops":      50,
	"surface":  60,
	// ee is the folder reserved for enterprise code, which is not part of
	// this tree. Code there may import the open layers; only the
	// composition root may import it (eeImporters), so the open core
	// never depends on it.
	"ee":  65,
	"cmd": 70,
	// app is the lore command itself (Run, Main, Options): the binary's
	// body, importable so another build composes it with Options. L7 beside
	// cmd and wire; only cmd may import it (appImporters).
	"app": 70,
	// wire is the composition root (L7 beside cmd): the only package that
	// picks concrete adapters; nothing may import it but cmd and the old
	// binary.
	"wire": 70,
	// tests holds the team scenario suite and its harness (tests/teame2e):
	// it may import anything; nothing imports it.
	"tests": 80,
}

// eeDir is the enterprise folder, and eeImporters the only folders that
// may import it: the composition root and the binary (ee itself too).
const eeDir = "ee"

var eeImporters = map[string]bool{"wire": true, "cmd": true, eeDir: true}

// appDir is the command package, and appImporters the only folders that
// may import it: a binary's main (and app itself). No core package may
// depend on how the binary is composed.
const appDir = "app"

var appImporters = map[string]bool{"cmd": true, appDir: true}

// internalDir holds test-only helpers (this package); nothing imports it.
const internalDir = "internal"

// forbidden import prefixes: the old modules being emptied and ent, which
// the new core never uses (no code generation).
var forbidden = []string{"saas", "dbent", "lace", "entgo.io/", "github.com/khanakia/entx"}

// modelAllowed are the only non-stdlib imports of model packages (L0).
var modelAllowed = []string{modulePath + "/model", "github.com/google/uuid"}

// pkg is the part of `go list -json` output the guard reads.
type pkg struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
	Deps       []string
}

// listPackages runs `go list -json ./...` at the module root.
func listPackages(t *testing.T) []pkg {
	t.Helper()
	rootOut, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", modulePath).Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	root := strings.TrimSpace(string(rootOut))
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list found no packages")
	}
	return pkgs
}

// topDir returns the first path element below the module, "" for a path
// outside the module.
func topDir(importPath string) string {
	rest, ok := strings.CutPrefix(importPath, modulePath+"/")
	if !ok {
		return ""
	}
	first, _, _ := strings.Cut(rest, "/")
	return first
}

func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// violations returns every layer rule p breaks.
func violations(p pkg) []string {
	var out []string
	top := topDir(p.ImportPath)
	rank, known := ranks[top]
	if !known && top != internalDir {
		return []string{p.ImportPath + ": folder " + top + " has no layer (add it to ranks)"}
	}
	for _, dep := range append(append([]string(nil), p.Imports...), p.Deps...) {
		for _, f := range forbidden {
			if dep == f || strings.HasPrefix(dep, f+"/") || (strings.HasSuffix(f, "/") && strings.HasPrefix(dep, f)) {
				out = append(out, p.ImportPath+" depends on forbidden "+dep)
			}
		}
	}
	for _, imp := range p.Imports {
		if top == "model" && !isStdlib(imp) && !hasAnyPrefix(imp, modelAllowed) {
			out = append(out, p.ImportPath+" (L0) imports "+imp+": model may use stdlib and uuid only")
		}
		itop := topDir(imp)
		if itop == "" {
			continue
		}
		if itop == eeDir && !eeImporters[top] {
			out = append(out, p.ImportPath+" imports enterprise "+imp+": only wire and cmd may import ee/")
			continue
		}
		if itop == appDir && !appImporters[top] {
			out = append(out, p.ImportPath+" imports the command "+imp+": only cmd may import app/")
			continue
		}
		if itop == internalDir {
			out = append(out, p.ImportPath+" imports test-only "+imp)
			continue
		}
		irank, ok := ranks[itop]
		if !ok {
			out = append(out, p.ImportPath+" imports "+imp+" in a folder with no layer")
			continue
		}
		if known && irank > rank {
			out = append(out, p.ImportPath+" (layer "+top+") imports higher layer "+imp)
		}
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}

// TestImportLayers fails on any upward import or forbidden dependency.
func TestImportLayers(t *testing.T) {
	pkgs := listPackages(t)
	for _, p := range pkgs {
		for _, v := range violations(p) {
			t.Error(v)
		}
	}
}

// TestGuardCatchesViolations proves the rule itself with synthetic packages,
// so the guard can never pass vacuously.
func TestGuardCatchesViolations(t *testing.T) {
	cases := map[string]pkg{
		"upward":         {ImportPath: modulePath + "/model/row", Imports: []string{modulePath + "/schema"}},
		"op to engine":   {ImportPath: modulePath + "/op", Imports: []string{modulePath + "/engine"}},
		"store to op":    {ImportPath: modulePath + "/store", Imports: []string{modulePath + "/op"}},
		"old module":     {ImportPath: modulePath + "/engine", Imports: []string{"saas/pkg/aicoder/ids"}},
		"ent transit":    {ImportPath: modulePath + "/engine", Deps: []string{"entgo.io/ent"}},
		"model 3rd pty":  {ImportPath: modulePath + "/model/ids", Imports: []string{"golang.org/x/text"}},
		"no layer":       {ImportPath: modulePath + "/misc"},
		"internal use":   {ImportPath: modulePath + "/engine", Imports: []string{modulePath + "/internal/layers"}},
		"ee from open":   {ImportPath: modulePath + "/surface/setupweb", Imports: []string{modulePath + "/ee/webui"}},
		"ee from tests":  {ImportPath: modulePath + "/tests/teame2e", Imports: []string{modulePath + "/ee/webui"}},
		"ee from ops":    {ImportPath: modulePath + "/ops/setup", Imports: []string{modulePath + "/ee/webui"}},
		"app from wire":  {ImportPath: modulePath + "/wire", Imports: []string{modulePath + "/app"}},
		"app from tests": {ImportPath: modulePath + "/tests/teame2e", Imports: []string{modulePath + "/app"}},
	}
	for name, p := range cases {
		if len(violations(p)) == 0 {
			t.Errorf("%s: not caught", name)
		}
	}
	for _, ok := range []pkg{
		{ImportPath: modulePath + "/engine", Imports: []string{modulePath + "/op", modulePath + "/store", "context"}},
		{ImportPath: modulePath + "/wire", Imports: []string{modulePath + "/ee/webui"}},
		{ImportPath: modulePath + "/cmd/lore", Imports: []string{modulePath + "/ee/webui"}},
		{ImportPath: modulePath + "/cmd/lore", Imports: []string{modulePath + "/app"}},
		{ImportPath: modulePath + "/app", Imports: []string{modulePath + "/wire", modulePath + "/surface/setupweb"}},
		{ImportPath: modulePath + "/ee/webui", Imports: []string{modulePath + "/surface/setupweb"}},
	} {
		if v := violations(ok); len(v) != 0 {
			t.Errorf("legal imports flagged: %v", v)
		}
	}
}

// files lists every non-test Go file of the module.
func files(t *testing.T) []string {
	pkgs := listPackages(t)
	var out []string
	for _, p := range pkgs {
		for _, f := range p.GoFiles {
			out = append(out, filepath.Join(p.Dir, f))
		}
	}
	return out
}
