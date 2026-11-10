package setupweb

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// The page's static files come from a UI (ui.go): the one the binary was
// composed with, else DefaultUI. They are
// embedded in the binary, so the page needs no CDN and no network at run
// time. Two routes read them:
//
//	GET /setup/assets/{file...}  one file of the UI's assets/ folder, by extension
//	GET /setup/{path...}         index.html, for /setup/ and every client-side route
//
// The second is the SPA fallback: the browser app routes with history
// URLs under /setup/, so a reload of /setup/team must answer the page.
// It never answers for the server's own names (assets, session, close),
// so a GET of /setup/close is a 404 and never the page.
const (
	fileIndex = "index.html"
	// uiAssetDir is the folder inside a UI's FS served under pathAssets.
	uiAssetDir = "assets"
	mediaHTML  = "text/html; charset=utf-8"
)

// assetTypes maps each extension served under /setup/assets/ to its media
// type; any other extension is 404, so a stray file in a UI build (a
// source map, a README) is never served.
var assetTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".txt":   "text/plain; charset=utf-8",
}

// reservedSegments are the first path segments under /setup/ that belong
// to the server; the SPA fallback never answers them.
var reservedSegments = map[string]bool{
	uiAssetDir: true,
	"session":  true,
	"close":    true,
}

// ui returns the configured UI, or DefaultUI when none was given.
func (p *Page) ui() UI {
	if p.opts.UI != nil {
		return p.opts.UI
	}
	return DefaultUI()
}

// serveIndex serves the UI's index.html for /setup/ and for every
// client-side route under it (the SPA fallback).
func (p *Page) serveIndex(w http.ResponseWriter, r *http.Request) {
	first, _, _ := strings.Cut(r.PathValue("path"), "/")
	if reservedSegments[first] {
		rest.WriteError(w, errs.New(errs.NotFound, "no route "+r.Method+" "+r.URL.Path))
		return
	}
	p.writeFile(w, fileIndex, mediaHTML)
}

// serveAsset serves one file of the UI's assets/ folder: a clean relative
// path (no "..", no empty or dot segment), a known extension, a regular
// file. A folder is 404, never a listing.
func (p *Page) serveAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	media, ok := assetTypes[path.Ext(name)]
	full := uiAssetDir + "/" + name
	if !ok || !fs.ValidPath(full) {
		rest.WriteError(w, errs.New(errs.NotFound, "no asset "+quote(name)))
		return
	}
	p.writeFile(w, full, media)
}

// writeFile writes one regular file of the UI (the safety headers are
// already set): 404 when it is missing or a folder.
func (p *Page) writeFile(w http.ResponseWriter, name, media string) {
	root := p.ui().FS()
	info, err := fs.Stat(root, name)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		rest.WriteError(w, errs.New(errs.NotFound, "no asset "+quote(name)))
		return
	}
	if err != nil {
		rest.WriteError(w, errs.Wrap(errs.Internal, "setupweb: stat "+name, err))
		return
	}
	b, err := fs.ReadFile(root, name)
	if err != nil {
		rest.WriteError(w, errs.Wrap(errs.Internal, "setupweb: read "+name, err))
		return
	}
	w.Header().Set(headerContentType, media)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b) // a failed write means the browser went away
}
