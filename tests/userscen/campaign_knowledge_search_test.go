//go:build userscen

package userscen

// Knowledge campaign: search with the queries people really type, on every
// kind. Words, phrases and prefixes find rows; a query the full-text
// syntax refuses (a lone quote, a dangling AND, a stray "(" or "*", a
// colon that reads as a column filter) is E_INVALID_INPUT with the reason
// and a hint showing the syntax, the same on the CLI and over REST (it was
// "sqlite cache: search" with no reason and no hint); --limit outside
// 1..1000 is refused; an empty query is refused.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// campBadQueries are queries the full-text syntax refuses.
var campBadQueries = []string{`"`, `'`, `*`, `(`, `AND`, `a OR`, `NEAR(`, `body:`, `time:3pm`}

func TestCampaignKnowledge_SearchQueriesEveryKind(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			id := campMustAdd(t, c, k, "Connection pool", "retry the connection pool twice")
			for _, q := range []string{"connection", `"connection pool"`, "conn*", "retry AND pool", "CONNECTION"} {
				if r := c.Lore(k.noun, "search", q, "--json").OK(t); !strings.Contains(r.Stdout, id) {
					t.Fatalf("search %q did not find %s:\n%s", q, id, r)
				}
			}
			for _, q := range campBadQueries {
				r := c.Lore(k.noun, "search", q).Fails(t, 20, "E_INVALID_INPUT", "not a valid search query")
				if !strings.Contains(r.Stderr, "hint:") || strings.Contains(r.Stderr, "sqlite cache") {
					t.Fatalf("search %q: want the reason and a hint, no cache internals:\n%s", q, r)
				}
				if e := knowJSONErr(t, c.Lore(k.noun, "search", q, "--json")); e.Error.Hint == "" {
					t.Fatalf("search %q --json: no hint: %+v", q, e.Error)
				}
			}
			c.Lore(k.noun, "search", "   ").Fails(t, 20, "E_INVALID_INPUT", "empty query")
			for _, l := range []string{"-1", "1001"} {
				c.Lore(k.noun, "search", "pool", "--limit="+l).Fails(t, 20, "E_INVALID_INPUT", "--limit")
			}
			c.Lore(k.noun, "search", "pool", "--limit=abc").Fails(t, 20, "E_INVALID_INPUT", "integer")
		})
	}
}

func TestCampaignKnowledge_SearchQueriesREST(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	campMustAdd(t, rw.repo, knowKindByNoun(t, "memory"), "", "retry the connection pool twice")
	for _, q := range campBadQueries {
		cli := restCLIErr(rw.repo.Lore("memory", "search", q, "--json"))
		qj, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		rep := srv.post(t, "/v1/memories/search", `{"query":`+string(qj)+`}`)
		if cli == nil || rep.err == nil || rep.status != http.StatusBadRequest || cli.Code != rep.err.Code || cli.Message != rep.err.Message || cli.Hint != rep.err.Hint {
			t.Fatalf("search %q: CLI %+v / REST %d %+v", q, cli, rep.status, rep.err)
		}
	}
}
