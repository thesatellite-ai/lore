package setup

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// coverEnv is an Onboard with a discovering prober and an AccountCoverer
// over covers (login → fact) and the recorded "Suggest again" keys.
type coverEnv struct {
	fakeEnv
	dp     *discoverProber
	covers map[string]AccountCoverFact
	again  map[string]bool
}

func (e coverEnv) SetupProber(context.Context) (Prober, error) { return e.dp, nil }

func (e coverEnv) AccountCover(_ context.Context, login, _ string) (AccountCoverFact, bool) {
	f, ok := e.covers[strings.ToLower(login)]
	if ok && f.State == CoverPurged {
		f.Again = e.again[strings.ToLower(login)+"|"+f.PurgedAt]
	}
	return f, ok
}

func (e coverEnv) RecordSuggestAgain(_ context.Context, login, purgedAt string) error {
	e.again[strings.ToLower(login)+"|"+purgedAt] = true
	return nil
}

func newCoverEnv(covers map[string]AccountCoverFact) coverEnv {
	env := newFakeEnv()
	f, _ := twoAccounts()
	return coverEnv{fakeEnv: env, dp: &discoverProber{fakeProber: env.prober, facts: f}, covers: covers, again: map[string]bool{}}
}

const purgedAt = "2026-11-08T01:02:03Z"

// Issue 6 through the op: a card is marked purged_by + covered only when
// the strongest cover is a purge and no "Suggest again" was recorded for
// that purge; a live setup (used_by) leaves it alone; Suggest again
// unmarks it, and a newer purge marks it again.
func TestDiscoverMarksPurgedAccounts(t *testing.T) {
	line := "personal (deleted for good) used GitHub account octo-personal; …"
	cases := []struct {
		name  string
		cover map[string]AccountCoverFact
		used  bool
		want  string // the octo-personal card's purged_by
	}{
		{name: "no setup", cover: nil},
		{name: "live", cover: map[string]AccountCoverFact{"octo-personal": {State: "live", Setup: "personal"}}},
		{name: "trashed", cover: map[string]AccountCoverFact{"octo-personal": {State: "trashed", Setup: "personal"}}},
		{name: "purged", cover: map[string]AccountCoverFact{"octo-personal": {State: CoverPurged, Setup: "personal", PurgedAt: purgedAt, Line: line}}, want: "personal"},
		{name: "purged but a live setup uses it", cover: map[string]AccountCoverFact{"octo-personal": {State: CoverPurged, Setup: "personal", PurgedAt: purgedAt, Line: line}}, used: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newCoverEnv(c.cover)
			if c.used {
				env.dp.facts.SetupUsers = map[string]string{"octo-personal": "home"}
			}
			out, err := newEngine(env, fakeCatalog{}).Discover(bg, DiscoverIn{})
			if err != nil {
				t.Fatal(err)
			}
			card := out.Accounts[0]
			if card.Login != "octo-personal" || card.PurgedBy != c.want {
				t.Fatalf("card %s purged_by %q, want %q", card.Login, card.PurgedBy, c.want)
			}
			if (card.Covered != "") != (c.want != "") || (c.want != "" && card.Covered != line) {
				t.Fatalf("covered %q", card.Covered)
			}
			if out.Accounts[1].PurgedBy != "" {
				t.Fatalf("octo-work marked: %+v", out.Accounts[1])
			}
		})
	}
}

func TestSuggestAgain(t *testing.T) {
	env := newCoverEnv(map[string]AccountCoverFact{
		"octo-personal": {State: CoverPurged, Setup: "personal", PurgedAt: purgedAt, Line: "…"},
		"octo-work":     {State: "trashed", Setup: "acme"},
	})
	e := newEngine(env, fakeCatalog{})
	discover := func() string {
		out, err := e.Discover(bg, DiscoverIn{})
		if err != nil {
			t.Fatal(err)
		}
		return out.Accounts[0].PurgedBy
	}
	if got := discover(); got != "personal" {
		t.Fatalf("before: purged_by %q", got)
	}
	// Refusals: not a login; an account no purged setup covers (a trashed
	// one is restored, not suggested again; an unknown one has nothing).
	for _, bad := range []struct {
		login string
		code  errs.Code
	}{{"", errs.InvalidInput}, {"a b", errs.InvalidInput}, {"-x", errs.InvalidInput}, {"octo-work", errs.NotFound}, {"nobody", errs.NotFound}} {
		if _, err := e.SuggestAgain(bg, SuggestAgainIn{Login: bad.login}); !errs.Has(err, bad.code) {
			t.Fatalf("suggest-again %q: %v, want %s", bad.login, err, bad.code)
		}
	}
	// Twice: the same answer, one record.
	for range 2 {
		out, err := e.SuggestAgain(bg, SuggestAgainIn{Login: "Octo-Personal"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Purged != "personal" || out.PurgedAt != purgedAt || out.Login != "Octo-Personal" {
			t.Fatalf("out %+v", out)
		}
	}
	if len(env.again) != 1 {
		t.Fatalf("records %v", env.again)
	}
	if got := discover(); got != "" {
		t.Fatalf("after suggest again: purged_by %q", got)
	}
	// A newer purge of a setup for the same account hides it again.
	env.covers["octo-personal"] = AccountCoverFact{State: CoverPurged, Setup: "personal-2", PurgedAt: "2026-11-09T00:00:00Z", Line: "…"}
	if got := discover(); got != "personal-2" {
		t.Fatalf("after a newer purge: purged_by %q", got)
	}
}
