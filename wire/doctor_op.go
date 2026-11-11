package wire

// The core side of `lore doctor` (ops/doctor, P3-MISC-8): the five store
// checks of doctor_checks.go, the three of doctor_more.go, the three of
// doctor_machines.go, the three of doctor_local.go and doctor_class.go mapped onto the operation's types, and the
// binary's own lore.db checks from Options.Context.Doctor.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// DoctorHost is Options.Context.Doctor (opsdoctor.Env); ok is false in a
// binary without lore.db.
func (a *App) DoctorHost(_ context.Context) (opsdoctor.Host, bool) {
	return a.opts.Context.Doctor, a.opts.Context.Doctor != nil
}

// BinaryVersion is Options.Version (opsdoctor.Env).
func (a *App) BinaryVersion() string { return a.opts.Version }

// DoctorChecks runs the core's store checks in their fixed order
// (opsdoctor.Env); fix applies the task-lead write-back.
func (a *App) DoctorChecks(ctx context.Context, fix bool) []opsdoctor.CheckRun {
	checks := []struct {
		name DoctorCheck
		run  func(context.Context) ([]DoctorFinding, error)
	}{
		{DoctorCheckTaskLead, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorTaskLeads(c, fix) }},
		{DoctorCheckTriageOwner, a.DoctorTriageOwner},
		{DoctorCheckStoreLock, a.DoctorStoreLocks},
		{DoctorCheckEditAfterPurge, a.DoctorEditsAfterPurge},
		{DoctorCheckInternalOff, a.DoctorInternalOff},
		{DoctorCheckResurrected, a.DoctorResurrected},
		{DoctorCheckCache, a.DoctorCaches},
		{DoctorCheckRemovedEnv, a.DoctorRemovedEnv},
		{DoctorCheckRowFile, a.DoctorRowFiles},
		{DoctorCheckUntrustedHost, a.DoctorUntrustedHost},
		{DoctorCheckTrash, a.DoctorTrash},
		{DoctorCheckSharedMachine, a.DoctorSharedMachines},
		{DoctorCheckTeamVersions, a.DoctorTeamVersions},
		{DoctorCheckCombinedCache, a.DoctorCombinedCache},
		{DoctorCheckTimeOutbox, a.DoctorTimeOutbox},
		{DoctorCheckPurgedCopies, a.DoctorPurgedCopies},
		{DoctorCheckOldSettings, a.DoctorUnclassifiedSettings},
		{DoctorCheckRepoClass, a.DoctorRepoClass},
		{DoctorCheckSLA, a.DoctorSLA},
		{DoctorCheckProjects, a.DoctorProjects},                                                                           // doctor_projects.go
		{DoctorCheckKindRoute, a.DoctorKindRoutes},                                                                        // doctor_kindroutes.go (G-META-9)
		{DoctorCheckIdentityContacts, a.DoctorIdentityContacts},                                                           // doctor_identity.go (TEAM_PROFILES 6.4)
		{DoctorCheckStoreClone, a.DoctorStoreClones},                                                                      // doctor_clones.go
		{DoctorCheckRemovedFolder, a.DoctorRemovedFolders},                                                                // doctor_removedfolder.go
		{DoctorCheckConfigStyle, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorConfigStyle(c, fix) }}, // doctor_configstyle.go
		{DoctorCheckActorRoster, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorActorRoster(c, fix) }}, // doctor_roster.go
		{DoctorCheckActorCopies, a.DoctorActorCopies},                                                                     // doctor_actorcopies.go (SOURCE_OF_TRUTH rows 2-3)
		{DoctorCheckDuplicateActors, a.DoctorDuplicateActors},                                                             // doctor_dupactors.go
		{DoctorCheckIdentitySource, a.DoctorIdentitySource},                                                               // doctor_identitysource.go (SOURCE_OF_TRUTH row 1)
		{DoctorCheckRenderedCopy, a.DoctorRenderedCopies},                                                                 // doctor_render.go (SOURCE_OF_TRUTH rows 10-12)
		{DoctorCheckSkillCopy, a.DoctorSkillCopies},                                                                       // doctor_skillcopies.go (SOURCE_OF_TRUTH row 13)
		{DoctorCheckStoreAddress, a.DoctorStoreAddresses},                                                                 // storeaddr.go (SOURCE_OF_TRUTH row 7)
		{DoctorCheckOrgSource, a.DoctorOrgSources},                                                                        // orgsource.go (SOURCE_OF_TRUTH row 9)
		{DoctorCheckRowFormat, a.DoctorRowFormat},                                                                         // doctor_rowformat.go
		{DoctorCheckOldDBEdits, a.DoctorOldDBEdits},                                                                       // v017edits.go
	}
	if NoCreate(ctx) && a.gitRepoWithoutLore(ctx) { // doctor_nocreate.go
		line := doctorNoProjectNote
		if remote := linkedByRemote(ctx, a.opts); remote != "" {
			line = doctorLinkedByRemoteNote(remote)
		}
		return []opsdoctor.CheckRun{{Check: string(DoctorCheckRepoClass), Findings: []opsdoctor.Finding{{Check: string(DoctorCheckRepoClass), Level: opsdoctor.LevelNote, Line: line}}}}
	}
	// Outside any git work tree there is no repo store to open: a check
	// that needs it is skipped (one note below), not counted as a check
	// that could not run, while the machine-wide checks (caches, env,
	// machines) still report.
	outside := gitTopLevel(ctx, a.projectDir()) == ""
	skipped := false
	out := make([]opsdoctor.CheckRun, 0, len(checks)+1)
	for _, c := range checks {
		found, err := c.run(ctx)
		if outside && errs.Has(err, errs.NotAStore) {
			skipped = true
			continue
		}
		run := opsdoctor.CheckRun{Check: string(c.name), Err: err}
		for _, f := range found {
			run.Findings = append(run.Findings, opsdoctor.Finding{Check: string(f.Check), Level: doctorLevel(f.Level), Fixed: f.Fixed, Line: f.Line(), Fix: f.Fix})
		}
		out = append(out, run)
	}
	if skipped {
		note := doctorNoRepoNote
		if gitBareRepo(ctx, a.projectDir()) {
			note = doctorBareRepoNote
		}
		out = append(out, opsdoctor.CheckRun{Check: string(DoctorCheckRepoClass), Findings: []opsdoctor.Finding{{Check: string(DoctorCheckRepoClass), Level: opsdoctor.LevelNote, Line: note}}})
	}
	return out
}

// doctorNoRepoNote is DoctorChecks' one line when the command's folder is
// in no git repo (instead of every repo store check failing to run).
const doctorNoRepoNote = "not inside a git repo, so the repo store checks were skipped (run lore doctor in a repo to check its project)"

// doctorBareRepoNote is doctorNoRepoNote for a bare repository: it is a
// git repo, so "not inside a git repo" would be wrong; it has no
// checked-out files, so no lore project can live in it.
const doctorBareRepoNote = "in a bare git repository (no checked-out files), so the repo store checks were skipped (run lore doctor in a clone of it)"

// gitBareRepo reports whether dir is a bare repository (or inside one).
func gitBareRepo(ctx context.Context, dir string) bool {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--is-bare-repository"}}.output(ctx)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// doctorLevel maps a finding level onto the operation's (exhaustive).
func doctorLevel(l DoctorLevel) string {
	switch l {
	case DoctorError:
		return opsdoctor.LevelError
	case DoctorWarning:
		return opsdoctor.LevelWarning
	case DoctorNote:
		return opsdoctor.LevelNote
	}
	return opsdoctor.LevelWarning // an unknown level fails closed: it degrades
}
