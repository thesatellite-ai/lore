package wire

// rowfile_notice.go — fail closed on row files a read had to leave out
// (not JSON, wrong path, over the cap, unresolved conflict markers): every
// command says so once on stderr (Options.Notice), and `lore doctor` is
// broken naming each file (DoctorRowFiles). v0.1.17 printed "invalid-file
// <path>" for the same case; 0.1.18 had dropped such files silently, so
// `rule list` showed "(no rules)" and doctor said healthy.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckRowFile names the unusable-row-file check.
const DoctorCheckRowFile DoctorCheck = "row_file"

// readProblemer is the optional backend upgrade of rowfs-based backends
// (git in repo mode, folder): the files the last Read left out.
type readProblemer interface {
	ReadProblems() []rowfs.Problem
}

// problemer is the doctor upgrade: every unusable entry of a store's data
// folder, read from disk now (store clones included).
type problemer interface {
	Problems(ctx context.Context) ([]rowfs.Problem, error)
}

// oneLine keeps a decode error (which may span lines) on its finding's
// one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// rowFilePlace is how a line names a file: its path from the store's top
// prefixed by its store ("store repo: .lore/data/rules/x.json", "store
// team: acme/rules/x.json").
func rowFilePlace(b store.Backend, src store.Source, folder, rel string) string {
	return "store " + string(src) + ": " + rowFileRel(b, src, folder, rel)
}

// What a left-out row file's line says about reads. A file that does not
// decode is in no read at all; a row whose text field or meta holds a
// conflict block (rowfs.Problem.FieldMarkers) is left out of lists and
// search, but show still prints it as it is so both sides can be read and
// one kept (rowfs decodeImportable: Get reads such a file on purpose).
const (
	rowFileNoticeLeftOut = "it is left out of every read (lore doctor)"
	rowFileDoctorLeftOut = "every read leaves it out"
	rowFileMarkersNote   = "lists and search leave it out until it is settled; show prints it with both sides (lore sync conflicts)"
)

// leftOutNote is the reads note of p: plain for an unusable file, the
// markers note for a row with a conflict block in a field.
func leftOutNote(p rowfs.Problem, plain string) string {
	if p.FieldMarkers {
		return rowFileMarkersNote
	}
	return plain
}

// rowFileNotices hands Options.Notice one line per row file the open's
// refresh left out. Called once per opened state from App.state, with
// a.mu held: it reads st only, never an App method (that would deadlock).
func (a *App) rowFileNotices(st *state) {
	if a.opts.Notice == nil || st == nil {
		return
	}
	names := make([]store.Source, 0, len(st.byName))
	for n := range st.byName {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		rp, ok := concrete(st.byName[n]).(readProblemer) // a live wrapper (WrapBackend) hides the method
		if !ok {
			continue
		}
		for _, p := range rp.ReadProblems() {
			line := "lore: invalid-file " + rowFilePlace(st.byName[n], n, st.folders[n], p.Path) + ": " + oneLine(p.Reason) + "; " + leftOutNote(p, rowFileNoticeLeftOut)
			if p.Hint != "" {
				line += "\n  next: " + p.Hint
			}
			a.opts.Notice(line)
		}
	}
}

// DoctorRowFiles names every entry of an attached store's data folder that
// is not a usable row; each is an error (doctor broken): rows the person
// wrote are missing from every read until the file is fixed.
func (a *App) DoctorRowFiles(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		pb, ok := concrete(st.byName[src]).(problemer)
		if !ok {
			continue
		}
		probs, perr := pb.Problems(ctx)
		if perr != nil {
			return out, perr
		}
		for _, p := range probs {
			next := "fix or remove it"
			if p.Hint != "" {
				next = p.Hint
			}
			out = append(out, DoctorFinding{Check: DoctorCheckRowFile, Level: DoctorError, Store: src, Plain: true,
				Message: rowFilePlace(st.byName[src], src, st.folders[src], p.Path) + " is not a usable row file: " + oneLine(p.Reason) + "; " + next + " (" + leftOutNote(p, rowFileDoctorLeftOut) + ")"})
		}
	}
	return out, nil
}
