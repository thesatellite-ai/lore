//go:build userscen

package userscen

// Knowledge campaign: every knowledge kind through `lore api` against the
// same op on the CLI. For each kind: a row added on each surface; list and
// show give the same JSON document; search the same hits; a REST edit is
// what the CLI then shows; a bad id is the same error object (code,
// message, hint, exit); delete without "yes" asks on both (CLI 26 / HTTP
// 428) and acts once confirmed; archive / unarchive where the kind has
// them. Plus the kind-specific routes: memory invalidate, handoff ack,
// explain, lint, directive show, why-context.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// campRESTCollection is the REST collection of a knowledge noun
// (docs/reference/openapi.json).
var campRESTCollection = map[string]string{
	"memory": "memories", "rule": "rules", "decision": "decisions", "hotfix": "hotfixes",
	"pattern": "patterns", "playbook": "playbooks", "prompt": "prompts",
	"architecturenote": "architecturenotes", "cookbookrecipe": "cookbookrecipes",
	"incident": "incidents", "tastepref": "tasteprefs", "behaviour": "behaviours",
	"snapshot": "snapshots", "suggestion": "suggestions",
}

// campAddJSON is the REST add body of kind k (behaviour's add input names
// its title "name"; every other titled kind "title").
func campAddJSON(t *testing.T, k knowKind, title, body string) string {
	t.Helper()
	m := map[string]string{"body": body}
	if k.addTitle {
		key := "title"
		if k.noun == "behaviour" {
			key = "name"
		}
		m[key] = title
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// campSameDoc fails when the CLI --json run and the REST reply differ.
func campSameDoc(t *testing.T, what string, cliOut string, cliCode int, rep restReply) {
	t.Helper()
	if cliCode != 0 || rep.status != http.StatusOK {
		t.Fatalf("%s: CLI exit %d, REST %d: %.300s / %.300s", what, cliCode, rep.status, cliOut, rep.body)
	}
	if !restJSONEqual([]byte(cliOut), []byte(rep.body)) {
		t.Fatalf("%s: CLI and REST differ\nCLI:  %.600s\nREST: %.600s", what, cliOut, rep.body)
	}
}

func TestCampaignKnowledge_RESTParityEveryKind(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			rw := newRestWorld(t)
			srv := restStart(t, rw.repo)
			c := rw.repo
			coll := "/v1/" + campRESTCollection[k.noun]

			viaCLI := campMustAdd(t, c, k, "From the CLI", "body from the CLI 🚀")
			add := srv.post(t, coll, campAddJSON(t, k, "Over REST", "body over REST שלום"))
			if add.status != http.StatusCreated {
				t.Fatalf("POST %s: %d %.300s", coll, add.status, add.body)
			}
			m := restIDRe.FindStringSubmatch(add.body)
			if m == nil || !strings.HasPrefix(m[1], k.prefix+"_") {
				t.Fatalf("POST %s answered no %s_ id: %.300s", coll, k.prefix, add.body)
			}
			viaREST := m[1]

			l := c.Lore(k.noun, "list", "--json")
			campSameDoc(t, "list", l.Stdout, l.Code, srv.get(t, coll))
			for _, id := range []string{viaCLI, viaREST} {
				s := c.Lore(k.noun, "show", id, "--json")
				campSameDoc(t, "show "+id, s.Stdout, s.Code, srv.get(t, coll+"/"+id))
			}
			sr := c.Lore(k.noun, "search", "body", "--json")
			campSameDoc(t, "search", sr.Stdout, sr.Code, srv.post(t, coll+"/search", `{"query":"body"}`))

			if k.canEdit {
				rep := srv.do(t, restReq{method: http.MethodPatch, path: coll + "/" + viaCLI, body: `{"body":"edited over REST"}`})
				if rep.status != http.StatusOK {
					t.Fatalf("PATCH: %d %.300s", rep.status, rep.body)
				}
				if got := knowShowRow(t, c, k, viaCLI).Body; got != "edited over REST" {
					t.Fatalf("CLI shows %q after the REST edit", got)
				}
			}

			// the same bad id on both surfaces
			for _, bad := range []string{"not-an-id", knowUnknownID(viaCLI)} {
				cli := restCLIErr(c.Lore(k.noun, "show", bad, "--json"))
				rep := srv.get(t, coll+"/"+bad)
				if cli == nil || rep.err == nil || cli.Code != rep.err.Code || cli.Message != rep.err.Message || cli.Hint != rep.err.Hint || cli.Exit != rep.err.Exit {
					t.Fatalf("show %s: CLI %+v / REST %d %+v", bad, cli, rep.status, rep.err)
				}
			}

			if k.archive {
				for _, verb := range []string{"archive", "unarchive"} {
					if rep := srv.post(t, coll+"/"+viaREST+"/"+verb, `{}`); rep.status != http.StatusOK {
						t.Fatalf("POST %s: %d %.300s", verb, rep.status, rep.body)
					}
				}
				if !knowListRows(t, c, k).has(viaREST) {
					t.Fatalf("archive + unarchive over REST lost the row from the CLI list")
				}
			}

			ask := srv.do(t, restReq{method: http.MethodDelete, path: coll + "/" + viaREST})
			cliAsk := restCLIErr(c.Lore(k.noun, "delete", viaREST, "--json"))
			if ask.status != http.StatusPreconditionRequired || ask.err == nil || cliAsk == nil || ask.err.Code != "E_NEEDS_CONFIRM" || cliAsk.Code != ask.err.Code || cliAsk.Message != ask.err.Message {
				t.Fatalf("delete without yes: REST %d %+v / CLI %+v", ask.status, ask.err, cliAsk)
			}
			if rep := srv.do(t, restReq{method: http.MethodDelete, path: coll + "/" + viaREST + "?yes=true"}); rep.status != http.StatusOK {
				t.Fatalf("DELETE ?yes=true: %d %.300s", rep.status, rep.body)
			}
			if knowListRows(t, c, k).has(viaREST) {
				t.Fatalf("the CLI list still holds the row REST deleted")
			}
			again := srv.do(t, restReq{method: http.MethodDelete, path: coll + "/" + viaREST + "?yes=true"})
			cliAgain := restCLIErr(c.Lore(k.noun, "delete", viaREST, "--yes", "--json"))
			if again.err == nil || cliAgain == nil || again.err.Code != "E_IN_TRASH" || cliAgain.Code != again.err.Code {
				t.Fatalf("second delete: REST %d %+v / CLI %+v", again.status, again.err, cliAgain)
			}
		})
	}
}

