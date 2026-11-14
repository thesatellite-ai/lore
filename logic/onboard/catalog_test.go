package onboard

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// The catalog is data the page, doctor and repair key on; these tests pin
// its internal consistency (ONB 2.2, 4.x, 6.1).

func TestCatalogStepOrderAndShape(t *testing.T) {
	cat := Catalog()
	ids := make([]StepID, len(cat))
	for i, s := range cat {
		ids[i] = s.ID
	}
	if !slices.Equal(ids, StepIDs()) {
		t.Fatalf("catalog order %v, want StepIDs %v", ids, StepIDs())
	}
	for _, s := range cat {
		if s.Title == "" || len(s.Kinds) == 0 {
			t.Errorf("step %s: title and kinds are required", s.ID)
		}
		for _, n := range s.Needs {
			if slices.Index(StepIDs(), n) >= slices.Index(StepIDs(), s.ID) {
				t.Errorf("step %s needs %s, which is not earlier in the catalog", s.ID, n)
			}
		}
		for _, q := range s.Questions {
			if !slices.Contains(QTypes(), q.Type) {
				t.Errorf("step %s question %s: type %q is not a QType", s.ID, q.ID, q.Type)
			}
			if q.Label == "" {
				t.Errorf("step %s question %s: no label", s.ID, q.ID)
			}
			for _, r := range q.Rules {
				if !slices.Contains(knownRuleKinds(), r.Kind()) {
					t.Errorf("step %s question %s: unknown rule %q", s.ID, q.ID, r)
				}
			}
			if (q.Type == QChoice || q.Type == QMulti) && len(q.Choices.Static) == 0 && q.Choices.Prefill == PrefillNone {
				t.Errorf("step %s question %s: a choice question needs choices", s.ID, q.ID)
			}
		}
		for _, c := range s.Checks {
			if _, ok := CheckByID(c); !ok {
				t.Errorf("step %s runs unknown check %s", s.ID, c)
			}
		}
	}
}

func TestPlanQuestionIDsUniquePerKind(t *testing.T) {
	for _, k := range Kinds() {
		for _, s := range PlanFor(k, true) {
			seen := map[string]bool{}
			for _, q := range s.Questions {
				if seen[q.ID] {
					t.Errorf("kind %s step %s: question id %s twice", k, s.ID, q.ID)
				}
				seen[q.ID] = true
			}
		}
	}
}

// infoChecks are the checks whose catalog level is LevelInfo: shown,
// never a warning or a failure. github.gh_matches: lore runs gh as the
// setup's own login, so gh's active account matters only for gh by hand.
var infoChecks = map[CheckID]bool{CheckGitHubGHMatches: true}

func TestEveryCheckHasRowAndFix(t *testing.T) {
	if len(checkMeta) != len(CheckIDs()) {
		t.Fatalf("checkMeta has %d rows, CheckIDs %d", len(checkMeta), len(CheckIDs()))
	}
	for _, id := range CheckIDs() {
		c, ok := CheckByID(id)
		if !ok {
			t.Fatalf("check %s has no catalog row", id)
		}
		if c.Title == "" || c.Probe == "" || (c.Level != LevelError && c.Level != LevelWarn && c.Level != LevelInfo) {
			t.Errorf("check %s: incomplete row %+v", id, c)
		}
		if (c.Level == LevelInfo) != infoChecks[id] {
			t.Errorf("check %s: level %s; only %v are informational", id, c.Level, infoChecks)
		}
		if c.Fix.Text == "" {
			t.Errorf("check %s: no fix text", id)
		}
		if c.Fix.Command == nil {
			t.Errorf("check %s: Command must be [] not nil", id)
		}
		if c.Fix.Outward && !c.Fix.Runnable {
			t.Errorf("check %s: outward implies runnable (else there is nothing to confirm)", id)
		}
		if c.Fix.Runnable && len(c.Fix.Command) == 0 {
			t.Errorf("check %s: runnable fix with no command", id)
		}
	}
	for k := range fixes {
		if _, ok := checkMeta[k.check]; !ok {
			t.Errorf("fix row for unknown check %s", k.check)
		}
	}
}

