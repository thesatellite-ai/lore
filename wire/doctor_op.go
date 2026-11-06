package wire

// The core side of `lore doctor` (ops/doctor, P3-MISC-8): the five store
// checks of doctor_checks.go, the three of doctor_more.go, the three of
// doctor_machines.go, the three of doctor_local.go and doctor_class.go mapped onto the operation's types, and the
// binary's own lore.db checks from Options.Context.Doctor.

import (
	"context"

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
	}
	out := make([]opsdoctor.CheckRun, 0, len(checks))
	for _, c := range checks {
		found, err := c.run(ctx)
		run := opsdoctor.CheckRun{Check: string(c.name), Err: err}
		for _, f := range found {
			run.Findings = append(run.Findings, opsdoctor.Finding{Check: string(f.Check), Level: doctorLevel(f.Level), Fixed: f.Fixed, Line: f.Line()})
		}
		out = append(out, run)
	}
	return out
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
