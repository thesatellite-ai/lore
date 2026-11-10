//go:build userscen

package userscen

// rest_parity_test.go — CLI vs `lore api` parity (ONE OPERATION SPEC, spec
// 9.2/9.4), extending tests/teame2e/api_parity_e2e_test.go (which covers
// the Mode B, leak, CFG7 and new-folder refusals and CLI writes beside a
// running API) to a matrix over the everyday nouns:
//
//   - reads: the same list and show give the same JSON document (schema
//     version, kind, count, every row field) on both surfaces, for rows
//     written by either surface;
//   - refusals: the same bad input gives the same error code, the same
//     message, the same exit in the error object; the CLI exits with that
//     exit code and the HTTP status is the one of its class (model/errs);
//   - confirmations: an operation that asks first is E_NEEDS_CONFIRM
//     (CLI 26 / HTTP 428) on both, and both act once confirmed (--yes /
//     "yes": true), leaving the same state.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// restAddID runs a CLI add with --json and returns the new row's id.
func restAddID(t *testing.T, rw *restWorld, args ...string) string {
	t.Helper()
	r := rw.repo.MustLore(append(args, "--json")...)
	m := restIDRe.FindStringSubmatch(r.Stdout)
	if m == nil {
		t.Fatalf("%v printed no id: %s", args, r.Out())
	}
	return m[1]
}

// TestRestParity_ReadsSameDocument: list and show answer the same document
// over the CLI (--json) and REST, whichever surface wrote the rows.
func TestRestParity_ReadsSameDocument(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	mem := restAddID(t, rw, "memory", "add", "--body", "a memory from the CLI")
	task := restAddID(t, rw, "task", "add", "a task from the CLI", "--to", "repo", "--commitment", "accepted")
	dec := restAddID(t, rw, "decision", "add", "--title", "a decision from the CLI", "--body", "why")
	for _, b := range []struct{ path, body string }{
		{"/v1/memories", `{"body":"a memory over REST"}`},
		{"/v1/tasks", `{"title":"a task over REST","to":"repo","commitment":"accepted"}`},
		{"/v1/decisions", `{"title":"a decision over REST","body":"why"}`},
	} {
		if rep := srv.post(t, b.path, b.body); rep.status != http.StatusCreated {
			t.Fatalf("POST %s: %d %.300s", b.path, rep.status, rep.body)
		}
	}
	cases := []struct {
		cli  []string
		path string
	}{
		{[]string{"memory", "list"}, "/v1/memories"},
		{[]string{"memory", "show", mem}, "/v1/memories/" + mem},
		{[]string{"task", "list", "--all"}, "/v1/tasks?all=true"},
		{[]string{"task", "show", task}, "/v1/tasks/" + task},
		{[]string{"decision", "list"}, "/v1/decisions"},
		{[]string{"decision", "show", dec}, "/v1/decisions/" + dec},
		{[]string{"tag", "list"}, "/v1/tags"},
		{[]string{"rule", "list"}, "/v1/rules"},
	}
	for _, c := range cases {
		cli := rw.repo.Lore(append(c.cli, "--json")...)
		rep := srv.get(t, c.path)
		what := strings.Join(c.cli, " ")
		if cli.Code != 0 || rep.status != http.StatusOK {
			t.Errorf("%s: CLI exit %d, REST %d: %s / %.300s", what, cli.Code, rep.status, cli.Out(), rep.body)
			continue
		}
		if !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
			t.Errorf("%s: CLI and REST documents differ\nCLI:  %.600s\nREST: %.600s", what, cli.Stdout, rep.body)
		}
	}
}

// restRefusalCase is one bad input given to both surfaces.
type restRefusalCase struct {
	name string
	cli  []string
	req  restReq
	code string
	say  string // part of the message both must print
}

