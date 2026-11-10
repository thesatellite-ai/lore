package setup

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// listerProber is a fakeProber with the GitHubLister upgrade: it answers
// table and records each request.
type listerProber struct {
	*fakeProber
	table AliasesOut
	err   error
	ins   []AliasesIn
}

func (p *listerProber) GitHubAliases(_ context.Context, in AliasesIn) (AliasesOut, error) {
	p.ins = append(p.ins, in)
	return p.table, p.err
}

// listerEnv is fakeEnv whose prober is a listerProber.
type listerEnv struct {
	fakeEnv
	lp *listerProber
}

func (e listerEnv) SetupProber(context.Context) (Prober, error) { return e.lp, nil }

// aliasTable: octo-p and github.com log in as octo-personal, github-work
// as octo-work, github-slow did not answer.
var aliasTable = AliasesOut{Aliases: []GitHubAlias{
	{Alias: "octo-p", IdentityFile: "~/.ssh/octo-p", PubKey: "~/.ssh/octo-p.pub", LogsInAs: "octo-personal", Status: "ok"},
	{Alias: "github-work", IdentityFile: "~/.ssh/work", PubKey: "~/.ssh/work.pub", LogsInAs: "octo-work", Status: "ok"},
	{Alias: "github-slow", IdentityFile: "~/.ssh/slow", NoPub: true, Status: "timeout"},
	{Alias: "github.com", IdentityFile: "~/.ssh/work_key", NoPub: true, LogsInAs: "octo-personal", Status: "ok"},
}, GH: []GHLogin{{User: "octo-personal", Active: true}}}

func newListerEngine(table AliasesOut, err error) (engine, *listerProber) {
	env := newFakeEnv()
	lp := &listerProber{fakeProber: env.prober, table: table, err: err}
	e := newEngine(listerEnv{fakeEnv: env, lp: lp}, fakeCatalog{})
	return e, lp
}

func TestAliasesOp(t *testing.T) {
	// No lister: E_UNSUPPORTED with a hint, never an empty table.
	e := newTestEngine(newFakeEnv())
	if _, err := e.Aliases(bg, AliasesIn{}); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("no lister: %v", err)
	}
	// With one: the table as the lister answers it; Fresh and Keys pass through.
	e, lp := newListerEngine(aliasTable, nil)
	out, err := e.Aliases(bg, AliasesIn{Fresh: true, Keys: true})
	if err != nil || len(out.Aliases) != len(aliasTable.Aliases) || len(lp.ins) != 1 || !lp.ins[0].Fresh || !lp.ins[0].Keys {
		t.Fatalf("out=%+v err=%v ins=%+v", out, err, lp.ins)
	}
	// A lister error is the op's error.
	e, _ = newListerEngine(AliasesOut{}, errs.New(errs.BadPath, "no ssh config"))
	if _, err := e.Aliases(bg, AliasesIn{}); !errs.Has(err, errs.BadPath) {
		t.Fatalf("lister error: %v", err)
	}
}

func githubStep() onboard.Step {
	return onboard.Step{ID: onboard.StepGitHub, Questions: []onboard.Question{
		{ID: onboard.QIDGitHubAccount, Required: true}, {ID: onboard.QIDHostAlias}, {ID: onboard.QIDSSHKey}, {ID: onboard.QIDRoute}}}
}

func TestValidateStepAliasMessages(t *testing.T) {
	e, _ := newListerEngine(aliasTable, nil)
	cases := []struct {
		name    string
		step    onboard.Step
		answers onboard.Answers
		rule    string // the host_alias error rule; "" = none
		has     string
	}{
		{"alias left empty is no error (derived)", githubStep(), onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal")}, "", ""},
		{"alias logs in as someone else", githubStep(), onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDHostAlias: text("github-work")},
			ruleAliasUser, "github-work logs in as octo-work, not octo-personal"},
		{"case-insensitive match passes", githubStep(), onboard.Answers{onboard.QIDGitHubAccount: text("Octo-Personal"), onboard.QIDHostAlias: text("octo-p")}, "", ""},
		{"silent alias is not judged", githubStep(), onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDHostAlias: text("github-slow")}, "", ""},
		{"other steps unchanged", onboard.Step{ID: onboard.StepIdentity}, onboard.Answers{}, "", ""},
	}
	for _, c := range cases {
		fe := e.validateStep(bg, c.step, onboard.KindSolo, c.answers)
		var got *onboard.FieldError
		for i := range fe {
			if fe[i].Question == onboard.QIDHostAlias {
				got = &fe[i]
			}
		}
		switch {
		case c.rule == "" && got != nil:
			t.Errorf("%s: unexpected %+v", c.name, *got)
		case c.rule != "" && (got == nil || got.Rule != c.rule || !strings.Contains(got.Message, c.has)):
			t.Errorf("%s: got %+v, want rule %s with %q", c.name, got, c.rule, c.has)
		}
	}
}

