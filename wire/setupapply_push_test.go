package wire

// setupapply_push_test.go — the setup page's push mode question
// (setupapply_push.go): the personal store step writes config.yaml
// personal.push and `lore store show --store me` reads it back; now writes
// nothing; a store with no remote is refused before anything is written;
// the team step writes push into the new team store's lore-store.yaml once
// the admin setups[] entry exists.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// bareAt makes an empty bare repo at <hosts>/<repo>.git.
func (w *orgWorld) bareAt(t *testing.T, repo string) {
	t.Helper()
	d := filepath.Join(w.hosts, repo+".git")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	orgGit(t, d, "init", "-q", "--bare", "-b", "main")
}

func TestSetupApplyPersonalPush(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "mira/lore-me")
	home := w.home(t, "mira")
	app, err := New(StandaloneOptions(w.repo, home, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setup := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true}
	apply := func(ans onboard.Answers) (opssetup.ApplyResult, error) {
		t.Helper()
		res, aerr := ap.Apply(ctx, opssetup.ApplyRequest{Setup: setup, Step: onboard.StepPersonalStore, Apply: onboard.ApplyPersonalStore, Answers: ans})
		if aerr == nil {
			setup = res.Setup
		}
		return res, aerr
	}
	cfgText := func() string {
		b, _ := os.ReadFile(filepath.Join(home, ConfigFile)) // missing reads as ""
		return string(b)
	}

	// No remote to push to: refused before config.yaml is touched.
	for _, c := range []struct{ choice, push string }{{onboard.PersonalStoreLocal, "batch"}, {onboard.PersonalStoreNone, "manual"}} {
		before := cfgText()
		_, aerr := apply(onboard.Answers{onboard.QIDPersonalStore: {Text: c.choice}, onboard.QIDPush: {Text: c.push}})
		if !errs.Has(aerr, errs.InvalidInput) || !strings.Contains(aerr.Error(), "needs a git repo") {
			t.Fatalf("%s + push %s = %v, want E_INVALID_INPUT naming the missing remote", c.choice, c.push, aerr)
		}
		if cfgText() != before {
			t.Fatalf("%s + push %s wrote config.yaml:\n%s", c.choice, c.push, cfgText())
		}
	}
	if _, aerr := apply(onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreLocal}, onboard.QIDPush: {Text: "hourly"}}); !errs.Has(aerr, errs.InvalidInput) || !strings.Contains(aerr.Error(), `"hourly"`) {
		t.Fatalf("push hourly = %v, want E_INVALID_INPUT naming the value", aerr)
	}

	url := "https://github.com/mira/lore-me"
	batch := onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreExisting}, onboard.QIDStoreURL: {Text: url}, onboard.QIDPush: {Text: "batch"}}
	res, err := apply(batch)
	if err != nil {
		t.Fatalf("existing + push batch: %v", err)
	}
	if !res.Changed || !strings.Contains(strings.Join(res.Wrote, " "), configPath(home)) {
		t.Fatalf("existing + push batch result = %+v, want config.yaml written", res)
	}
	raw, err := personalPushRaw(home)
	if err != nil || raw.Push != string(storepush.ModeBatch) || raw.PushEvery != "" {
		t.Fatalf("personal push = %+v, %v; want push batch and the default interval (no push_every)", raw, err)
	}
	v, err := app.StorePush(ctx, "me")
	if err != nil || v.Push != string(storepush.ModeBatch) || v.PushEvery != "5m" {
		t.Fatalf("store show me = %+v, %v; want push batch every 5m", v, err)
	}
	if res, err = apply(batch); err != nil || res.Changed {
		t.Fatalf("second identical save = %+v, %v; want unchanged", res, err)
	}
	// now (or no answer) writes nothing, also over an earlier batch.
	before := cfgText()
	for _, p := range []string{"now", ""} {
		ans := onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreExisting}, onboard.QIDStoreURL: {Text: url}, onboard.QIDPush: {Text: p}}
		if res, err = apply(ans); err != nil || res.Changed {
			t.Fatalf("push %q = %+v, %v; want nothing written", p, res, err)
		}
	}
	if cfgText() != before {
		t.Fatalf("push now rewrote config.yaml:\n%s", cfgText())
	}
}

func TestSetupApplyTeamPush(t *testing.T) {
	ctx := context.Background()
	w := newOrgWorld(t)
	w.bareAt(t, "agency/team")
	home := w.home(t, "ana")
	opts, _ := StandaloneOptionsOrg(w.repo, home, w.repo, "test")
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	ap, err := app.SetupApplier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/agency/team"
	ans := onboard.Answers{onboard.QIDOrg: {Text: "northwind"}, onboard.QIDStoreURL: {Text: url}, onboard.QIDPush: {Text: "bogus"},
		onboard.QIDEmail: {Text: "ana@agency.example"}, onboard.QIDName: {Text: "Ana"}}
	req := opssetup.ApplyRequest{Setup: onboard.SetupEntry{Name: "northwind", Kind: onboard.KindAdmin}, Step: onboard.StepTeam, Apply: onboard.ApplyTeam, Answers: ans}
	if _, err = ap.Apply(ctx, req); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("team push bogus = %v, want E_INVALID_INPUT", err)
	}
	if files := orgGit(t, filepath.Join(w.hosts, "agency/team.git"), "for-each-ref"); files != "" {
		t.Fatalf("a refused push answer still wrote the team store: %s", files)
	}
	ans[onboard.QIDPush] = onboard.Answer{Text: "batch"}
	res, err := ap.Apply(ctx, req)
	if err != nil {
		t.Fatalf("team + push batch: %v", err)
	}
	if !res.Changed || !strings.Contains(strings.Join(res.Wrote, " "), url) {
		t.Fatalf("team result = %+v, want the store written", res)
	}
	if !isOrgAdmin(home, "northwind") {
		t.Fatal("no admin setups[] entry for northwind after the team step")
	}
	marker := orgGit(t, filepath.Join(w.hosts, "agency/team.git"), "show", "main:lore-store.yaml")
	if !strings.Contains(marker, "push: batch") {
		t.Fatalf("team store lore-store.yaml has no push: batch:\n%s", marker)
	}
	subjects := orgGit(t, filepath.Join(w.hosts, "agency/team.git"), "log", "--format=%s", "main")
	if !strings.Contains(subjects, storeSetSubject+"northwind") {
		t.Fatalf("no %q commit on the team store:\n%s", storeSetSubject+"northwind", subjects)
	}
	// Saving the step again writes no second settings commit.
	if _, err = ap.Apply(ctx, req); err != nil {
		t.Fatalf("team step saved again: %v", err)
	}
	again := orgGit(t, filepath.Join(w.hosts, "agency/team.git"), "log", "--format=%s", "main")
	if strings.Count(again, storeSetSubject) != 1 {
		t.Fatalf("saving again added a settings commit:\n%s", again)
	}
}