// TestEveryCheckMapsToOneStep: each check either runs in some step or is
// doctor-only (never both), and for every kind it applies to it has
// exactly one home step that is in the kind's plan (6.5 repair).
func TestEveryCheckMapsToOneStep(t *testing.T) {
	for _, id := range CheckIDs() {
		inStep := slices.ContainsFunc(Catalog(), func(s Step) bool { return slices.Contains(s.Checks, id) })
		if inStep == DoctorOnly(id) {
			t.Errorf("check %s: in a step=%v, doctor-only=%v; want exactly one", id, inStep, DoctorOnly(id))
		}
		for _, k := range CheckKinds(id) {
			home, ok := StepOfCheck(id, k)
			if !ok {
				t.Errorf("check %s has no home step for kind %s", id, k)
				continue
			}
			if !slices.ContainsFunc(PlanFor(k, true), func(s Step) bool { return s.ID == home }) {
				t.Errorf("check %s: home %s is not in kind %s's plan", id, home, k)
			}
		}
		for _, k := range Kinds() {
			if !slices.Contains(CheckKinds(id), k) {
				if _, ok := StepOfCheck(id, k); ok {
					t.Errorf("check %s maps to a step for kind %s it does not apply to", id, k)
				}
			}
		}
	}
	// Shared checks resolve to their first step.
	cases := []struct {
		id   CheckID
		kind Kind
		want StepID
	}{
		{CheckStoreRead, KindSolo, StepPersonalStore},
		{CheckStoreWrite, KindMember, StepPersonalStore},
		{CheckIdentityGitEmail, KindSolo, StepIdentity},
		{CheckSetupOrgEntry, KindMember, StepJoin},
		{CheckSetupOrgEntry, KindAdmin, StepTeam},
		{CheckLiveMode, KindAdmin, StepThisFolder},
	}
	for _, c := range cases {
		if got, _ := StepOfCheck(c.id, c.kind); got != c.want {
			t.Errorf("StepOfCheck(%s, %s) = %s, want %s", c.id, c.kind, got, c.want)
		}
	}
}

func TestPlanFor(t *testing.T) {
	stepIDs := func(ss []Step) []StepID {
		out := make([]StepID, len(ss))
		for i, s := range ss {
			out[i] = s.ID
		}
		return out
	}
	cases := []struct {
		kind   Kind
		inRepo bool
		want   []StepID
	}{
		{KindSolo, false, []StepID{StepKind, StepIdentity, StepGitHub, StepPersonalStore, StepTools, StepSecrets, StepFolders, StepSummary}},
		{KindMember, true, []StepID{StepKind, StepIdentity, StepGitHub, StepPersonalStore, StepJoin, StepTools, StepSecrets, StepFolders, StepThisFolder, StepSummary}},
		{KindAdmin, false, []StepID{StepKind, StepIdentity, StepGitHub, StepPersonalStore, StepTeam, StepTools, StepSecrets, StepFolders, StepSummary}},
		{Kind("boss"), true, nil},
	}
	for _, c := range cases {
		if got := stepIDs(PlanFor(c.kind, c.inRepo)); !slices.Equal(got, c.want) {
			t.Errorf("PlanFor(%s, %v) = %v, want %v", c.kind, c.inRepo, got, c.want)
		}
	}
	// Kind-limited questions and checks are narrowed.
	solo := PlanFor(KindSolo, false)
	if !slices.ContainsFunc(solo[0].Questions, func(q Question) bool { return q.ID == QIDDefault }) {
		t.Error("solo kind step lost its default question")
	}
	member := PlanFor(KindMember, false)
	if slices.ContainsFunc(member[0].Questions, func(q Question) bool { return q.ID == QIDDefault }) {
		t.Error("member kind step must not ask default (solo only, 4.1)")
	}
	if slices.Contains(solo[1].Checks, CheckIdentityInviteEmail) || !slices.Contains(member[1].Checks, CheckIdentityInviteEmail) {
		t.Error("identity.invite_email must run for member only")
	}
	if q := member[1].Questions[1]; q.ID != QIDEmail || q.Prefill != PrefillInviteEmail {
		t.Errorf("member email question = %+v, want the invite prefill", q)
	}
	// The plan marshals (the page reads it as JSON).
	if _, err := json.Marshal(PlanFor(KindAdmin, true)); err != nil {
		t.Fatal(err)
	}
}

