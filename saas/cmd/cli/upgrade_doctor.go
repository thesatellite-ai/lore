// upgrade_doctor.go — the doctor sections of the upgrade batch, called from
// doctor.go (owned by the coordinator):
//
//	doctorSchemaDrift  real columns vs this binary's schema (FORMAT_EVOLUTION 8.11 part 3)
//	doctorVersionPin   a pin below min_lore_version is an error; this binary vs the pin (8.4)
//	doctorTeamVersions per writer and machine, from the `_w` stamps (FQ3, OQ7)
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/thesatellite-ai/lore/logic/formatgate"
)

// doctorSchemaDrift makes the DB "broken" when a table or column this
// binary expects is missing, whatever the migration log says (the
// 2026-11-04 incident: the log passed, the column was missing).
func doctorSchemaDrift(ctx context.Context, db *sql.DB, r *doctorReport) {
	lines, err := schemaDriftLines(ctx, db)
	if err != nil {
		r.Warnings = append(r.Warnings, "schema drift: "+err.Error())
		degrade(r)
		return
	}
	if len(lines) == 0 {
		return
	}
	r.Errors = append(r.Errors, lines...)
	r.Status = doctorBroken
}

// doctorVersionPin checks the project pin (lore_version_pin in
// .lore/data/_meta.json, or LORE_VERSION_PIN) against the floor and this
// binary.
func doctorVersionPin(r *doctorReport) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	_, m, ok, err := findProjectMeta(cwd)
	if err != nil || !ok {
		return
	}
	p := effectivePin(m)
	if p.pin == "" {
		return
	}
	if m.MinLoreVersion != "" && formatgate.VersionBelow(p.pin, m.MinLoreVersion) {
		r.Errors = append(r.Errors, fmt.Sprintf("the pinned lore %s (%s) is below this project's min_lore_version %s: everyone on the pin is read-only; raise the pin",
			normVersion(p.pin), p.source, normVersion(m.MinLoreVersion)))
		r.Status = doctorBroken
	}
	if normVersion(p.pin) != normVersion(version) && !isDevBuild(version) {
		r.Notes = append(r.Notes, fmt.Sprintf("this lore is %s; the project pins %s (%s): run `%s upgrade`", normVersion(version), normVersion(p.pin), p.source, BinaryName))
	}
}

// doctorTeamVersions adds one "Team versions" note per writer and machine
// from the `_w` stamps of every attached cache (coreApp.TeamVersions: the
// repo and every store, OV28), and a warning for each one below the
// project floor. Rows without a stamp are older than 0.1.18 and not listed.
func doctorTeamVersions(ctx context.Context, repoTop string, r *doctorReport) {
	if coreApp == nil {
		return
	}
	tvs, err := coreApp.TeamVersions(ctx)
	if err != nil || len(tvs) == 0 {
		return
	}
	_, m, _, _ := findProjectMeta(repoTop)
	r.Notes = append(r.Notes, "Team versions (newest save per writer and machine):")
	for _, tv := range tvs {
		machine := tv.Machine
		if machine == "" {
			machine = "(code repo, no machine id)"
		} else if len(machine) > len("mch_")+8 {
			machine = machine[:len("mch_")+8] // shown as mch_ plus 8 hex (machine profile decision)
		}
		r.Notes = append(r.Notes, fmt.Sprintf("  %s on %s: lore %s (last save %s)", tv.Actor, machine, tv.Version, tv.LastSeen))
		if m.MinLoreVersion != "" && formatgate.VersionBelow(tv.Version, m.MinLoreVersion) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s on %s last wrote with lore %s, below min_lore_version %s: that lore is read-only here until it runs `%s upgrade`",
				tv.Actor, machine, tv.Version, m.MinLoreVersion, BinaryName))
			degrade(r)
		}
	}
}
