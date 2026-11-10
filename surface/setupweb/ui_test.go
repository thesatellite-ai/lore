package setupweb

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeUI is a UI over an in-memory tree, standing in for the EE build.
type fakeUI struct{ root fstest.MapFS }

func (f fakeUI) FS() fs.FS     { return f.root }
func (fakeUI) Version() string { return "fake" }
func newFakeUIRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	ui := fakeUI{root: fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html><title>spa</title>")},
		"assets/app-1a2b.js":       {Data: []byte("console.log(1)")},
		"assets/app-1a2b.css":      {Data: []byte("body{}")},
		"assets/logo.svg":          {Data: []byte("<svg/>")},
		"assets/font.woff2":        {Data: []byte("wOF2")},
		"assets/pic.png":           {Data: []byte("\x89PNG")},
		"assets/LICENSES.txt":      {Data: []byte("MIT")},
		"assets/app-1a2b.js.map":   {Data: []byte("{}")},
		"assets/sub/deep.js":       {Data: []byte("deep")},
		"assets/folder.js/x.js":    {Data: []byte("x")},
		"secret.js":                {Data: []byte("outside assets")},
		"assets/README.md":         {Data: []byte("readme")},
		"assets/archive.tar.gz":    {Data: []byte("gz")},
		"assets/noext":             {Data: []byte("noext")},
		"assets/.hidden.js":        {Data: []byte("hidden")},
		"assets/emptydir/keep.txt": {Data: []byte("k")},
	}}
	r.page.opts.UI = ui
	return r
}

// TestAssetTypes: every served extension gets its media type and the
// file's bytes; nested paths under assets/ work.
func TestAssetTypes(t *testing.T) {
	r := newFakeUIRig(t)
	for file, want := range map[string]string{
		"app-1a2b.js":  "text/javascript; charset=utf-8",
		"app-1a2b.css": "text/css; charset=utf-8",
		"logo.svg":     "image/svg+xml",
		"font.woff2":   "font/woff2",
		"pic.png":      "image/png",
		"LICENSES.txt": "text/plain; charset=utf-8",
		"sub/deep.js":  "text/javascript; charset=utf-8",
		".hidden.js":   "text/javascript; charset=utf-8",
	} {
		w := r.do(http.MethodGet, pathAssets+file, "", nil)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != want {
			t.Errorf("%s: %d %q", file, w.Code, w.Header().Get("Content-Type"))
			continue
		}
		data := r.page.opts.UI.FS().(fstest.MapFS)["assets/"+file].Data
		if w.Body.String() != string(data) {
			t.Errorf("%s: body %q", file, w.Body)
		}
	}
}

// TestAssetRefusals: unknown extensions, missing files, folders, files
// outside assets/ and dot-dot paths are never served.
func TestAssetRefusals(t *testing.T) {
	r := newFakeUIRig(t)
	for _, p := range []string{
		pathAssets + "app-1a2b.js.map",
		pathAssets + "README.md",
		pathAssets + "archive.tar.gz",
		pathAssets + "noext",
		pathAssets + "missing.js",
		pathAssets + "folder.js",
		pathAssets + "sub",
		pathAssets + "emptydir",
		pathAssets,
		pathAssets + "../secret.js",
		pathAssets + "..%2fsecret.js",
		pathAssets + "%2e%2e/secret.js",
		pathAssets + "sub/../../secret.js",
		pathAssets + "./app-1a2b.js",
		pathAssets + "sub//deep.js",
		"/setup/assets",
	} {
		w := r.do(http.MethodGet, p, "", nil)
		if w.Code == http.StatusOK {
			t.Errorf("%s served: %q", p, w.Body)
		}
		if strings.Contains(w.Body.String(), "outside assets") {
			t.Errorf("%s leaked a file outside assets/", p)
		}
	}
}

