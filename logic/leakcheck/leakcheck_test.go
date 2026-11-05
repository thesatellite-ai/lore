package leakcheck

import (
	"strings"
	"testing"
)

const (
	memID   = "mem_0b770000000000000000000000000001"
	memID2  = "mem_0c120000000000000000000000000002"
	actID   = "act_3f9c0000000000000000000000000003"
	repoPID = "prj_aaaa0000000000000000000000000000"
	intPID  = "prj_bbbb0000000000000000000000000000"
	clientT = "tsk_0100000000000000000000000000000a"
)

const note = "client is slow to pay so always send the estimate before the call"

func index() *Index {
	return NewIndex([]IndexRow{
		{ID: memID, Table: "memories", Label: "[northwind/acme]", Texts: []string{note}},
		{ID: memID2, Table: "memories", Label: "[me]", Texts: []string{"I forget the staging VPN password rotation every single month"}},
		{ID: actID, Table: "actors", Label: "[northwind/org]", Texts: []string{"Alice Doe alice at northwind example"}},
	}, map[string]string{intPID: "[northwind/acme]"})
}

func rules(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, string(f.Rule)+"@"+f.Where)
	}
	return out
}

// F16 matrix: id elsewhere / reference / text at 7 and 8 words (case,
// punctuation, unicode) / allowed actor projection / secret, at every
// place (staged file, commit message, live write, PR body).
func TestCheckMatrix(t *testing.T) {
	words := strings.Fields(note)
	cases := []struct {
		name  string
		item  Item
		want  Rule // "" = clean
		rowID string
	}{
		{"7 words in source", Item{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("// " + strings.Join(words[2:9], " "))}, "", ""},
		{"8 words in source", Item{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("// " + strings.Join(words[2:10], " "))}, RuleText, memID},
		{"8 words, upper case and commas", Item{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("// " + strings.ToUpper(strings.Join(words[2:10], ", ")) + "!")}, RuleText, memID},
		{"8 words split across lines", Item{Where: "README.md", Path: "README.md", Content: []byte(strings.Join(words[0:4], " ") + "\n" + strings.Join(words[4:8], " "))}, RuleText, memID},
		{"8 words, fullwidth unicode (NFKC)", Item{Where: "x", Path: "x", Content: []byte("ｃｌｉｅｎｔ is slow to pay so always send")}, RuleText, memID},
		{"commit message", Item{Where: "commit message", Content: []byte(strings.Join(words[4:12], " "))}, RuleText, memID},
		{"PR body personal note", Item{Where: "PR body", Content: []byte("note: I forget the staging VPN password rotation every single month")}, RuleText, memID2},
		{"internal id in source", Item{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("// see " + memID)}, RuleID, memID},
		{"internal row file copied", Item{Where: ".lore/data/memories/" + memID + ".json", Path: ".lore/data/memories/" + memID + ".json", Content: []byte(`{"id":"` + memID + `","project_id":"` + repoPID + `"}`)}, RuleID, memID},
		{"row of another project (unknown id)", Item{Where: "f", Path: ".lore/data/memories/mem_ffff0000000000000000000000000009.json", Content: []byte(`{"id":"mem_ffff0000000000000000000000000009","project_id":"` + intPID + `"}`)}, RuleID, ""},
		{"client row points at internal row", Item{Where: "c", Path: ".lore/data/comments/cmt_0100000000000000000000000000000b.json", Content: []byte(`{"id":"cmt_0100000000000000000000000000000b","entity_id":"` + memID + `","project_id":"` + repoPID + `"}`)}, RuleRef, memID},
		{"client row points into internal project", Item{Where: "c", Path: ".lore/data/comments/cmt_0100000000000000000000000000000c.json", Content: []byte(`{"id":"cmt_0100000000000000000000000000000c","entity_project_id":"` + intPID + `","project_id":"` + repoPID + `"}`)}, RuleRef, ""},
		{"client task referencing an actor is fine", Item{Where: "t", Path: ".lore/data/tasks/" + clientT + ".json", Content: []byte(`{"id":"` + clientT + `","assigned_to_actor_id":"` + actID + `","project_id":"` + repoPID + `","title":"migrate checkout"}`)}, "", ""},
		{"actor projection allowed", Item{Where: "a", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"_table":"actors","id":"` + actID + `","display_name":"Alice Doe","kind":"human"}`)}, "", ""},
		{"actor projection with empty extra columns", Item{Where: "a", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"id":"` + actID + `","display_name":"Alice Doe","kind":"human","handle":null,"label":"","owner_actor_id":null}`)}, "", ""},
		{"agent actor with owner and label", Item{Where: "a", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"id":"` + actID + `","display_name":"backend-1","kind":"agent","label":"backend-1","owner_actor_id":"act_x","stable_key":"agent:backend-1"}`)}, RuleActor, actID},
		{"secret in live write", Item{Where: "live write", Content: []byte(`{"body":"db is postgres://admin:hunter2pass@db:5432/app"}`)}, RuleSecret, ""},
		{"roster name is not text-matched", Item{Where: "README.md", Path: "README.md", Content: []byte("Alice Doe alice at northwind example wrote this code here")}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := Check([]Item{tc.item}, index(), Options{Secrets: true, SecretScan: fakeSecrets, RepoProjectID: repoPID, ClientRepo: true})
			if tc.want == "" {
				if len(fs) != 0 {
					t.Fatalf("want clean, got %v %+v", rules(fs), fs)
				}
				return
			}
			found := false
			for _, f := range fs {
				if f.Rule == tc.want && (tc.rowID == "" || f.RowID == tc.rowID) {
					found = true
					if f.Where != tc.item.Where {
						t.Fatalf("finding names %q, want %q", f.Where, tc.item.Where)
					}
				}
			}
			if !found {
				t.Fatalf("want %s on %s, got %+v", tc.want, tc.rowID, fs)
			}
		})
	}
}

// stable_key in a client actor copy BLOCKS (BUG-D2, fail closed): the
// export writes only the projection, so a person's email in a client repo
// is a leak, never a warning. Agent details block too. A projection alone
// passes.
func TestStableKeyBlocks(t *testing.T) {
	it := Item{Where: "a", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"id":"` + actID + `","display_name":"Alice","kind":"human","stable_key":"human:alice@northwind.example","handle":"alice"}`)}
	fs := Check([]Item{it}, index(), Options{ClientRepo: true})
	if len(fs) != 1 || fs[0].Warn || len(Blocking(fs)) != 1 {
		t.Fatalf("stable_key must block: %+v", fs)
	}
	it.Content = []byte(`{"_table":"actors","_v":1,"id":"` + actID + `","display_name":"Alice","kind":"human"}`)
	if fs := Check([]Item{it}, index(), Options{ClientRepo: true}); len(fs) != 0 {
		t.Fatalf("the projection must pass: %+v", fs)
	}
	it.Content = []byte(`{"id":"` + actID + `","kind":"agent","label":"backend-1","stable_key":"agent:backend-1"}`)
	if fs := Check([]Item{it}, index(), Options{ClientRepo: true}); len(Blocking(fs)) != 1 {
		t.Fatalf("an agent label always blocks: %+v", fs)
	}
}