func TestLevelFor(t *testing.T) {
	cases := []struct {
		id   CheckID
		c    LevelCtx
		want Level
	}{
		{CheckStorePrivate, LevelCtx{Kind: KindAdmin, Store: StoreRoleOrg}, LevelError},
		{CheckStorePrivate, LevelCtx{Kind: KindSolo, Store: StoreRoleMe}, LevelWarn},
		{CheckStorePrivate, LevelCtx{Store: StoreRoleAttached}, LevelWarn},
		{CheckStoreWrite, LevelCtx{Store: StoreRoleMe}, LevelError},
		{CheckStoreWrite, LevelCtx{Store: StoreRoleOrg}, LevelError},
		{CheckStoreWrite, LevelCtx{Store: StoreRoleAttached}, LevelError},
		{CheckStoreWrite, LevelCtx{Store: StoreRoleAttachedReadOnly}, LevelWarn},
		{CheckIdentityGitEmail, LevelCtx{}, LevelWarn},
		{CheckFoldersOverlap, LevelCtx{}, LevelError},
	}
	for _, c := range cases {
		if got := LevelFor(c.id, c.c); got != c.want {
			t.Errorf("LevelFor(%s, %+v) = %s, want %s", c.id, c.c, got, c.want)
		}
	}
	ck, _ := CheckFor(CheckStorePrivate, StepTeam, LevelCtx{Store: StoreRoleOrg}, FixDefault)
	if ck.Level != LevelError || !strings.Contains(ck.Fix.Text, "team's notes") {
		t.Errorf("CheckFor team store.private = %+v", ck)
	}
}

func TestFixRunnableOutward(t *testing.T) {
	cases := []struct {
		id                 CheckID
		step               StepID
		v                  FixVariant
		runnable, outward  bool
		firstArg, wantText string
	}{
		{CheckStoreRead, StepPersonalStore, FixDefault, true, true, "gh", "gh repo create {owner}/{repo} --private"},
		{CheckStoreRead, StepPersonalStore, FixUnreachable, false, false, "", "network or VPN"},
		{CheckStoreRead, StepJoin, FixDefault, false, false, "", "github.com/notifications"},
		{CheckIdentityGitEmail, StepIdentity, FixDefault, false, false, "git", "git config --global user.email {email}"},
		{CheckIdentityGitEmail, StepThisFolder, FixRepo, false, false, "git", "git -C {repo_root}"},
		{CheckToolsSkillInstalled, StepTools, FixDefault, true, false, "lore", "lore skill install --tool {tool}"},
		{CheckToolsSkillVersion, StepTools, FixNoVersionLine, true, false, "lore", "no version line"},
		{CheckToolsHooks, StepTools, FixDefault, true, false, "lore", "lore setup --agent-hooks --yes"},
		{CheckToolsPointerFile, StepTools, FixDefault, true, false, "lore", "adds {tool} to the repo's context.targets"},
		{CheckToolsPointerFile, StepTools, FixGenerated, false, false, "", "the source that generates the file"}, // devco J-17
		{CheckRepoInit, StepThisFolder, FixDefault, true, false, "lore", "this sets it up"},
		{CheckRepoInit, StepThisFolder, FixInitTargets, true, false, "lore", "the files {targets} read"},
		{CheckInviteGitHubAccess, StepTeam, FixDefault, true, true, "gh", "permission=push"},
		{CheckGitHubGHMatches, StepGitHub, FixDefault, false, false, "gh", "gh auth switch --user {user}"},
	}
	for _, c := range cases {
		f := FixFor(c.id, c.step, c.v)
		first := ""
		if len(f.Command) > 0 {
			first = f.Command[0]
		}
		if f.Runnable != c.runnable || f.Outward != c.outward || first != c.firstArg || !strings.Contains(f.Text, c.wantText) {
			t.Errorf("FixFor(%s, %s, %q) = %+v", c.id, c.step, c.v, f)
		}
	}
	if got := FixFor(CheckStoreRead, StepJoin, FixUnreachable); !strings.Contains(got.Text, "network") {
		t.Errorf("join unreachable = %q", got.Text)
	}
}

