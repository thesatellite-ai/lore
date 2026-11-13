package setup

// register.go — the guided-setup ops (ONBOARDING_DESIGN 7.1) bound to an
// Onboard env, for op.NewRegistry. All are Local: they work on this
// machine's config.yaml, machine.db and probes, never on rows, so they run
// outside a repo and while a store is unreachable (the moment setup is
// needed most). Ops that may ask (fix, trash purge, repair --yes) declare
// Confirm.MayAsk and read the answer with op.Confirmed.

import (
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Error sets shared by the specs.
var (
	errsPort  = []errs.Code{errs.Unsupported, errs.ConfigInvalid, errs.Internal}
	errsNamed = append([]errs.Code{errs.InvalidInput, errs.SetupNotFound}, errsPort...)
)

// with is codes followed by the port errors.
func with(codes ...errs.Code) []errs.Code { return append(codes, errsNamed...) }

// GuidedOps returns setup.plan … setup.repair bound to env and the
// logic/onboard catalog. wire registers them next to Ops(env).
func GuidedOps(env Onboard) []op.Entry {
	return guidedOps(newEngine(env, onboardCatalog{}))
}

// guidedOps is GuidedOps over an engine (tests pass a fake catalog).
func guidedOps(e engine) []op.Entry {
	local := func(s op.Spec) op.Spec {
		s.Local, s.RESTNoun = true, restNoun
		if s.Permission == "" {
			s.Permission = op.PermRead
		}
		s.Access = op.AccessRead
		return s
	}
	return append([]op.Entry{
		op.Define(local(op.Spec{Name: OpPlan, Summary: "List the guided setup steps for a kind and the progress of a setup being made",
			Errors:   with(),
			Examples: []op.Example{{Title: "the steps of a solo setup", CLI: "lore setup plan --kind solo --json", Want: ""}}}),
			e.Plan),
		op.Define(local(op.Spec{Name: OpCheck, Summary: "Validate one step's answers and run its checks (writes nothing but the progress)",
			Errors:   with(),
			Examples: []op.Example{{Title: "check the identity step", CLI: `lore setup check --setup personal --step identity --answers '{"email":"mira@agency.example"}'`, Want: ""}}}),
			e.Check),
		op.Define(local(op.Spec{Name: OpApply, Summary: "Save one setup step: its one write, once every error-level check passes",
			Permission: op.PermWrite, Errors: with(errs.SetupStepFailed, errs.SetupStepOrder, errs.SetupNameTaken, errs.JoinOrgUnknown),
			Examples: []op.Example{{Title: "save the identity step", CLI: "lore setup apply --setup personal --step identity", Want: ""}}}),
			e.Apply),
		op.Define(local(op.Spec{Name: OpFix, Summary: "Run a failing check's fix command after a confirmation",
			Permission: op.PermWrite, Confirm: op.ConfirmRule{MayAsk: true},
			Errors:   with(errs.FixRefused, errs.NeedsConfirm),
			Examples: []op.Example{{Title: "run the fix of a failing check", CLI: "lore setup fix --setup personal --check tools.pointer_file --yes", Want: ""}}}),
			e.Fix),
		op.Define(local(op.Spec{Name: OpList, Summary: "List the setups on this machine and the one active here",
			Errors:   errsPort,
			Examples: []op.Example{{Title: "list setups", CLI: "lore setup list", Want: ""}}}),
			e.List),
		op.Define(local(op.Spec{Name: OpShow, Summary: "Show one setup's entry (keys and paths, never a secret)",
			Errors:   with(),
			Examples: []op.Example{{Title: "show a setup", CLI: "lore setup show --name personal", Want: ""}}}),
			e.Show),
		op.Define(local(op.Spec{Name: OpUse, Summary: "Pin this repo to a setup (--here), clear the pin, or print the shell line for one shell",
			Permission: op.PermWrite, Errors: with(),
			Examples: []op.Example{{Title: "pin this repo", CLI: "lore setup use --name acme-hub --here", Want: ""}}}),
			e.Use),
		op.Define(local(op.Spec{Name: OpEdit, Summary: "Change a setup's folders, GitHub account, key, personal store, tools or secrets backend",
			Permission: op.PermWrite, Errors: with(),
			Examples: []op.Example{{Title: "set the host alias", CLI: "lore setup edit --name acme-hub --host-alias github-acme", Want: ""}}}),
			e.Edit),
		op.Define(local(op.Spec{Name: OpRemove, Summary: "Move a setup to the trash (restorable; never its stores, repos or orgs)",
			Permission: op.PermWrite, Errors: with(),
			Examples: []op.Example{{Title: "remove a setup", CLI: "lore setup remove --name old-client", Want: ""}}}),
			e.Remove),
		op.Define(local(op.Spec{Name: OpJoin, Summary: "Join your org: by its name or store url when an admin added you (lore member add; matched by your email), or from an invite line; prints the lore setup apply lines that join, and on a terminal (or --web) opens the setup page on the join step",
			Errors:   with(errs.InviteInvalid, errs.JoinOrgUnknown),
			Examples: []op.Example{{Title: "join as a member", CLI: "lore setup join --invite agency --no-open --json", Want: ""}, {Title: "join from an invite", CLI: "lore setup join --invite lore-invite:v1:… --no-open --json", Want: ""}}}),
			e.Join),
		op.Define(local(op.Spec{Name: OpRepair, Summary: "Run a setup's checks fresh and list the steps to redo; --yes runs the safe fixes",
			Permission: op.PermWrite, Confirm: op.ConfirmRule{MayAsk: true},
			Errors:   with(errs.SetupAmbiguous, errs.SetupOrgMismatch),
			Examples: []op.Example{{Title: "list what to repair", CLI: "lore setup repair --name personal --dry-run", Want: ""}}}),
			e.Repair),
		op.Define(local(op.Spec{Name: OpAliases, Summary: "List the ssh host aliases of github.com, the GitHub user each logs in as, and gh's logins; --keys tests every key alone",
			Errors:   errsPort,
			Examples: []op.Example{{Title: "which alias logs in as whom", CLI: "lore setup aliases --json", Want: ""}}}),
			e.Aliases),
	}, append(lifecycleOps(e, local), leftoverOps(e, local)...)...)
}
