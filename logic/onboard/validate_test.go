package onboard

import (
	"slices"
	"testing"
)

func TestValidateValue(t *testing.T) {
	cases := []struct {
		rule Rule
		v    string
		ok   bool
	}{
		{R(RuleEmail), "mira@agency.example", true},
		{R(RuleEmail), "Mira <mira@agency.example>", false},
		{R(RuleEmail), "mira", false},
		{R(RuleEmail), "", false},
		{R(RuleSnakeCase), "acme_hub", true},
		{R(RuleSnakeCase), "acme-hub", false},
		{R(RuleSnakeCase), "1acme", false},
		{R(RuleSnakeCase), "Acme", false},
		{R(RuleSetupName), "acme-hub", true},
		{R(RuleSetupName), "acme hub", false},
		{R(RuleGitURL), "git@github.com:agency/lore-me.git", true},
		{R(RuleGitURL), "git@github-agency:agency/lore-me.git", true},
		{R(RuleGitURL), "ssh://git@github.com/agency/lore-me.git", true},
		{R(RuleGitURL), "https://github.com/agency/lore-me.git", true},
		{R(RuleGitURL), "file://~/.lore/stores/me", true},
		{R(RuleGitURL), "file:///srv/lore/me", true},
		{R(RuleGitURL), userinfoURL("https", "mira", "ghp_x1y2", "github.com/agency/lore-me.git"), false},
		{R(RuleGitURL), "https://ghp_" + "x1y2" + "@github.com/agency/lore-me.git", false},
		{R(RuleGitURL), userinfoURL("ssh", "git", "pw", "github.com/agency/lore-me.git"), false},
		{R(RuleGitURL), "mira:tok@github.com:agency/lore-me.git", false},
		{R(RuleGitURL), "--upload-pack=touch /tmp/x", false},
		{R(RuleGitURL), "ftp://github.com/a/b", false},
		{R(RuleGitURL), "git@github.com:a/b.git\nx", false},
		{R(RuleGitURL), "https://github.com/a/b?x=1", false},
		{MaxLen(3), "abc", true},
		{MaxLen(3), "abcd", false},
		{MaxLen(3), "äöü", true},
		{Rule("max_len:x"), "a", false},
		{NotIn("repo", "me"), "notes", true},
		{NotIn("repo", "me"), "ME", false},
		{R(RuleKeyPath), "~/.ssh/id_ed25519.pub", true},
		{R(RuleKeyPath), "~/.ssh/id_ed25519", true},
		{R(RuleKeyPath), "/home/octo-user/.ssh/work-key", true},
		{R(RuleKeyPath), "id_ed25519", false},
		{R(RuleKeyPath), "-oProxyCommand=x", false},
		{R(RuleKeyPath), "~/.ssh/a\nb", false},
		{R(RuleAbsGlob), "~/www/projects/agency/**", true},
		{R(RuleAbsGlob), "/srv/work/**", true},
		{R(RuleAbsGlob), "www/**", false},
		{R(RuleAbsGlob), "~", true},
		{Rule("nope"), "x", false},
	}
	for _, c := range cases {
		if _, ok := ValidateValue(c.rule, c.v); ok != c.ok {
			t.Errorf("ValidateValue(%q, %q) ok = %v, want %v", c.rule, c.v, ok, c.ok)
		}
	}
}

func TestValidateAnswers(t *testing.T) {
	kind, _ := StepByID(StepKind)
	ident, _ := StepByID(StepIdentity)
	folders, _ := StepByID(StepFolders)
	ps, _ := StepByID(StepPersonalStore)
	txt := func(s string) Answer { return Answer{Text: s} }
	list := func(s ...string) Answer { return Answer{List: s, IsList: true} }
	b := func(v bool) Answer { return Answer{Bool: v, IsBool: true} }
	type fe struct{ q, rule string }
	cases := []struct {
		name string
		step Step
		kind Kind
		a    Answers
		want []fe
	}{
		{"kind ok", kind, KindSolo, Answers{"kind": txt("solo"), "name": txt("personal"), "default": b(false)}, nil},
		{"kind missing name", kind, KindSolo, Answers{"kind": txt("solo")}, []fe{{"name", FieldRuleRequired}}},
		{"kind bad choice", kind, KindSolo, Answers{"kind": txt("boss"), "name": txt("personal")}, []fe{{"kind", FieldRuleChoice}}},
		{"kind reserved name", kind, KindSolo, Answers{"kind": txt("solo"), "name": txt("me")}, []fe{{"name", "not_in:repo,me,team,company"}}},
		{"default not for member", kind, KindMember, Answers{"kind": txt("member"), "name": txt("acme-hub"), "default": b(true)}, []fe{{"default", FieldRuleUnknown}}},
		{"bool as text", kind, KindSolo, Answers{"kind": txt("solo"), "name": txt("personal"), "default": txt("yes")}, []fe{{"default", FieldRuleType}}},
		{"identity ok", ident, KindMember, Answers{"name": txt("Mira"), "email": txt("mira@agency.example"), "emails": list("m@old.example")}, nil},
		{"identity bad list item", ident, KindSolo, Answers{"name": txt("Mira"), "email": txt("mira@agency.example"), "emails": list("ok@agency.example", "nope")}, []fe{{"emails", "email"}}},
		{"identity list as text", ident, KindSolo, Answers{"name": txt("Mira"), "email": txt("mira@agency.example"), "emails": txt("a@b.example")}, []fe{{"emails", FieldRuleType}}},
		{"identity bad email", ident, KindAdmin, Answers{"name": txt("Mira"), "email": txt("mira at agency")}, []fe{{"email", "email"}}},
		{"folders relative", folders, KindSolo, Answers{"folders": list("~/www/**", "www/x")}, []fe{{"folders", "abs_glob"}}},
		{"store token url", ps, KindSolo, Answers{"personal_store": txt("existing"), "store_url": txt("https://ghp_" + "x" + "@github.com/a/b.git")}, []fe{{"store_url", "git_url"}}},
		{"store none", ps, KindSolo, Answers{"personal_store": txt("none")}, nil},
		{"unknown key", ps, KindSolo, Answers{"personal_store": txt("none"), "zz": txt("x")}, []fe{{"zz", FieldRuleUnknown}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []fe
			for _, e := range ValidateAnswers(c.step, c.kind, c.a) {
				if e.Message == "" {
					t.Errorf("%s: empty message", e.Question)
				}
				got = append(got, fe{e.Question, e.Rule})
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("ValidateAnswers = %v, want %v", got, c.want)
			}
		})
	}
}