func TestCampaignKnowledge_RESTKindVerbs(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	c := rw.repo

	mem := campMustAdd(t, c, knowKindByNoun(t, "memory"), "", "a memory to invalidate")
	if rep := srv.post(t, "/v1/memories/"+mem+"/invalidate", `{}`); rep.status != http.StatusOK {
		t.Fatalf("invalidate: %d %.300s", rep.status, rep.body)
	}
	c.Lore("memory", "invalidate", mem).OK(t).Says(t, "already invalid since")
	move := srv.post(t, "/v1/memories/"+mem+"/move", `{"to":"repo"}`)
	cliMove := restCLIErr(c.Lore("memory", "move", mem, "--to", "repo", "--json"))
	if move.err == nil || cliMove == nil || move.err.Code != "E_INVALID_INPUT" || move.err.Message != cliMove.Message {
		t.Fatalf("move to its own store: REST %d %+v / CLI %+v", move.status, move.err, cliMove)
	}

	c.MustLore("actor", "register")
	hnd := restAddID(t, rw, "handoff", "add", "--for", "@person", "check the backfill")
	if rep := srv.post(t, "/v1/handoffs/"+hnd+"/ack", `{}`); rep.status != http.StatusOK {
		t.Fatalf("ack: %d %.300s", rep.status, rep.body)
	}
	if st := campStatusStr(t, c.Lore("handoff", "show", hnd, "--json").Stdout); st != "acked" {
		t.Fatalf("CLI shows status %q after the REST ack", st)
	}
	hl := c.Lore("handoff", "list", "--json")
	campSameDoc(t, "handoff list", hl.Stdout, hl.Code, srv.get(t, "/v1/handoffs"))

	ex := srv.post(t, "/v1/explains/junk/run", `{}`)
	cliEx := restCLIErr(c.Lore("explain", "junk", "--json"))
	if ex.err == nil || cliEx == nil || ex.err.Code != "E_INVALID_ID" || cliEx.Code != ex.err.Code || cliEx.Hint != ex.err.Hint {
		t.Fatalf("explain junk: REST %d %+v / CLI %+v", ex.status, ex.err, cliEx)
	}
	lint := c.Lore("lint", "run", "--json")
	campSameDoc(t, "lint", lint.Stdout, lint.Code, srv.post(t, "/v1/lints/run", `{}`))
	dir := c.Lore("directive", "show", "--json")
	campSameDoc(t, "directive show", dir.Stdout, dir.Code, srv.get(t, "/v1/directives/show"))
	why := srv.get(t, "/v1/why_context/show")
	cliWhy := restCLIErr(c.Lore("why-context", "show", "--json"))
	if why.err == nil || cliWhy == nil || why.err.Code != cliWhy.Code || why.err.Hint != cliWhy.Hint {
		t.Fatalf("why-context before render: REST %d %+v / CLI %+v", why.status, why.err, cliWhy)
	}
}

// campStatusStr reads data.status_str of a show --json document.
func campStatusStr(t *testing.T, out string) string {
	t.Helper()
	var s knowShow
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return s.Data.StatusStr
}