// TestSPAFallback: /setup/ and every client-side route answer index.html;
// the server's own names under /setup/ never do.
func TestSPAFallback(t *testing.T) {
	r := newFakeUIRig(t)
	for _, p := range []string{PagePath, "/setup/team", "/setup/steps/solo/3", "/setup/doctor?x=1", "/setup/join"} {
		w := r.do(http.MethodGet, p, "", nil)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != mediaHTML || !strings.Contains(w.Body.String(), "<title>spa</title>") {
			t.Errorf("%s: %d %q %q", p, w.Code, w.Header().Get("Content-Type"), w.Body)
		}
	}
	for _, p := range []string{PathClose, "/setup/close/x", "/setup/assets", "/setup/session/x", "/setup", "/other"} {
		w := r.do(http.MethodGet, p, "", nil)
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "spa") {
			t.Errorf("%s served the page", p)
		}
	}
	// POST to a client route is not the page either.
	if w := r.do(http.MethodPost, "/setup/team", `{}`, nil); w.Code == http.StatusOK {
		t.Errorf("POST /setup/team: %d", w.Code)
	}
}

// TestSPAMissingIndex: a UI without index.html is a 404, not a crash.
func TestSPAMissingIndex(t *testing.T) {
	r := newRig(t)
	r.page.opts.UI = fakeUI{root: fstest.MapFS{"assets/a.js": {Data: []byte("a")}}}
	if w := r.do(http.MethodGet, PagePath, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// TestDefaultUI: with no UI the open-core page is served, names the CLI
// commands, and its stylesheet loads.
func TestDefaultUI(t *testing.T) {
	r := newRig(t)
	if r.page.opts.UI != nil {
		t.Fatal("rig set a UI")
	}
	w := r.do(http.MethodGet, PagePath, "", nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != mediaHTML {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	for _, want := range []string{"lore EE", "lore setup plan", "lore setup check", "lore setup apply", "lore doctor --all", "/setup/assets/opencore.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("default page lacks %q", want)
		}
	}
	if w := r.do(http.MethodGet, "/setup/team", "", nil); w.Code != http.StatusOK || w.Body.String() != body {
		t.Errorf("default SPA fallback: %d", w.Code)
	}
	if w := r.do(http.MethodGet, pathAssets+"opencore.css", "", nil); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("opencore.css: %d %v", w.Code, w.Header())
	}
	if DefaultUI().Version() == "" {
		t.Error("default UI has no version")
	}
}

// TestCSPOnEveryUIResponse: the safety headers ride on assets, the
// fallback, refusals and redirects alike.
func TestCSPOnEveryUIResponse(t *testing.T) {
	r := newFakeUIRig(t)
	for _, p := range []string{PagePath, "/setup/x/y", pathAssets + "app-1a2b.js", pathAssets + "app-1a2b.js.map", pathAssets + "../secret.js", PathClose} {
		w := r.do(http.MethodGet, p, "", nil)
		if w.Header().Get("Content-Security-Policy") != CSP || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s (%d): headers %v", p, w.Code, w.Header())
		}
	}
}

// lenientFS is a careless UI tree: it cleans any name before the lookup,
// so "assets/../secret.js" opens secret.js. The embed and MapFS trees
// refuse such names themselves; the handler must not rely on that.
type lenientFS struct{ m fstest.MapFS }

func (l lenientFS) Open(name string) (fs.File, error) { return l.m.Open(path.Clean(name)) }

type lenientUI struct{ fakeUI }

func (l lenientUI) FS() fs.FS { return lenientFS{m: l.root} }

// TestAssetPathValidated calls the handler past the mux (which cleans
// dot-dot paths itself) with a lenient tree, so the handler's own check is
// what refuses: a path that is not a clean relative name is a 404.
func TestAssetPathValidated(t *testing.T) {
	r := newFakeUIRig(t)
	r.page.opts.UI = lenientUI{r.page.opts.UI.(fakeUI)}
	for _, name := range []string{"../secret.js", "sub/../../secret.js", "./app-1a2b.js", "sub//deep.js", "/abs.js"} {
		q := httptest.NewRequest(http.MethodGet, pathAssets+"x.js", nil)
		q.SetPathValue("file", name)
		w := httptest.NewRecorder()
		r.page.serveAsset(w, q)
		if w.Code != http.StatusNotFound {
			t.Errorf("%q: %d %s", name, w.Code, w.Body)
		}
	}
}

// TestSessionCarriesBuildFacts pins the About popover's data: Go version
// and platform always, the commit cut to commitLen when stamped.
func TestSessionCarriesBuildFacts(t *testing.T) {
	b := buildFacts()
	if b.Go == "" || b.OS == "" || b.Arch == "" {
		t.Fatalf("build facts missing: %+v", b)
	}
	if len(b.Commit) > commitLen {
		t.Fatalf("commit not cut: %q", b.Commit)
	}
}
