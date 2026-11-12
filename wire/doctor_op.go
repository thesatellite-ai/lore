package wire

// The core side of `lore doctor` (ops/doctor, P3-MISC-8): the five store
// checks of doctor_checks.go, the three of doctor_more.go, the three of
// doctor_machines.go, the three of doctor_local.go and doctor_class.go mapped onto the operation's types, and the
// binary's own lore.db checks from Options.Context.Doctor.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
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
		{DoctorCheckProjects, a.DoctorProjects}, // doctor_projects.go
		{DoctorCheckDuplicateRepos, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorDuplicateRepos(c, fix) }}, // doctor_duprepos.go (B2-7)
		{DoctorCheckKindRoute, a.DoctorKindRoutes},              // doctor_kindroutes.go (G-META-9)
		{DoctorCheckIdentityContacts, a.DoctorIdentityContacts}, // doctor_identity.go (TEAM_PROFILES 6.4)
		{DoctorCheckStoreClone, a.DoctorStoreClones},            // doctor_clones.go
		{DoctorCheckRemovedFolder, a.DoctorRemovedFolders},      // doctor_removedfolder.go
		{DoctorCheckStoreReadme, a.DoctorStoreReadmes},          // doctor_storereadme.go
		{DoctorCheckStoreFiles, a.DoctorStoreFiles},             // doctor_storefiles.go
		{DoctorCheckStoreIdentity, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorStoreIdentities(c, fix) }}, // doctor_storeidentity.go
		{DoctorCheckConfigStyle, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorConfigStyle(c, fix) }},       // doctor_configstyle.go
		{DoctorCheckActorRoster, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorActorRoster(c, fix) }},       // doctor_roster.go
		{DoctorCheckActorCopies, a.DoctorActorCopies},                                                                       // doctor_actorcopies.go (SOURCE_OF_TRUTH rows 2-3)
		{DoctorCheckDuplicateActors, a.DoctorDuplicateActors},                                                               // doctor_dupactors.go
		{DoctorCheckSharedKeys, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorSharedKeys(c, fix) }},     // actor_merge.go
		{DoctorCheckPendingInvites, a.DoctorPendingInvites},                                                                 // doctor_invites.go
		{DoctorCheckOrgAdmin, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorOrgAdmin(c, fix) }},         // doctor_orgadmin.go
		{DoctorCheckEmptyRef, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorEmptyRefs(c, fix) }},        // doctor_emptyref.go
		{DoctorCheckIdentitySource, a.DoctorIdentitySource},                                                                 // doctor_identitysource.go (SOURCE_OF_TRUTH row 1)
		{DoctorCheckRenderedCopy, a.DoctorRenderedCopies},                                                                   // doctor_render.go (SOURCE_OF_TRUTH rows 10-12)
		{DoctorCheckSkillCopy, a.DoctorSkillCopies},                                                                         // doctor_skillcopies.go (SOURCE_OF_TRUTH row 13)
		{DoctorCheckStoreAccess, a.storeAccess(fix)},                                                                        // doctor_storeaccess.go (brief 3.7)
		{DoctorCheckStoreAddress, a.DoctorStoreAddresses},                                                                   // storeaddr.go (SOURCE_OF_TRUTH row 7)
		{DoctorCheckOrgSource, a.DoctorOrgSources},                                                                          // orgsource.go (SOURCE_OF_TRUTH row 9)
		{DoctorCheckRowFormat, a.DoctorRowFormat},                                                                           // doctor_rowformat.go
		{DoctorCheckOldDBEdits, a.DoctorOldDBEdits},                                                                         // v017edits.go
		{DoctorCheckHomeLeftovers, a.DoctorHomeLeftovers},                                                                   // hometidy.go
		{DoctorCheckOldRepoCache, func(c context.Context) ([]DoctorFinding, error) { return a.DoctorOldRepoCache(c, fix) }}, // doctor_oldrepocache.go (owner item 16)
		{DoctorCheckOldClones, a.DoctorOldClones},                                                                           // doctor_oldclones.go (plain files 1.6)
	}
	// A repo linked to a store folder by its remote opens without making
	// anything (its rows live in the folder), so the store checks run
	// there: doctor --fix in the client repo repairs the folder's rows
	// (owner report 2026-11-12). Its "holds no lore data" note comes from
	// the repo class check. Only a repo neither set up nor linked stops at
	// one note.
	if NoCreate(ctx) && a.gitRepoWithoutLore(ctx) && linkedByRemote(ctx, a.opts) == "" { // doctor_nocreate.go
		return []opsdoctor.CheckRun{{Check: string(DoctorCheckRepoClass), Findings: []opsdoctor.Finding{{Check: string(DoctorCheckRepoClass), Level: opsdoctor.LevelNote, Line: doctorNoProjectNote}}}}
	}
	// Outside any git work tree there is no repo store to open: a check
	// that needs it is skipped (one note below), not counted as a check
	// that could not run, while the machine-wide checks (caches, env,
	// machines) still report.
	outside := gitTopLevel(ctx, a.projectDir()) == ""
	skipped := false
	out := make([]opsdoctor.CheckRun, 0, len(checks)+1)
	// The checks share one read of each store (engine.WithReadSnapshot):
	// most list tables, and each List used to read the whole store again
	// (item 41). The engine's own writes drop what they change; a check
	// that fixed something (or failed part way) may have changed files
	// around the engine, so the next check starts a new snapshot.
	shared := engine.WithReadSnapshot(ctx)
	for _, c := range checks {
		found, err := c.run(shared)
		if fix && (err != nil || anyFixed(found)) {
			shared = engine.WithReadSnapshot(ctx)
		}
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

// anyFixed reports whether a check fixed any of its findings.
func anyFixed(found []DoctorFinding) bool {
	for _, f := range found {
		if f.Fixed {
			return true
		}
	}
	return false
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
