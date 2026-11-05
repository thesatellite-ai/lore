// doctor_host.go — the host behind `lore doctor`: the operation doctor.run
// (ops/doctor) runs the core's store checks itself and asks this binary for
// the lore.db, sync, identity and PATH checks of runDoctor (doctor.go),
// mapped onto the operation's report (PASS3 ONE-PATH, P3-MISC-8).
package main

import (
	"context"
	"encoding/json"

	"saas/pkg/aicoder/lsync"

	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// doctorHost implements opsdoctor.Host.
type doctorHost struct{}

// Base implements opsdoctor.Host: runDoctor without the core checks (the
// operation runs those, once).
func (doctorHost) Base(ctx context.Context, fix bool) (_ opsdoctor.Base, err error) {
	defer asHostError(&err)
	r, err := runDoctor(ctx, &doctorFlags{fix: fix, skipCore: true})
	if err != nil {
		return opsdoctor.Base{}, err
	}
	b := opsdoctor.Base{Status: r.Status, DBOK: r.DBOK, WALSizeBytes: r.WALSizeBytes,
		Identity:      opsdoctor.Identity{Resolved: r.IdentityInfo.Resolved, Source: r.IdentityInfo.Source, Stable: r.IdentityInfo.Stable},
		PathConflicts: r.PathsConflict, Warnings: r.Warnings, Notes: r.Notes, Errors: r.Errors}
	if s := r.Sync; s != nil {
		b.Sync = &opsdoctor.Sync{Baselined: s.Baselined, DataDirPresent: s.DataDirPresent, TrackedFiles: s.TrackedFiles,
			PendingExports: s.PendingExports, OpenConflicts: s.OpenConflicts, TrashRows: s.TrashRows,
			Errors: doctorFileErrors(s.Errors), ProjectFiles: s.ProjectFiles, DanglingRefs: s.DanglingRefs,
			DuplicateGroups: s.DuplicateGroups, FileErrors: doctorFileErrors(s.FileErrors), Uncommitted: s.Uncommitted,
			DataIgnored: s.DataIgnored, MergeDriverOK: s.MergeDriverOK, InGitRepo: s.InGitRepo}
	}
	if r.RepoAddresses != nil {
		if b.RepoAddresses, err = json.Marshal(r.RepoAddresses); err != nil {
			return opsdoctor.Base{}, err
		}
	}
	return b, nil
}

// doctorFileErrors maps sync file errors onto the report's.
func doctorFileErrors(in []lsync.FileError) []opsdoctor.FileError {
	if len(in) == 0 {
		return nil
	}
	out := make([]opsdoctor.FileError, len(in))
	for i, e := range in {
		out[i] = opsdoctor.FileError{Path: e.Path, Kind: e.Kind, Detail: e.Detail}
	}
	return out
}