// deriveRoute: the person picks only the account; the route follows.
// Table over the shapes of a real ~/.ssh: the default host logs in through
// a key with no .pub (no ssh_key recorded, never a refused value), a named
// alias with a .pub, a forced alias or key kept, https via gh when no ssh
// route logs in, and the fix when nothing does.
func TestDeriveRoute(t *testing.T) {
	type want struct {
		alias, key string
		route      onboard.GitRoute
		errHas     string
	}
	acct := func(u string) onboard.Answers { return onboard.Answers{onboard.QIDGitHubAccount: text(u)} }
	noNamed := AliasesOut{Aliases: aliasTable.Aliases[2:], GH: aliasTable.GH}
	withGH := AliasesOut{Aliases: aliasTable.Aliases, GH: []GHLogin{{User: "octo-personal"}, {User: "octo-third"}}}
	cases := []struct {
		name  string
		table AliasesOut
		in    onboard.Answers
		want  want
	}{
		{"named alias with .pub", aliasTable, acct("octo-personal"), want{alias: "octo-p", key: "~/.ssh/octo-p.pub", route: onboard.RouteSSH}},
		{"default host, key without .pub", noNamed, acct("octo-personal"), want{alias: "github.com", route: onboard.RouteSSH}},
		{"second account's alias", aliasTable, acct("octo-work"), want{alias: "github-work", key: "~/.ssh/work.pub", route: onboard.RouteSSH}},
		{"no ssh route: https via gh", withGH, acct("octo-third"), want{route: onboard.RouteHTTPS}},
		{"neither: fix on the account", aliasTable, acct("octo-nobody"), want{errHas: "gh auth login"}},
		{"forced alias kept, key derived", aliasTable, onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDHostAlias: text("github.com")},
			want{alias: "github.com", route: onboard.RouteSSH}},
		{"forced private key kept", aliasTable, onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDSSHKey: text("~/.ssh/work_key")},
			want{key: "~/.ssh/work_key", route: onboard.RouteSSH}},
		{"forced https kept", aliasTable, onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDRoute: text(string(onboard.RouteHTTPS))},
			want{route: onboard.RouteHTTPS}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := newListerEngine(c.table, nil)
			before := maps.Clone(c.in)
			got, fe := e.deriveRoute(bg, githubStep(), c.in)
			if !sameAnswers(c.in, before) {
				t.Error("the answers map was changed in place")
			}
			if c.want.errHas != "" {
				if len(fe) != 1 || fe[0].Question != onboard.QIDGitHubAccount || !strings.Contains(fe[0].Message, c.want.errHas) {
					t.Fatalf("fe = %+v", fe)
				}
				return
			}
			if len(fe) != 0 {
				t.Fatalf("fe = %+v", fe)
			}
			if got[onboard.QIDHostAlias].Text != c.want.alias || got[onboard.QIDSSHKey].Text != c.want.key || onboard.GitRoute(got[onboard.QIDRoute].Text) != c.want.route {
				t.Errorf("derived = %+v, want %+v", got, c.want)
			}
			// What was derived always passes the validator.
			if fe := e.validateStep(bg, githubStep(), onboard.KindSolo, got); len(fe) != 0 {
				t.Errorf("derived answers refused: %+v", fe)
			}
		})
	}
	// Other steps, nil answers, no account, a failing lister: unchanged.
	e, _ := newListerEngine(aliasTable, nil)
	for _, in := range []onboard.Answers{nil, {}} {
		if out, fe := e.deriveRoute(bg, githubStep(), in); !sameAnswers(out, in) || fe != nil {
			t.Errorf("%+v → %+v %+v", in, out, fe)
		}
	}
	if out, _ := e.deriveRoute(bg, onboard.Step{ID: onboard.StepIdentity}, acct("octo-personal")); out[onboard.QIDRoute].Text != "" {
		t.Errorf("other step derived: %+v", out)
	}
	e, _ = newListerEngine(AliasesOut{}, errs.New(errs.BadPath, "x"))
	if out, fe := e.deriveRoute(bg, githubStep(), acct("octo-personal")); !sameAnswers(out, acct("octo-personal")) || fe != nil {
		t.Errorf("failed lister: %+v %+v", out, fe)
	}
}

// setup.aliases with a user answers the route line too.
func TestAliasesRoute(t *testing.T) {
	e, _ := newListerEngine(aliasTable, nil)
	out, err := e.Aliases(bg, AliasesIn{User: "octo-work"})
	if err != nil || out.Route.Alias != "github-work" || !strings.Contains(out.Route.Line, "lore will connect as octo-work") {
		t.Fatalf("route = %+v err %v", out.Route, err)
	}
	if out, _ := e.Aliases(bg, AliasesIn{}); out.Route.Line != "" {
		t.Errorf("no user, route %+v", out.Route)
	}
}

// sameAnswers compares answers by text (Answer holds a list, so it is not comparable).
func sameAnswers(a, b onboard.Answers) bool {
	return maps.EqualFunc(a, b, func(x, y onboard.Answer) bool { return x.Text == y.Text && slices.Equal(x.List, y.List) })
}

