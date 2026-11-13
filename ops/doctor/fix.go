package doctor

// fix.go — doctor.fix: run the fix of one finding of this repo's report
// (findings.go), the page's Run it beside a finding such as a stale
// rendered copy (`lore render`) or unpushed store work (`lore sync`).
//
//	doctor.fix <key> ─▶ the repo report again (no setups, no probes)
//	                 ─▶ the finding whose Target is "repo <key>"; refused when
//	                    it is gone or its fix is not one plain lore command
//	                 ─▶ not confirmed: E_NEEDS_CONFIRM with the exact command
//	                 ─▶ setup.FixRunner in the repo's top folder (lore only;
//	                    no shell) ─▶ the report again: still there or not
//
// The caller names a finding, never a command: the argv always comes from
// the report this binary just made, so a page script cannot run anything
// the report would not offer.

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/setup"
)

// OpFix is the operation name (`lore doctor fix <key>`).
const OpFix = "doctor.fix"

// FixEnv is the optional upgrade of Env that doctor.fix needs: the fix
// runner of the setup engine (allowlisted programs, no shell) and the
// repo the fix runs in.
type FixEnv interface {
	SetupFixRunner(ctx context.Context) (setup.FixRunner, error)
	RepoTop(ctx context.Context) (string, error)
}

// FixIn is the input of doctor.fix.
type FixIn struct {
	Finding string `json:"finding" arg:"0" desc:"the finding's key: its target in lore doctor --json findings[], without the leading \"repo \""`
}

// FixOut is the output of doctor.fix.
type FixOut struct {
	// Ran is the command that ran.
	Ran []string `json:"ran"`
	// Exit is its exit status.
	Exit int `json:"exit"`
	// OutputTail is the last lines of its output, secrets masked.
	OutputTail string `json:"output_tail"`
	// Fixed is true when the finding is gone from the report after the run.
	Fixed bool `json:"fixed"`
	// StillFailing names the finding when the command exited 0 but the
	// finding is still there.
	StillFailing string `json:"still_failing,omitempty"`
	// Findings are the repo report's findings after the run.
	Findings []onboard.CheckResult `json:"findings"`
}

// fixOp is the registry entry of doctor.fix.
func fixOp(env Env) op.Entry {
	return op.Define(op.Spec{
		Name: OpFix, Summary: "Run the fix of one doctor finding of this repo (a plain lore command such as lore render), after a confirmation",
		Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
		Confirm:  op.ConfirmRule{MayAsk: true},
		Errors:   []errs.Code{errs.Internal, errs.Unsupported, errs.NeedsConfirm, errs.FixRefused, errs.NotFound},
		Examples: []op.Example{{Title: "refresh a stale rendered copy doctor found", CLI: "lore doctor fix --finding 0123456789abcdef --yes", Want: ""}},
	}, func(ctx context.Context, in FixIn) (FixOut, error) {
		return Fix(ctx, env, in)
	})
}

// repoFindings is the repo part of the report: base and core checks, no
// setups (nothing is probed), finalized into findings.
func repoFindings(ctx context.Context, env Env) ([]onboard.CheckResult, error) {
	r, err := runRepo(ctx, env, false)
	if err != nil {
		return nil, err
	}
	r.finalize()
	return r.Findings, nil
}

// Fix is doctor.fix over env.
func Fix(ctx context.Context, env Env, in FixIn) (FixOut, error) {
	fe, ok := env.(FixEnv)
	if !ok {
		return FixOut{}, errs.New(errs.Unsupported, "lore doctor fix needs the setup engine, which this binary does not have")
	}
	key := strings.TrimPrefix(strings.TrimSpace(in.Finding), TargetFinding)
	before, err := repoFindings(ctx, env)
	if err != nil {
		return FixOut{}, err
	}
	i := slices.IndexFunc(before, func(f onboard.CheckResult) bool { return f.Target == TargetFinding+key })
	if key == "" || i < 0 {
		return FixOut{}, errs.New(errs.NotFound, "no doctor finding "+strconv.Quote(key)+" in this repo now").
			WithHint("run lore doctor --json and use a key from findings[]; a finding that is gone needs no fix")
	}
	f := before[i]
	if !f.Fix.Runnable || len(f.Fix.Command) == 0 || f.Fix.Command[0] != "lore" {
		return FixOut{}, errs.New(errs.FixRefused, "the finding has no command lore can run: "+f.Detail).
			WithHint(f.Fix.Text)
	}
	if !op.Confirmed(ctx) {
		q := "Run " + strings.Join(f.Fix.Command, " ") + "?"
		return FixOut{}, errs.New(errs.NeedsConfirm, f.Detail+"\n"+q).WithQuestion(q).
			WithHint("rerun with --yes to run it").WithNext("lore", "doctor", "fix", key, "--yes")
	}
	runner, err := fe.SetupFixRunner(ctx)
	if err != nil {
		return FixOut{}, err
	}
	top, _ := fe.RepoTop(ctx) // "" outside a repo: the fix runs in this folder
	run, err := runner.RunFixIn(ctx, top, f.Fix.Command)
	if err != nil {
		return FixOut{}, err
	}
	out := FixOut{Ran: run.Argv, Exit: run.Exit, OutputTail: run.OutputTail}
	after, aerr := repoFindings(ctx, env)
	if aerr != nil {
		return out, nil // the fix ran; a report that cannot be made again is not its error
	}
	out.Findings = after
	out.Fixed = !slices.ContainsFunc(after, func(x onboard.CheckResult) bool { return x.Target == f.Target })
	if !out.Fixed && run.Exit == 0 {
		out.StillFailing = "ran OK but the finding is still there: " + f.Detail
	}
	return out, nil
}