func TestMergeStatus(t *testing.T) {
	r := func(id CheckID, l Level, s Status) CheckResult { return CheckResult{Check: id, Level: l, Status: s} }
	cases := []struct {
		name string
		in   []CheckResult
		want StepStatus
	}{
		{"empty", nil, StepReady},
		{"all ok", []CheckResult{r(CheckStoreRead, LevelError, StatusOK)}, StepReady},
		{"warn fail", []CheckResult{r(CheckStoreMarker, LevelWarn, StatusFail)}, StepReady},
		{"warn status", []CheckResult{r(CheckStoreMarker, LevelWarn, StatusWarn)}, StepReady},
		{"probe error at error level", []CheckResult{r(CheckStoreRead, LevelError, StatusError)}, StepReady},
		{"skip", []CheckResult{r(CheckStoreWrite, LevelError, StatusSkip)}, StepReady},
		{"error fail", []CheckResult{r(CheckStoreRead, LevelError, StatusOK), r(CheckStoreWrite, LevelError, StatusFail)}, StepFailed},
	}
	for _, c := range cases {
		if got := MergeStatus(c.in); got != c.want {
			t.Errorf("%s: MergeStatus = %s, want %s", c.name, got, c.want)
		}
	}
	got := FailingErrors([]CheckResult{r(CheckStoreWrite, LevelError, StatusFail), r(CheckStoreWrite, LevelError, StatusFail), r(CheckStoreRead, LevelError, StatusFail)})
	if !slices.Equal(got, []CheckID{CheckStoreWrite, CheckStoreRead}) {
		t.Errorf("FailingErrors = %v", got)
	}
}

func TestNeedsDone(t *testing.T) {
	join, _ := StepByID(StepJoin)
	p := func(s StepID, st StepStatus) Progress { return Progress{Setup: "acme-hub", Step: s, Status: st} }
	err := NeedsDone(join, KindMember, []Progress{p(StepIdentity, StepDone), p(StepGitHub, StepFailed)})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.SetupStepOrder || !strings.Contains(e.Message, "needs github first") {
		t.Fatalf("NeedsDone = %v, want E_SETUP_STEP_ORDER naming github", err)
	}
	if err := NeedsDone(join, KindMember, []Progress{p(StepIdentity, StepDone), p(StepGitHub, StepDone)}); err != nil {
		t.Fatalf("all needs done: %v", err)
	}
	tools, _ := StepByID(StepTools)
	if err := NeedsDone(tools, KindSolo, []Progress{p(StepKind, StepSkipped)}); err != nil {
		t.Fatalf("skipped counts as done: %v", err)
	}
	if miss := MissingNeeds(tools, KindSolo, nil); !slices.Equal(miss, []StepID{StepKind}) {
		t.Errorf("MissingNeeds no progress = %v", miss)
	}
}

// TestEmailsQuestionSaysWhereTheMainEmailIs pins realmac #17: the saved
// emails list leaves out the main Email by design (it is the identity
// key), so the identity step's "Other addresses" help says where it is.
func TestEmailsQuestionSaysWhereTheMainEmailIs(t *testing.T) {
	for _, k := range []Kind{KindSolo, KindMember} {
		found := false
		for _, st := range PlanFor(k, false) {
			for _, q := range st.Questions {
				if q.ID != QIDEmails {
					continue
				}
				found = true
				if q.Help != EmailsHelp || !strings.Contains(q.Help, "identity key") || !strings.Contains(q.Help, "not repeated") {
					t.Errorf("%s: emails help = %q, want it to say the main Email is the identity key", k, q.Help)
				}
			}
		}
		if !found {
			t.Errorf("%s: no emails question in the plan", k)
		}
	}
}
