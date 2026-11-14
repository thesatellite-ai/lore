package wire

// servestale.go — a long-running `lore api` server follows store moves
// (W-3, B3-15 follow-up). The App opens its state once and keeps every
// store's handle for its whole life; a CLI `lore store set-url` (or one
// sent to this server) rewrites where a store lives (config.yaml, the
// clone record that may now name another clone folder, the address
// record, the repo's .lore/stores.yaml) while the server keeps the old
// handle, so writes went to the old address or clone.
//
//	ServeAsPerson ──▶ watchPlaces on
//	Run (each request) ──▶ placesMoved: stat the place files the state
//	   was opened with ──▶ changed: retire the state (and the repo-only
//	   state); the next use opens fresh from the files as they are now
//
// A retired state is not closed at once: a request still running may hold
// it. Close closes every retired state with the App.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine/attach"
)

// FollowPlaces makes this App check, on each request, whether a place file
// (config.yaml, the clone and address records, the repo's stores.yaml)
// changed since its state was opened, and open again from the files as
// they are now: the watch ServeAsPerson turns on, without its person-only
// identity. The setup page serves a person through it, so the setup the
// page itself just saved (a new org, its identity, its stores) is what the
// page's next call runs as (devco J-16).
func (a *App) FollowPlaces() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.watchPlaces = true
}

// placeFiles are the files that say where this machine's stores live, as
// seen from the repo at top ("" = none).
func placeFiles(home, top string) []string {
	files := []string{
		filepath.Join(home, ConfigFile),
		gitbackend.CloneRecordPath(home),
		storeAddrRecordPath(home),
	}
	if top != "" {
		files = append(files, filepath.Join(top, filepath.FromSlash(attach.FileName)))
	}
	return files
}

// placesStamp is one line per place file: its size and modification time
// ("-" when it does not exist). Cheap: stats only, no read.
func placesStamp(files []string) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f)
		if st, err := os.Stat(f); err == nil {
			b.WriteString(" " + strconv.FormatInt(st.Size(), 10) + " " + strconv.FormatInt(st.ModTime().UnixNano(), 10))
		} else {
			b.WriteString(" -")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// notePlaces records, under a.mu, the place files and their stamp for the
// state just opened, so a later request can tell they changed.
func (a *App) notePlaces(st *state) {
	if !a.watchPlaces || st == nil {
		return
	}
	a.placeFiles = placeFiles(a.opts.Home, st.repoTop)
	a.placesAt = placesStamp(a.placeFiles)
}

// refreshMovedStores retires the opened states when a place file changed
// since they were opened (a store moved): the next use opens them again.
// Only a serving App watches (ServeAsPerson); a CLI command opens once.
func (a *App) refreshMovedStores() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.watchPlaces || !a.opened || a.st == nil || len(a.placeFiles) == 0 {
		return
	}
	if placesStamp(a.placeFiles) == a.placesAt {
		return
	}
	a.retired = append(a.retired, a.st)
	a.opened, a.st, a.err = false, nil, nil
	if a.cfg != nil {
		a.retired = append(a.retired, a.cfg)
	}
	a.cfgOpened, a.cfg, a.cfgErr = false, nil, nil
	a.placeFiles, a.placesAt = nil, ""
}