// Monorepo: a sibling project's rows are not "another project" for this
// project's hook block (the sibling checks its own).
func TestDataPrefixScopesProjectRule(t *testing.T) {
	it := Item{Where: "w", Path: "web/.lore/data/rules/rul_0000000000000000000000000000000a.json", Content: []byte(`{"project_id":"prj_web0000000000000000000000000000"}`)}
	if fs := Check([]Item{it}, nil, Options{RepoProjectID: repoPID, DataPrefix: ".lore/data/"}); len(fs) != 0 {
		t.Fatalf("sibling project rows: %+v", fs)
	}
	it.Path = ".lore/data/rules/rul_0000000000000000000000000000000a.json"
	if fs := Check([]Item{it}, nil, Options{RepoProjectID: repoPID, DataPrefix: ".lore/data/"}); len(fs) != 1 {
		t.Fatalf("own data dir with a foreign project id: %+v", fs)
	}
}

// Outside a client repo an actor file is the repo's own (no projection).
func TestActorRuleOnlyInClientRepos(t *testing.T) {
	it := Item{Where: "a", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"id":"` + actID + `","stable_key":"human:dev@acme.example"}`)}
	if fs := Check([]Item{it}, index(), Options{}); len(fs) != 0 {
		t.Fatalf("no client repo, no projection rule: %+v", fs)
	}
}

// The text finding reports the run length and the line.
func TestTextRunLengthAndLine(t *testing.T) {
	content := "first line\n// " + note + "\n"
	fs := Check([]Item{{Where: "src/c.ts", Path: "src/c.ts", Content: []byte(content)}}, index(), Options{})
	if len(fs) != 1 || fs[0].Words != len(strings.Fields(note)) || fs[0].Line != 2 {
		t.Fatalf("got %+v", fs)
	}
}

// CI mode: structure only, no index; never names an internal id.
func TestCheckCI(t *testing.T) {
	files := map[string][]byte{
		".lore/data/memories/" + memID + ".json":                        []byte(`{"id":"` + memID + `","project_id":"` + intPID + `","body":"` + note + `"}`),
		".lore/data/rules/rul_0000000000000000000000000000000a.json":    []byte(`{"id":"rul_0000000000000000000000000000000a","project_id":"` + repoPID + `"}`),
		".lore/data/comments/cmt_0000000000000000000000000000000b.json": []byte(`{"id":"cmt_0000000000000000000000000000000b","project_id":"` + repoPID + `","entity_project_id":"` + intPID + `"}`),
		".lore/data/actors/" + actID + ".json":                          []byte(`{"id":"` + actID + `","owner_actor_id":"x"}`),
		"src/app.ts":                                                    []byte("x"),
	}
	fs := CheckCI(files, repoPID)
	if len(fs) != 3 {
		t.Fatalf("want 3 CI findings, got %+v", fs)
	}
	for _, f := range fs {
		s := f.Where + " " + f.Detail
		if strings.Contains(s, memID) || strings.Contains(s, intPID) || strings.Contains(s, actID) || strings.Contains(s, "slow to pay") {
			t.Fatalf("CI output must name nothing internal: %q", s)
		}
	}
	if fs := CheckCI(map[string][]byte{".lore/data/rules/rul_0000000000000000000000000000000a.json": []byte(`{"project_id":"` + repoPID + `"}`)}, repoPID); len(fs) != 0 {
		t.Fatalf("own rows are clean: %+v", fs)
	}
}

func TestNeedsIndex(t *testing.T) {
	cases := []struct {
		items []Item
		want  bool
	}{
		{nil, false},
		{[]Item{{Where: "commit message", Content: []byte("lore")}}, false},
		{[]Item{{Where: "commit message", Content: []byte("one two three four five six seven")}}, false},
		{[]Item{{Where: "commit message", Content: []byte("one two three four five six seven eight")}}, true},
		{[]Item{{Where: "commit message", Content: []byte("see " + memID)}}, true},
		{[]Item{{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("x")}}, true},
	}
	for i, c := range cases {
		if got := NeedsIndex(c.items); got != c.want {
			t.Errorf("case %d: NeedsIndex = %v, want %v", i, got, c.want)
		}
	}
}

// TestSessionIDRule pins section 9: a known tool session id in text bound
// for git blocks, and the finding never prints the id.
func TestSessionIDRule(t *testing.T) {
	const sid = "019a7c2e-5d1b-7f00-9c3a-0e4b1f2a3c4d"
	items := []Item{{Where: "this memory", Content: []byte("resume with\n" + sid + " later")}}
	fs := Check(items, nil, Options{SessionIDs: []string{sid}})
	if len(fs) != 1 || fs[0].Rule != RuleSession || fs[0].Line != 2 || strings.Contains(fs[0].Detail, sid) {
		t.Fatalf("session id not blocked or printed: %+v", fs)
	}
	if fs := Check([]Item{{Where: "x", Content: []byte("nothing here")}}, nil, Options{SessionIDs: []string{sid}}); len(fs) != 0 {
		t.Fatalf("false hit: %+v", fs)
	}
}

// fakeSecrets is the secret rule's scanner in these tests (the real one is
// an adapter wire passes): a URL with a password is a credential.
func fakeSecrets(it Item) []SecretHit {
	if strings.Contains(string(it.Content), "://admin:") {
		return []SecretHit{{Line: 1, Rule: "url-password", Preview: "postgres://admin:***"}}
	}
	return nil
}
