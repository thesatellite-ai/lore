package wire

// projectformat.go — the format of the project a command runs in (round
// "formatpin", D12): what its committed .lore/data/_meta.json records
// (rowfs.ReadProjectFormat), and for a file that says nothing, the rows:
// lore 0.1.17 row files (_v: 1) of a migrated table make it format 1,
// anything else format 2. Shown by lore doctor (project_format), lore
// where and lore migrate status; the write paths of this lore refuse a
// project whose format is older than the one it writes (OlderFormat).

import (
	"context"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// ProjectFormatAt is the format of the checkout at top (the folder holding
// .lore). Format is rowfs.ProjectFormatUnknown only when there is no
// .lore/data at all (not a lore project). An unreadable _meta.json is the
// error (fail closed: E_OLD_LORE).
func ProjectFormatAt(top string) (rowfs.ProjectFormat, error) {
	if top == "" {
		return rowfs.ProjectFormat{}, nil
	}
	pf, err := rowfs.ReadProjectFormat(top)
	if err != nil || pf.Format != rowfs.ProjectFormatUnknown {
		return pf, err
	}
	if _, serr := os.Stat(filepath.Join(top, rowfs.LoreDir, rowfs.DataDir)); serr != nil {
		return pf, nil // no project here
	}
	if hasV017Rows(top) {
		pf.Format = rowfs.ProjectFormatV017
	} else {
		pf.Format = rowfs.ProjectFormatCurrent
	}
	return pf, nil
}

// ProjectFormat is ProjectFormatAt for the repository this App works in
// (zero outside a git repository).
func (a *App) ProjectFormat(ctx context.Context) (rowfs.ProjectFormat, error) {
	return ProjectFormatAt(gitTopLevel(ctx, a.projectDir()))
}

// OlderFormat reports whether pf is a known format older than the one
// this lore writes: the project is read normally, never written (D13).
func OlderFormat(pf rowfs.ProjectFormat) bool {
	return pf.Format != rowfs.ProjectFormatUnknown && pf.Format < rowfs.ProjectFormatCurrent
}

// ProjectFormatLine is how lore names a project's format in doctor, where
// and migrate status: "the lore 0.1.17 format (format 1)", plus who
// migrated it and when when recorded.
func ProjectFormatLine(pf rowfs.ProjectFormat) string {
	s := rowfs.FormatName(pf.Format)
	switch {
	case pf.MigratedBy != "" && pf.MigratedAt != "":
		s += ", migrated by " + pf.MigratedBy + " at " + pf.MigratedAt
	case pf.MigratedBy != "":
		s += ", migrated by " + pf.MigratedBy
	}
	if OlderFormat(pf) {
		s += "; this lore reads it and does not write it until someone runs lore migrate run"
	}
	return s
}
