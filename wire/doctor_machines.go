package wire

// doctor_machines.go — two `lore doctor` rows over the machine stamps and
// profiles, moved from the old binary (saas/cmd/cli doctor_machines.go,
// upgrade_doctor.go) into the core so cmd/lore prints them (later 518,
// OQ7, AGENT_IDENTITY 6.8). Lines are the v0.1.17 text word for word
// (DoctorFinding.Plain):
//
//	DoctorSharedMachines  a machine id whose profiles name two or more hosts: warning
//	DoctorTeamVersions    "Team versions" notes, one per (writer, machine); a
//	                      writer below the repo's min_lore_version: warning
//	DoctorCombinedCache   more than MaxStoreCaches store folders share one
//	                      combined cache file (CFG3): note (core only; the
//	                      old binary never printed it, so it runs in both)
//
// The old binary prints both rows from its own doctor host, so, like the
// untrusted_host row, they run only in the binary StandaloneOptionsOrg
// built (cmd/lore); otherwise the old binary would print each line twice.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// The checks of this file (the JSON "check" names).
const (
	DoctorCheckSharedMachine DoctorCheck = "shared_machine_id"
	DoctorCheckTeamVersions  DoctorCheck = "team_versions"
	DoctorCheckCombinedCache DoctorCheck = "combined_cache"
)

// DoctorCombinedCache notes the CFG3 fallback: past MaxStoreCaches
// attached stores, every store shares one cache file (search and lore sql
// still name each row's store). It is a note, not a warning: the
// fallback is designed, only slower to rebuild one store alone.
func (a *App) DoctorCombinedCache(ctx context.Context) ([]DoctorFinding, error) {
	if _, err := a.state(ctx); err != nil {
		return nil, nil // no store open: the other checks report it
	}
	if !a.Combined() {
		return nil, nil
	}
	return []DoctorFinding{{Check: DoctorCheckCombinedCache, Level: DoctorNote,
		Message: fmt.Sprintf("more than %d store folders: one combined cache for the attached stores (rebuilding one store rebuilds them all)", MaxStoreCaches)}}, nil
}

// doctorBinaryName is the command a below-floor writer runs to upgrade.
const doctorBinaryName = "lore"

// teamVersionsHeader opens the Team versions notes.
const teamVersionsHeader = "Team versions (newest save per writer and machine):"

// codeRepoMachine is the machine column of a save with no machine id (a
// code-repo save, Q-R3-9).
const codeRepoMachine = "(code repo, no machine id)"

// shortMachine is the display form of a machine id: mch_ plus 8 hex (the
// machine profile decision, PROV4).
func shortMachine(id string) string {
	if n := len("mch_") + machineShortHex; len(id) > n {
		return id[:n]
	}
	return id
}

// coreOwnsMachineRows reports the binary whose doctor host does not print
// the machine rows itself (cmd/lore; see the file comment).
func (a *App) coreOwnsMachineRows() bool {
	_, standalone := a.opts.OrgCommands.(*standaloneOrg)
	return standalone
}

// DoctorSharedMachines warns for each machine id whose profiles name two
// or more hosts: a cloned VM image that kept its OS machine id, or a host
// renamed since a store's copy of the profile was written (6.8). A failed
// machines read is the store checks' to report, so it adds nothing.
func (a *App) DoctorSharedMachines(ctx context.Context) ([]DoctorFinding, error) {
	if !a.coreOwnsMachineRows() {
		return nil, nil
	}
	shared, err := a.SharedMachineIDs(ctx)
	if err != nil {
		return nil, nil // reported by the store checks (see above)
	}
	return sharedMachineFindings(shared), nil
}

// sharedMachineFindings is one warning per shared id, in the given order.
func sharedMachineFindings(shared []opsinfra.SharedMachine) []DoctorFinding {
	out := make([]DoctorFinding, 0, len(shared))
	for _, s := range shared {
		mine := ""
		if s.This {
			mine = " (this machine)"
		}
		out = append(out, DoctorFinding{Check: DoctorCheckSharedMachine, Level: DoctorWarning, Plain: true,
			Message: fmt.Sprintf("machine %s%s is reported by hosts %s: two machines share an OS machine id; give the clone a new one (regenerate /etc/machine-id). If one host was only renamed, its next write refreshes the profile",
				shortMachine(s.ID), mine, strings.Join(s.Hosts, ", "))})
	}
	return out
}

// DoctorTeamVersions lists the newest lore version each writer saved with
// on each machine, over every attached cache (TeamVersions), as notes
// under one header; a writer below the repo's min_lore_version is also a
// warning (that lore is read-only here until it upgrades). No stamped save
// anywhere: no lines. A repo format file that cannot be read: no floor
// (the format gate reports it).
func (a *App) DoctorTeamVersions(ctx context.Context) ([]DoctorFinding, error) {
	if !a.coreOwnsMachineRows() {
		return nil, nil
	}
	tvs, err := a.TeamVersions(ctx)
	if err != nil || len(tvs) == 0 {
		return nil, err
	}
	floor := ""
	if top, terr := a.repoTop(ctx); terr == nil && top != "" {
		if d, ok, ferr := rowfs.ReadRepoFormat(top); ferr == nil && ok {
			floor = d.MinLoreVersion
		}
	}
	return teamVersionFindings(tvs, floor, a.sourceActorNamer(ctx), a.teamMachineNamer(ctx)), nil // actor names from each actor's source (actor_source.go)
}

// teamVersionFindings is the header note, one note per save and a warning
// after each save below floor ("" = no floor). name shows a writer's actor
// id as the person and machineName a machine id as the machine's label or
// host (doctor_teamnames.go); the save time shows as its date
// (CLI_REFERENCE 3.7).
func teamVersionFindings(tvs []sqlite.TeamVersion, floor string, name, machineName func(string) string) []DoctorFinding {
	out := []DoctorFinding{{Check: DoctorCheckTeamVersions, Level: DoctorNote, Plain: true, Message: teamVersionsHeader}}
	// A code-repo save carries no machine id, so a person who wrote to the
	// repo and to a store with one lore showed twice ("ana on (code repo,
	// no machine id)" and "ana on laptop", plainfiles:4): that line says
	// nothing the machine line does not. It stays when its version is one
	// no machine line of that writer has (another lore, another machine).
	onMachine := map[string]bool{}
	for _, tv := range tvs {
		if tv.Machine != "" {
			onMachine[tv.Actor+"\x00"+tv.Version] = true
		}
	}
	for _, tv := range tvs {
		if tv.Machine == "" && onMachine[tv.Actor+"\x00"+tv.Version] {
			continue
		}
		machine := codeRepoMachine
		if tv.Machine != "" {
			machine = machineName(tv.Machine)
		}
		out = append(out, DoctorFinding{Check: DoctorCheckTeamVersions, Level: DoctorNote, Plain: true,
			Message: fmt.Sprintf("  %s on %s: lore %s (last save %s)", name(tv.Actor), machine, tv.Version, saveDate(tv.LastSeen))})
		if floor != "" && formatgate.VersionBelow(tv.Version, floor) {
			out = append(out, DoctorFinding{Check: DoctorCheckTeamVersions, Level: DoctorWarning, Plain: true,
				Message: fmt.Sprintf("%s on %s last wrote with lore %s, below min_lore_version %s: that lore is read-only here until it runs `%s upgrade`",
					name(tv.Actor), machine, tv.Version, floor, doctorBinaryName)})
		}
	}
	return out
}
