package ask

// namecost_test.go — bug hunt H-300 (devco D-1036): one long question took
// 5 s where every other took under 1.5 s. Each scored row runs identBonus,
// which asks namesResource for every question word against every
// identifier of the row, and namesResource rebuilt each matched
// resource's names through a regexp on every call: work grew as rows x
// identifiers x question words x matched resources. The names are now
// built once per resource and the answer kept per word (nameMemo).

import (
	"fmt"
	"strings"
	"testing"
)

// nameCostQuestion is a long question naming three resources (a company,
// a code repo, a person) with many other words, as the slow question did.
const nameCostQuestion = "When acme-co rotates two signing keys on widget_api and bob-lee has none at all, which token does the install link carry and which hmac does the redirect send back?"

// nameCostResources are the resources the question names, each with
// aliases, plus others it does not name.
func nameCostResources() []resourceRow {
	rs := []resourceRow{
		{ID: "res_acme", Kind: "company", Name: "acme-co", Aliases: []string{"Acme Corporation", "ACME", "Acme Widgets Inc."}},
		{ID: "res_widget", Kind: "code_repo", Name: "widget_api", Aliases: []string{"Widget Gateway", "the widget service"}},
		{ID: "res_bob", Kind: "person", Name: "bob-lee", Aliases: []string{"Bobby", "Robert Lee", "CFO"}},
	}
	for i := range 40 {
		rs = append(rs, resourceRow{ID: fmt.Sprintf("res_other%02d", i), Kind: "code_repo", Name: fmt.Sprintf("gizmo_svc_%02d", i), Aliases: []string{fmt.Sprintf("Gizmo Service %d", i)}})
	}
	return rs
}

// nameCostRow is a note full of identifiers whose parts are the
// question's words (signingKeyRotation, install_link_token, …).
func nameCostRow(i int) textRow {
	var b strings.Builder
	fmt.Fprintf(&b, "# widget_api signing notes %d\n\nacme-co rotates keys; bob-lee reviews.\n\n", i)
	parts := []string{"signing", "keys", "rotates", "token", "install", "link", "redirect", "hmac", "carry", "send", "none", "back"}
	for n := range 80 {
		a, c := parts[n%len(parts)], parts[(n*5+3)%len(parts)]
		fmt.Fprintf(&b, "- `%s%s%d` and `%s_%s_v%d` hold the %s step.\n", a, strings.ToUpper(c[:1])+c[1:], n, c, a, n, a)
	}
	return textRow{Table: "memories", ID: fmt.Sprintf("mem_%03d", i), Title: fmt.Sprintf("widget_api signing notes %d", i), Body: b.String()}
}

// TestIdentBonusNoNameWorkPerIdentifier: once a resource's names are
// built, identBonus over a row of 80 identifiers and a question of 25
// words costs no allocation per identifier or word (it allocated
// thousands per row before: a regexp run per word per identifier per
// matched resource).
func TestIdentBonusNoNameWorkPerIdentifier(t *testing.T) { // not parallel: AllocsPerRun
	q := parseQuestion(nameCostQuestion, now)
	rs := nameCostResources()
	ms := matchResources(q, rs)
	if len(ms) < 3 {
		t.Fatalf("the question matched %d resources, want acme-co, widget_api and bob-lee", len(ms))
	}
	r := nameCostRow(1)
	if len(r.idents()) < 60 {
		t.Fatalf("the row holds %d identifiers, want over 60", len(r.idents()))
	}
	want := identBonus(q, ms, &r)
	allocs := testing.AllocsPerRun(20, func() {
		if got := identBonus(q, ms, &r); got != want {
			t.Fatalf("identBonus %d, then %d", want, got)
		}
	})
	if allocs > 8 {
		t.Errorf("identBonus allocates %.0f times per row once the names are built, want at most 8: name work grows with identifiers x question words", allocs)
	}
}

// TestNamesResourceAnswers: namesResource answers as before the memo: a
// name, an alias, a part of a name the words split, a typo of the name
// (any length) or of an alias (four letters on), a said word; nothing
// else. Asked twice, it answers the same; a copied row with other
// aliases answers by its own.
func TestNamesResourceAnswers(t *testing.T) {
	t.Parallel()
	rs := nameCostResources()
	ms := []match{{res: &rs[0]}, {res: &rs[1]}, {res: &rs[2], said: []string{"reviewer"}}}
	for w, want := range map[string]bool{
		"acme-co": true, "acme-cx": true, "acme": true, // name, typo of the name, alias key
		"widget": true, "api": true, "widget_api": true, // parts of widget_api, the name
		"bobby": true, "boby": true, "cfo": true, "cto": false, // alias, typo of an alias, short alias, short typo
		"reviewer": true, "token": false, "redirect": false, "gizmo": false,
	} {
		for range 2 {
			if got := namesResource(w, ms); got != want {
				t.Errorf("namesResource(%q) = %v, want %v", w, got, want)
			}
		}
	}
	// a copy of a row with other aliases (a test's fixture) answers by its
	// own aliases, not by the names kept for the row it was copied from
	cp := rs[2]
	cp.Aliases = []string{"Huey"}
	if !namesResource("huey", []match{{res: &cp}}) || namesResource("bobby", []match{{res: &cp}}) {
		t.Errorf("a copied row with the alias Huey still answers by the aliases of the row it was copied from")
	}
}

// TestLongQuestionOverIdentifierRowsBoundedWork: the whole answer to the
// long question over 60 identifier-heavy rows allocates a bounded amount
// per row: about 1,000 now, about 80,000 before H-300 (the name checks
// alone), which took 5 s on a store of 4,000 rows.
func TestLongQuestionOverIdentifierRowsBoundedWork(t *testing.T) { // not parallel: AllocsPerRun
	q := parseQuestion(nameCostQuestion, now)
	var rows []textRow
	for i := range 60 {
		rows = append(rows, nameCostRow(i))
	}
	if o := build(q, data{resources: nameCostResources(), rows: rows}, 0, 0); len(o.Rows) == 0 {
		t.Fatalf("no rows answered the long question")
	}
	allocs := testing.AllocsPerRun(2, func() {
		build(q, data{resources: nameCostResources(), rows: rows}, 0, 0)
	})
	if per := allocs / float64(len(rows)); per > 5000 {
		t.Errorf("ask allocates %.0f per row for a long question naming three resources, want at most 5000: work grows with identifiers x question words x resources", per)
	}
}
