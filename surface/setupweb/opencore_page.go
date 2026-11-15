package setupweb

import (
	"bytes"
	"html/template"
	"net/http"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// The open-core page names the binary that serves it (devco D-419): a
// user who runs a stale or source-built lore by mistake lands on this
// page instead of the guided one, and without the version and the path
// they cannot tell which lore answered. The values are filled in by the
// server when it answers /setup/, so the page stays free of script (the
// CSP allows no inline script) and needs no extra route.

// openCoreIndex is the default UI's index.html read as a template; every
// value is escaped by html/template.
var openCoreIndex = template.Must(template.ParseFS(openCore, openCoreDir+"/"+fileIndex))

// unknownValue is shown when the version or the path cannot be read.
const unknownValue = "unknown"

// openCoreFacts is what the open-core page shows about the binary. Only
// the version string and the executable's path: never the environment,
// the lore home or anything else that could be private.
type openCoreFacts struct {
	Version string
	Path    string
}

// binaryPath is this process's executable with symlinks resolved, so a
// link on PATH shows the file it points to; "unknown" when the OS does
// not say.
func binaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return unknownValue
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// writeOpenCoreIndex answers /setup/ (and the SPA fallback) with the
// open-core page filled with this binary's version and path.
func (p *Page) writeOpenCoreIndex(w http.ResponseWriter) {
	facts := openCoreFacts{Version: p.opts.LoreVersion, Path: binaryPath()}
	if facts.Version == "" {
		facts.Version = unknownValue
	}
	var buf bytes.Buffer
	if err := openCoreIndex.Execute(&buf, facts); err != nil {
		rest.WriteError(w, errs.Wrap(errs.Internal, "setupweb: render the open-core page", err))
		return
	}
	w.Header().Set(headerContentType, mediaHTML)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes()) // a failed write means the browser went away
}