// TestRestParity_RefusalsSameErrorObject: the same refusal on both
// surfaces, with exit and status from the code's class.
func TestRestParity_RefusalsSameErrorObject(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	mem := restAddID(t, rw, "memory", "add", "--body", "kept")
	task := restAddID(t, rw, "task", "add", "kept task", "--to", "repo", "--commitment", "accepted")
	// Well-formed ids that do not exist here: rows of bob's own repo.
	bob := rw.w.Human("bob", "bob@example.com")
	other := bob.Clone(rw.w.Remote("github.com/octo-personal/other"), "other")
	other.MustLore("init", "--non-interactive", "--name=other")
	idOf := func(args ...string) string {
		t.Helper()
		m := restIDRe.FindStringSubmatch(other.MustLore(append(args, "--json")...).Stdout)
		if m == nil {
			t.Fatalf("%v printed no id", args)
		}
		return m[1]
	}
	missingMem := idOf("memory", "add", "--body", "bob's")
	missingTask := idOf("task", "add", "bob's task", "--to", "repo", "--commitment", "accepted")
	missingDec := idOf("decision", "add", "--title", "bob's decision", "--body", "why")
	cases := []restRefusalCase{
		{"malformed memory id", []string{"memory", "show", "mem_nope"}, restReq{method: http.MethodGet, path: "/v1/memories/mem_nope"}, "E_INVALID_ID", "is not a memory id"},
		{"task id given to memory", []string{"memory", "show", task}, restReq{method: http.MethodGet, path: "/v1/memories/" + task}, "E_INVALID_ID", "is not a memory id"},
		{"missing memory", []string{"memory", "show", missingMem}, restReq{method: http.MethodGet, path: "/v1/memories/" + missingMem}, "E_NOT_FOUND", missingMem},
		{"missing task", []string{"task", "show", missingTask}, restReq{method: http.MethodGet, path: "/v1/tasks/" + missingTask}, "E_NOT_FOUND", missingTask},
		{"missing decision", []string{"decision", "show", missingDec}, restReq{method: http.MethodGet, path: "/v1/decisions/" + missingDec}, "E_NOT_FOUND", missingDec},
		{"edit a missing memory", []string{"memory", "edit", missingMem, "--body", "x"}, restReq{method: http.MethodPatch, path: "/v1/memories/" + missingMem, body: `{"body":"x"}`}, "E_NOT_FOUND", missingMem},
		{"unknown commitment", []string{"task", "add", "t", "--to", "repo", "--commitment", "bogus"}, restReq{method: http.MethodPost, path: "/v1/tasks", body: `{"title":"t","to":"repo","commitment":"bogus"}`}, "E_INVALID_INPUT", "bogus"},
		{"unknown priority", []string{"task", "add", "t", "--to", "repo", "--priority", "p9"}, restReq{method: http.MethodPost, path: "/v1/tasks", body: `{"title":"t","to":"repo","priority":"p9"}`}, "E_INVALID_INPUT", "p9"},
		{"empty memory body", []string{"memory", "add", "--body", "   "}, restReq{method: http.MethodPost, path: "/v1/memories", body: `{"body":"   "}`}, "", ""},
		{"unknown store", []string{"memory", "add", "--body", "x", "--to", "no_such_store"}, restReq{method: http.MethodPost, path: "/v1/memories", body: `{"body":"x","to":"no_such_store"}`}, "", "no_such_store"},
		{"task add after a missing task", []string{"task", "add", "t", "--to", "repo", "--after", missingTask}, restReq{method: http.MethodPost, path: "/v1/tasks", body: `{"title":"t","to":"repo","after":["` + missingTask + `"]}`}, "", missingTask},
		{"memory delete without confirm", []string{"memory", "delete", mem, "--purge"}, restReq{method: http.MethodDelete, path: "/v1/memories/" + mem + "?purge=true"}, "", ""},
	}
	for _, c := range cases {
		cli := rw.repo.Lore(append(c.cli, "--json")...)
		rep := srv.do(t, c.req)
		cliErr := restCLIErr(cli)
		if cliErr == nil || rep.err == nil {
			t.Errorf("%s: both must refuse: CLI exit %d %s / REST %d %.300s", c.name, cli.Code, cli.Out(), rep.status, rep.body)
			continue
		}
		if c.code != "" && cliErr.Code != c.code {
			t.Errorf("%s: CLI code %s, want %s (%s)", c.name, cliErr.Code, c.code, cliErr.Message)
		}
		if rep.err.Code != cliErr.Code || rep.err.Message != cliErr.Message || rep.err.Hint != cliErr.Hint || rep.err.Exit != cliErr.Exit || rep.err.Class != cliErr.Class {
			t.Errorf("%s: error objects differ\nCLI:  %+v\nREST: %+v", c.name, *cliErr, *rep.err)
		}
		if c.say != "" && !strings.Contains(cliErr.Message, c.say) {
			t.Errorf("%s: message %q lacks %q", c.name, cliErr.Message, c.say)
		}
		if cli.Code != cliErr.Exit {
			t.Errorf("%s: CLI exited %d but its error object says %d", c.name, cli.Code, cliErr.Exit)
		}
		if want := errs.HTTPFor(errs.Class(cliErr.Class), cliErr.Exit); rep.status != want {
			t.Errorf("%s: HTTP %d, want %d for class %s", c.name, rep.status, want, cliErr.Class)
		}
		if rep.status >= 500 && cliErr.Code != "E_INTERNAL" {
			t.Errorf("%s: %s answered %d", c.name, cliErr.Code, rep.status)
		}
	}
	// The kept rows are still there: no refusal changed anything.
	rw.repo.MustLore("memory", "show", mem)
	rw.repo.MustLore("task", "show", task)
}

