package setupweb

// The page allowlist (ONB 5.2, 7.1 "Page allowlist" column): the only
// operations the setup page can call. Every other operation of the
// registry, /openapi.json, /v1/spec and the dynamic-kind routes answer 404
// on the page's server, so a script that steals nothing but reaches the
// port still cannot run `lore sql` or delete rows.
//
// The names are written out here, not imported from ops/*, so the list is
// one reviewable constant block. TestAllowlistMatchesSetupPageOps pins it
// to ops/setup.PageOps plus the three existing read ops, so widening the
// page is always a change to this file and its test; TestPageOpsExist
// (cmd/lore) checks every name is a real operation of the binary.
const (
	OpSetupPlan        = "setup.plan"
	OpSetupCheck       = "setup.check"
	OpSetupApply       = "setup.apply"
	OpSetupFix         = "setup.fix"
	OpSetupList        = "setup.list"
	OpSetupShow        = "setup.show"
	OpSetupUse         = "setup.use"
	OpSetupEdit        = "setup.edit"
	OpSetupRemove      = "setup.remove"
	OpSetupJoin        = "setup.join"
	OpSetupRepair      = "setup.repair"
	OpTeamInvite       = "team.invite"
	OpTeamInviteList   = "team.invite.list"
	OpTeamInviteRevoke = "team.invite.revoke"
	OpTeamInviteResend = "team.invite.resend"
	OpSkillInstall     = "skill.install"
	OpSkillStatus      = "skill.status"
	OpRepoInit         = "repo.init"
	OpSetupAliases     = "setup.aliases"
	OpSetupDiscover    = "setup.discover"
	// OpSetupSuggestAgain brings back Home's suggestion for an account
	// whose setup was purged (ops/setup/discover_again.go).
	OpSetupSuggestAgain = "setup.suggest_again"
	OpSetupImportScan   = "setup.import_scan"
	OpSetupImport       = "setup.import"
	OpSetupFolders      = "setup.folders"
	OpSetupBrowse       = "setup.browse"
	// The setup lifecycle (ops/setup/lifecycle.go): disable/enable, the
	// setup trash, and dismissing Home's import banner.
	OpSetupDisable       = "setup.disable"
	OpSetupEnable        = "setup.enable"
	OpSetupTrashList     = "setup.trash.list"
	OpSetupTrashRestore  = "setup.trash.restore"
	OpSetupTrashPurge    = "setup.trash.purge"
	OpSetupImportDismiss = "setup.import_dismiss"
	OpDoctorRun          = "doctor.run"
	// OpDoctorLast is what Doctor shows on open: each setup's last
	// results and when they were taken, read from disk, no check run.
	OpDoctorLast = "doctor.last"
	// OpDoctorFix runs one repo finding's fix (Doctor's Run it): the
	// caller names a finding key, the command comes from the report the
	// server makes again, and only a plain lore command runs.
	OpDoctorFix    = "doctor.fix"
	OpWhoamiShow   = "whoami.show"
	OpIdentityShow = "identity.show"
	// OpSetupLeftoversClear clears a config.yaml identity or personal.store
	// no setup uses (Home's leftovers box); only those two keys.
	OpSetupLeftoversClear = "setup.leftovers.clear"
	// OpMachineReveal / OpMachinePathList: open ~/.lore, config.yaml or the
	// repo's .lore in the file manager (a closed target set, never a path).
	OpMachineReveal   = "machine.reveal"
	OpMachinePathList = "machine.path.list"
	// OpConfigFmt is doctor's config_style fix, `lore config fmt`: Home's
	// Run it (setup.list config_style). It rewrites only the layout of
	// config.yaml (block style, comments and key order kept) and refuses
	// a result the schema reads differently (OWN7-6).
	OpConfigFmt = "config.fmt"
)

// allowlist is the page's operations, in the order of ONB 7.1.
var allowlist = [...]string{
	OpSetupPlan, OpSetupCheck, OpSetupApply, OpSetupFix, OpSetupList, OpSetupShow,
	OpSetupUse, OpSetupEdit, OpSetupRemove, OpSetupJoin, OpSetupRepair,
	OpTeamInvite, OpTeamInviteList, OpTeamInviteRevoke, OpTeamInviteResend,
	OpSkillInstall, OpSkillStatus, OpRepoInit, OpSetupAliases, OpSetupDiscover, OpSetupSuggestAgain, OpSetupImportScan, OpSetupImport, OpSetupFolders, OpSetupBrowse,
	OpSetupDisable, OpSetupEnable, OpSetupTrashList, OpSetupTrashRestore, OpSetupTrashPurge, OpSetupImportDismiss,
	OpDoctorRun, OpDoctorLast, OpDoctorFix, OpWhoamiShow, OpIdentityShow, OpSetupLeftoversClear,
	OpMachineReveal, OpMachinePathList, OpConfigFmt,
}

// AllowedOps returns a copy of the page allowlist.
func AllowedOps() []string { return append([]string(nil), allowlist[:]...) }

// Allowed reports whether the page may call the operation name. It is the
// rest.Options.Allow of the page's server.
func Allowed(name string) bool {
	for _, n := range allowlist {
		if n == name {
			return true
		}
	}
	return false
}
