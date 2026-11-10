package setupweb

import (
	"embed"
	"io/fs"
)

// UI is the seam between the open core and the enterprise web UI.
//
// The open core owns the setup page's server: the one-time token, the
// session cookie and CSRF value, the Host and Origin checks, the CSP and
// the allowlisted /v1 operation routes. It does not own the page's look.
// A UI supplies the static files the browser loads, and nothing else: it
// cannot add a route, a header or an operation, so a UI can never weaken
// the page's security.
//
// An enterprise build implements UI and hands it in through its own
// composition root (Options.UI). Open packages never import enterprise
// code (the layer guard in internal/layers enforces it); the open build
// passes no UI and serves DefaultUI: a plain page that names the CLI
// commands doing the same work.
//
// FS contract: the root holds index.html (served for /setup/ and every
// client-side route under it) and an assets/ folder (served under
// /setup/assets/ for the media types in assetTypes; anything else is
// 404). index.html must not carry an inline script or style: the CSP
// allows 'self' only.
type UI interface {
	// FS is the file tree described above.
	FS() fs.FS
	// Version identifies the build: a content hash, never a time, so two
	// builds of the same files report the same version.
	Version() string
}

// The open-core page: one HTML file and one stylesheet, no script.
//
//go:embed opencore
var openCore embed.FS

// openCoreDir is the embedded folder that is the default UI's root.
const openCoreDir = "opencore"

// openCoreVersion is the default UI's version (its files change only with
// the binary).
const openCoreVersion = "opencore"

// defaultUI serves the open-core page.
type defaultUI struct{ root fs.FS }

// FS implements UI.
func (d defaultUI) FS() fs.FS { return d.root }

// Version implements UI.
func (defaultUI) Version() string { return openCoreVersion }

// DefaultUI is the UI an open-core build serves when Options.UI is nil: a
// plain page saying the setup page comes with lore EE and listing
// `lore setup plan|check|apply` and `lore doctor --all`.
func DefaultUI() UI {
	root, err := fs.Sub(openCore, openCoreDir)
	if err != nil {
		// fs.Sub fails only on an invalid path name; openCoreDir is a
		// constant that is valid.
		panic("setupweb: embedded open-core page: " + err.Error())
	}
	return defaultUI{root: root}
}