func TestLoginsOf(t *testing.T) {
	got := loginsOf(aliasTable)
	if len(got) != 4 || got[0] != (onboard.AliasLogin{Alias: "octo-p", LogsInAs: "octo-personal"}) || got[2].LogsInAs != "" {
		t.Errorf("logins = %+v", got)
	}
	if got := loginsOf(AliasesOut{}); got == nil || len(got) != 0 {
		t.Errorf("empty = %#v", got)
	}
}

// knownProber adds the KnownAliasLister upgrade: it answers table with
// pending aliases and records each query.
type knownProber struct {
	*listerProber
	pending []string
	qs      []AliasQuery
}

func (p *knownProber) GitHubAliasesKnown(_ context.Context, q AliasQuery) (AliasesOut, []string, error) {
	p.qs = append(p.qs, q)
	return p.table, p.pending, p.err
}

type knownEnv struct {
	fakeEnv
	kp *knownProber
}

func (e knownEnv) SetupProber(context.Context) (Prober, error) { return e.kp, nil }

func newKnownEngine(table AliasesOut, pending []string) (engine, *knownProber) {
	env := newFakeEnv()
	kp := &knownProber{listerProber: &listerProber{fakeProber: env.prober, table: table}, pending: pending}
	return newEngine(knownEnv{fakeEnv: env, kp: kp}, fakeCatalog{}), kp
}

// A cold table: github.com answered as octo-work, the rest still checking.
var coldTable = AliasesOut{Aliases: []GitHubAlias{
	{Alias: "octo-p", IdentityFile: "~/.ssh/octo-p", PubKey: "~/.ssh/octo-p.pub", Status: StatusChecking},
	{Alias: "github-work", IdentityFile: "~/.ssh/work", PubKey: "~/.ssh/work.pub", Status: StatusChecking},
	{Alias: "github.com", IdentityFile: "~/.ssh/work_key", NoPub: true, LogsInAs: "octo-work", Status: "ok"},
}}

func TestDeriveRouteUsesKnownAnswers(t *testing.T) {
	user := onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal")}
	// Nothing answered as the user yet and aliases still pending: "still
	// checking", never https or a false "nothing logs in" error.
	e, kp := newKnownEngine(coldTable, []string{"octo-p", "github-work"})
	_, fe := e.deriveRoute(bg, githubStep(), user)
	if len(fe) != 1 || !strings.Contains(fe[0].Message, "still checking which ssh route logs in as octo-personal") ||
		!strings.Contains(fe[0].Message, "octo-p, github-work") {
		t.Fatalf("cold = %+v", fe)
	}
	if len(kp.qs) != 1 || kp.qs[0].Wait != routeWait || kp.qs[0].User != "octo-personal" {
		t.Errorf("query = %+v", kp.qs)
	}
	// Once an alias answered as the user, it is the route even with others pending.
	warm := coldTable
	warm.Aliases = append([]GitHubAlias{{Alias: "octo-p", PubKey: "~/.ssh/octo-p.pub", LogsInAs: "octo-personal", Status: "ok"}}, coldTable.Aliases[1:]...)
	e, _ = newKnownEngine(warm, []string{"github-work"})
	if got, fe := e.deriveRoute(bg, githubStep(), user); len(fe) != 0 || got[onboard.QIDHostAlias].Text != "octo-p" {
		t.Errorf("warm = %+v %+v", got, fe)
	}
	// A picked alias still checking is no error, and is judged without waiting.
	e, kp = newKnownEngine(coldTable, []string{"octo-p", "github-work"})
	picked := onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDHostAlias: text("github-work")}
	if fe := e.validateStep(bg, githubStep(), onboard.KindSolo, picked); len(fe) != 0 {
		t.Errorf("pending pick = %+v", fe)
	}
	if len(kp.qs) != 1 || kp.qs[0].Wait != 0 {
		t.Errorf("pick query waited: %+v", kp.qs)
	}
	// A picked alias already known to log in as someone else is refused.
	picked[onboard.QIDHostAlias] = text("github.com")
	if fe := e.validateStep(bg, githubStep(), onboard.KindSolo, picked); len(fe) != 1 || fe[0].Rule != ruleAliasUser {
		t.Errorf("known mismatch = %+v", fe)
	}
	// A forced alias needs only file facts: no wait, key filled for a pending alias.
	kp.qs = nil
	got, _ := e.deriveRoute(bg, githubStep(), onboard.Answers{onboard.QIDGitHubAccount: text("octo-personal"), onboard.QIDHostAlias: text("octo-p")})
	if got[onboard.QIDSSHKey].Text != "~/.ssh/octo-p.pub" || len(kp.qs) != 1 || kp.qs[0].Wait != 0 {
		t.Errorf("forced = %+v, qs %+v", got, kp.qs)
	}
}
