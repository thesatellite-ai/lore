package wire

// doctor_class.go — the `lore doctor` row that says why the repo store is
// treated as a client repo when nothing positively said so (later 324,
// clientrepo; F15 fail closed): no client-repo check is wired (a note), or
// the check failed, e.g. the org's links could not be read (a warning, the
// cause named). A positive answer either way prints nothing.

import (
	"context"
	"strings"
)

// DoctorCheckRepoClass is the JSON check name of the row.
const DoctorCheckRepoClass DoctorCheck = "repo_class"

// repoClassFailedMark is the part of repoIsClient's note that marks a
// failed check (as against an unwired one).
const repoClassFailedMark = "could not be checked"

// DoctorRepoClass reports App.RepoClassNote: none when Options.RepoIsClient
// answered, a note when it is unset, a warning when it failed.
func (a *App) DoctorRepoClass(ctx context.Context) ([]DoctorFinding, error) {
	note, err := a.RepoClassNote(ctx)
	if err != nil || note == "" {
		return nil, err
	}
	level := DoctorNote
	if strings.Contains(note, repoClassFailedMark) {
		level = DoctorWarning
	}
	return []DoctorFinding{{Check: DoctorCheckRepoClass, Level: level, Plain: true, Message: note}}, nil
}
