package setup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/repoinit"
	"github.com/thesatellite-ai/lore/ops/skill"
)

// def defines a throwaway op over In/Out: op.Define panics on a field type
// or tag the CLI/REST surfaces cannot build, so this pins every contract
// struct of ports.go as definable.
func def[In, Out any](t *testing.T, name string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: %v", name, r)
		}
	}()
	op.Define(op.Spec{Name: name, Summary: "contract test", Permission: op.PermRead, Access: op.AccessRead, Local: true},
		func(context.Context, In) (Out, error) {
			var o Out
			return o, nil
		})
}

// TestContractsDefinable: every In/Out of ONB 7.1 is a valid op shape.
func TestContractsDefinable(t *testing.T) {
	def[PlanIn, PlanOut](t, OpPlan)
	def[CheckIn, CheckOut](t, OpCheck)
	def[ApplyIn, ApplyOut](t, OpApply)
	def[FixIn, FixOut](t, OpFix)
	def[ListIn, ListOut](t, OpList)
	def[ShowIn, ShowOut](t, OpShow)
	def[UseIn, UseOut](t, OpUse)
	def[EditIn, EditOut](t, OpEdit)
	def[RemoveIn, RemoveOut](t, OpRemove)
	def[JoinIn, JoinOut](t, OpJoin)
	def[RepairIn, RepairOut](t, OpRepair)
	def[TeamInviteIn, TeamInviteOut](t, OpTeamInvite)
	def[TeamInviteListIn, TeamInviteListOut](t, OpTeamInviteList)
	def[TeamInviteRefIn, TeamInviteRevokeOut](t, OpTeamInviteRevoke)
	def[TeamInviteRefIn, TeamInviteResendOut](t, OpTeamInviteResend)
	def[RunIn, RunOut](t, OpRun)
}

// TestCheckResultJSON pins the ONB 2.2 check result shape byte for byte.
func TestCheckResultJSON(t *testing.T) {
	r := onboard.CheckResult{Check: onboard.CheckStoreWrite, Status: onboard.StatusFail, Level: onboard.LevelError,
		Target: "store acme-hub", Detail: "denied",
		Fix:    onboard.Fix{Text: "ask", Command: []string{"lore", "setup", "edit", "acme-hub"}, Runnable: true},
		TookMS: 812}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"check":"store.write","status":"fail","level":"error","target":"store acme-hub","detail":"denied","fix":{"text":"ask","command":["lore","setup","edit","acme-hub"],"runnable":true,"outward":false},"took_ms":812}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	ok, _ := json.Marshal(onboard.CheckResult{Check: onboard.CheckFoldersOverlap, Status: onboard.StatusOK, Level: onboard.LevelError, Target: "machine"})
	if string(ok) != `{"check":"folders.overlap","status":"ok","level":"error","target":"machine"}` {
		t.Errorf("ok result: %s", ok)
	}
}

// TestAnswersRoundTrip: answers keep their natural JSON form both as a
// JSON object and as the --answers flag text.
func TestAnswersRoundTrip(t *testing.T) {
	in := `{"default":false,"emails":["mira@agency.example"],"name":"Mira"}`
	var a onboard.Answers
	if err := a.UnmarshalText([]byte(in)); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != in {
		t.Errorf("got %s want %s", b, in)
	}
	var c CheckIn
	if err := json.Unmarshal([]byte(`{"setup":"agency","step":"identity","answers":`+in+`}`), &c); err != nil || c.Answers["name"].Text != "Mira" || !c.Answers["default"].IsBool {
		t.Errorf("CheckIn decode: %+v %v", c, err)
	}
}

// TestOpNamesMatch: the names repeated for PageOps are the owning
// packages' own constants.
func TestOpNamesMatch(t *testing.T) {
	for got, want := range map[string]string{OpSkillInstall: skill.OpInstall, OpSkillStatus: skill.OpStatus, OpRepoInit: repoinit.OpRepoInit} {
		if got != want {
			t.Errorf("%s != %s", got, want)
		}
	}
}

// TestTeamInviteOutText pins the ONB 1.6 block of `lore team invite`.
func TestTeamInviteOutText(t *testing.T) {
	o := TeamInviteOut{ID: "inv_1", Line: "lore-invite:v1:abc", ExpiresOn: "2026-11-20", Email: "tomas@agency.example", Org: "agency",
		GitHub: "tomas-dev", Repo: "agency-org/lore-team", GitHubAccess: "pending",
		GitHubFix: "gh api -X PUT repos/agency-org/lore-team/collaborators/tomas-dev -f permission=push"}
	want := "✓ invite inv_1 written to store agency (expires 2026-11-20)\n" +
		"⚠ tomas-dev has no access to agency-org/lore-team yet\n" +
		"  fix: gh api -X PUT repos/agency-org/lore-team/collaborators/tomas-dev -f permission=push\n" +
		"       (or add tomas-dev to a GitHub team that has write access)\n" +
		"Send this to tomas@agency.example (it holds no secret):\n  lore setup join lore-invite:v1:abc\n"
	if got := o.Text(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTargetRepo: a fix runs in the repo its check's target names.
func TestTargetRepo(t *testing.T) {
	for in, want := range map[string]string{
		"/w/acme-hub":             "/w/acme-hub",
		"claude_code /w/acme-hub": "/w/acme-hub",
		"store agency":            "",
		"store /w/x":              "",
		"machine":                 "",
		"":                        "",
	} {
		if got := TargetRepo(in); got != want {
			t.Errorf("TargetRepo(%q) = %q, want %q", in, got, want)
		}
	}
}