// TestRestParity_ConfirmThenAct: delete with --purge asks first on both
// surfaces (E_NEEDS_CONFIRM, CLI 26, HTTP 428; --yes / "yes" do not purge)
// and both purge once confirmed with --confirm / "confirm": the state
// afterwards is the same (E_PURGED on both reads).
func TestRestParity_ConfirmThenAct(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	viaCLI := restAddID(t, rw, "memory", "add", "--body", "purge me over the CLI")
	viaREST := restAddID(t, rw, "memory", "add", "--body", "purge me over REST")

	ask := rw.repo.Lore("memory", "delete", viaCLI, "--purge", "--json")
	askREST := srv.do(t, restReq{method: http.MethodDelete, path: "/v1/memories/" + viaREST + "?purge=true"})
	cliErr := restCLIErr(ask)
	if cliErr == nil || askREST.err == nil {
		t.Fatalf("purge without confirm: CLI %d %s / REST %d %.300s", ask.Code, ask.Out(), askREST.status, askREST.body)
	}
	if cliErr.Code != askREST.err.Code {
		t.Errorf("purge without confirm: CLI %s / REST %s", cliErr.Code, askREST.err.Code)
	}
	if cliErr.Code == "E_NEEDS_CONFIRM" && (ask.Code != 26 || askREST.status != http.StatusPreconditionRequired) {
		t.Errorf("E_NEEDS_CONFIRM: CLI exit %d, HTTP %d", ask.Code, askREST.status)
	}
	// Both rows are still there after the refusal.
	for _, id := range []string{viaCLI, viaREST} {
		if r := srv.get(t, "/v1/memories/"+id); r.status != http.StatusOK {
			t.Fatalf("%s gone after a refused purge: %d", id, r.status)
		}
	}
	// --yes is not the purge confirmation on either surface.
	yesCLI := rw.repo.Lore("memory", "delete", viaCLI, "--purge", "--yes", "--json")
	yesREST := srv.do(t, restReq{method: http.MethodDelete, path: "/v1/memories/" + viaREST + "?purge=true&yes=true"})
	if ye := restCLIErr(yesCLI); ye == nil || yesREST.err == nil || ye.Code != "E_NEEDS_CONFIRM" || yesREST.err.Code != ye.Code || yesREST.err.Hint != ye.Hint {
		t.Errorf("purge with yes only: CLI %s / REST %d %.300s", yesCLI.Out(), yesREST.status, yesREST.body)
	}
	// Confirmed.
	rw.repo.MustLore("memory", "delete", viaCLI, "--purge", "--confirm")
	if r := srv.do(t, restReq{method: http.MethodDelete, path: "/v1/memories/" + viaREST + "?purge=true&confirm=true"}); r.status != http.StatusOK {
		t.Fatalf("REST purge with yes: %d %.300s", r.status, r.body)
	}
	for _, id := range []string{viaCLI, viaREST} {
		cli := rw.repo.Lore("memory", "show", id, "--json")
		rep := srv.get(t, "/v1/memories/"+id)
		ce := restCLIErr(cli)
		if ce == nil || rep.err == nil || ce.Code != rep.err.Code || ce.Code != "E_PURGED" || rep.status != errs.HTTPFor(errs.Class(ce.Class), ce.Exit) {
			t.Errorf("%s after purge: CLI %+v / REST %d %.300s", id, ce, rep.status, rep.body)
		}
	}
	// Running the confirmed purge twice: the second refuses the same way on both.
	again := rw.repo.Lore("memory", "delete", viaCLI, "--purge", "--confirm", "--json")
	againREST := srv.do(t, restReq{method: http.MethodDelete, path: "/v1/memories/" + viaREST + "?purge=true&confirm=true"})
	ae := restCLIErr(again)
	if ae == nil || againREST.err == nil || ae.Code != againREST.err.Code {
		t.Errorf("second purge: CLI %s / REST %d %.300s", again.Out(), againREST.status, againREST.body)
	}
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(srv.get(t, "/v1/memories").body), &list); err != nil || len(list.Data) != 0 {
		t.Errorf("memories left after both purges: %d (%v)", len(list.Data), err)
	}
}
