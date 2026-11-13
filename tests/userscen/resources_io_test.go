//go:build userscen

// Resource import and export as a person uses them: export → import round
// trips in yaml, json and csv; a re-import of the same file keeps rows
// (idempotent), --upsert-by name, --overwrite; all-or-nothing refusals
// that name the bad row AND why it is bad (E_RESOURCE_BATCH); --skip-errors
// and --dry-run; malformed YAML with the line the parser stopped at;
// --json shapes of the import report; a 200 + 100 row import in one batch.
package userscen

import (
	"fmt"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// importJSON is `lore resource import --json`.
type importJSON struct {
	Source  string `json:"source"`
	Batch   string `json:"batch"`
	DryRun  bool   `json:"dry_run"`
	Created int    `json:"created"`
	Updated int    `json:"updated"`
	Kept    int    `json:"kept"`
	Errors  int    `json:"errors"`
	Rows    []struct {
		Action string `json:"action"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		ID     string `json:"id"`
		Error  string `json:"error"`
	} `json:"rows"`
}

// An all-or-nothing import refusal says which row AND why, so a person
// can fix the file without re-running with --skip-errors to learn the
// reason; nothing is written. (Bug found by this suite: the reason was
// dropped from the printed message.)
func TestResourcesImportRefusalNamesRowAndReason(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "taken", "--set", "provider=aws")
	for _, tc := range []struct {
		name, body string
		say        []string
	}{
		{"value not in the closed set", "resources:\n  - kind: host\n    name: a1\n    fields:\n      provider: aws\n  - kind: host\n    name: a2\n    fields:\n      provider: gcp\n",
			[]string{"host a2", `provider "gcp" is not one of hetzner, aws`, "nothing written", "lore resource kind show --id host"}},
		{"unknown field", "resources:\n  - kind: host\n    name: a1\n    fields:\n      provider: aws\n      bogus: 1\n",
			[]string{"host a1", "unknown field bogus", "nothing written"}},
		{"unknown kind", "resources:\n  - kind: nokind\n    name: a1\n",
			[]string{"nokind a1", `no resource kind "nokind"`, "lore resource kinds"}},
		{"name taken by a row added by hand", "resources:\n  - kind: host\n    name: taken\n    fields:\n      provider: aws\n",
			[]string{"host taken already exists in repo", "--upsert-by name"}},
		{"required field missing", "resources:\n  - kind: host\n    name: a3\n",
			[]string{"host a3", "required field provider is missing"}},
		{"bad name", "resources:\n  - kind: host\n    name: Bad Name\n    fields:\n      provider: aws\n",
			[]string{"Bad Name", "name format"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := resWriteFile(t, c, strings.ReplaceAll(tc.name, " ", "_")+".yaml", tc.body)
			r := c.Lore("resource", "import", f, "--yes")
			r.Fails(t, resExitRefused, append([]string{"E_RESOURCE_BATCH"}, tc.say...)...)
			e := resErrOf(t, c.Lore("resource", "import", f, "--yes", "--json"))
			if e.Error.Code != "E_RESOURCE_BATCH" || e.Error.Exit != resExitRefused || !strings.Contains(e.Error.Message, tc.say[0]) {
				t.Fatalf("--json refusal names the reason: %+v", e)
			}
			if n := resList(t, c, "host").Count; n != 1 {
				t.Fatalf("all or nothing: %d hosts after a refused import, want 1", n)
			}
		})
	}
}

// --skip-errors writes the good rows and lists the bad ones with their
// reason; --dry-run lists the plan and writes nothing.
func TestResourcesImportSkipErrorsAndDryRun(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	f := resWriteFile(t, c, "mixed.yaml", "resources:\n  - kind: host\n    name: a1\n    fields:\n      provider: aws\n  - kind: host\n    name: a2\n    fields:\n      provider: gcp\n")
	dry := th.DecodeJSON[importJSON](t, c.Lore("resource", "import", f, "--dry-run", "--json"))
	if !dry.DryRun || dry.Errors != 1 || len(dry.Rows) != 2 || dry.Rows[0].Action != "create" || dry.Rows[1].Action != "error" || dry.Batch != "" {
		t.Fatalf("dry run report = %+v", dry)
	}
	if n := resList(t, c, "host").Count; n != 0 {
		t.Fatalf("--dry-run wrote %d rows", n)
	}
	r := c.Lore("resource", "import", f, "--skip-errors", "--yes").OK(t)
	r.Says(t, "create  host a1", "error  host a2", `provider "gcp" is not one of`, "imported 1 row (1 created, 0 updated)", "1 error (skipped)")
	if l := resList(t, c, "host"); l.Count != 1 || l.Data[0].Name != "a1" {
		t.Fatalf("--skip-errors wrote %+v", l)
	}
}

// An import file is checked as a whole: a malformed YAML file is refused
// with E_INVALID_INPUT, the parser's line and problem, and nothing written.
func TestResourcesImportBadYAML(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	for _, tc := range []struct {
		name, body string
		say        []string
	}{
		{"tab indentation", "resources:\n  - kind: host\n    name: a1\n    fields:\n      provider: aws\n\tip: 1\n", []string{"line 5", "tab character"}},
		{"unclosed flow map", "resources:\n  - kind: host\n    name: a1\n    fields: {provider: aws\n", []string{"line 3", "did not find expected ',' or '}'"}},
		{"top level list", "- kind: host\n  name: a2\n", []string{"line 1", "expected a mapping"}},
		{"stray list item in a map", "resources:\n  - kind: host\n    name: a1\n    fields:\n      provider: aws\n    - broken\n", []string{"the YAML is not valid", "did not find expected key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := resWriteFile(t, c, strings.ReplaceAll(tc.name, " ", "_")+".yaml", tc.body)
			c.Lore("resource", "import", f, "--yes").Fails(t, resExitInvalid, append([]string{"E_INVALID_INPUT", "resource import"}, tc.say...)...)
			if n := resList(t, c, "host").Count; n != 0 {
				t.Fatalf("a malformed file wrote %d rows", n)
			}
		})
	}
	// An empty file is nothing to do, not an error.
	c.Lore("resource", "import", resWriteFile(t, c, "empty.yaml", ""), "--yes").OK(t).Says(t, "nothing written")
	c.Lore("resource", "import", c.A.Home+"/imports/missing.yaml", "--yes").Fails(t, resExitInvalid)
	c.Lore("resource", "import", resWriteFile(t, c, "h.txt", "x"), "--yes").Fails(t, resExitInvalid, "csv, json or yaml")
}

// export → import is the inverse: every format round-trips the rows of a
// kind (refs by name) into a second repo, and re-importing the same file
// there matches every row by its import key (PROVENANCE 11.4: an
// unchanged row is updated in place): no duplicates, no new history.
func TestResourcesExportImportRoundTrip(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, format := range []string{"yaml", "json", "csv"} {
		t.Run(format, func(t *testing.T) {
			w := newWorld(t)
			a := w.Human("alice", "alice@example.com")
			a.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}})
			src := a.Clone(w.Remote("github.com/octo-personal/src"), "src")
			src.MustLore("init", "--non-interactive", "--name=src")
			dst := a.Clone(w.Remote("github.com/octo-personal/dst"), "dst")
			dst.MustLore("init", "--non-interactive", "--name=dst")
			resHostService(t, src)
			resHostService(t, dst)
			src.MustLore("resource", "add", "host", "h1", "--set", "provider=aws", "--set", "ip=10.0.0.1", "--set", "cores=8")
			src.MustLore("resource", "add", "host", "h2", "--set", "provider=hetzner")
			src.MustLore("resource", "add", "service", "api", "--set", "runs_on=h1", "--set", "port=8080")
			for _, kind := range []string{"host", "service"} {
				out := src.MustLore("resource", "export", "--kind", kind, "--format", format).Stdout
				f := resWriteFile(t, dst, kind+"."+format, out)
				args := []string{"resource", "import", f, "--yes", "--json"}
				if format == "csv" {
					args = append(args, "--kind", kind)
				}
				rep := th.DecodeJSON[importJSON](t, dst.Lore(args...))
				if rep.Source != format || rep.Errors != 0 || rep.Created == 0 || !strings.HasPrefix(rep.Batch, "bat_") {
					t.Fatalf("%s import of %s = %+v", format, kind, rep)
				}
				hist := th.DecodeJSON[historyJSON](t, dst.Lore("resource", "history", rep.Rows[0].ID, "--json")).Count
				again := th.DecodeJSON[importJSON](t, dst.Lore(args...))
				if again.Created != 0 || again.Updated != rep.Created || again.Errors != 0 {
					t.Fatalf("re-import of the same %s file updates in place, creates nothing: %+v", format, again)
				}
				if h := th.DecodeJSON[historyJSON](t, dst.Lore("resource", "history", rep.Rows[0].ID, "--json")).Count; h != hist {
					t.Fatalf("an unchanged re-import adds no field history: %d → %d", hist, h)
				}
			}
			for _, name := range []string{"h1", "h2", "api"} {
				s, d := resShow(t, src, name), resShow(t, dst, name)
				if fmt.Sprint(s.Values) != fmt.Sprint(d.Values) || s.Kind != d.Kind {
					t.Fatalf("%s round trip: %+v → %+v", name, s, d)
				}
				if s.ID == d.ID {
					t.Fatalf("an import into another repo makes new ids: %s", s.ID)
				}
			}
			if n := resList(t, dst, "host").Count; n != 2 {
				t.Fatalf("hosts after two imports = %d, want 2", n)
			}
		})
	}
}

// Re-import after an edit in lore keeps the edit (default), --overwrite
// replaces it; --upsert-by name updates a row added by hand.
func TestResourcesReimportEditedAndUpsert(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	f := resWriteFile(t, c, "hosts.yaml", "resources:\n  - kind: host\n    name: a1\n    fields:\n      provider: aws\n")
	c.MustLore("resource", "import", f, "--yes")
	c.MustLore("resource", "edit", "a1", "--set", "provider=hetzner")
	kept := th.DecodeJSON[importJSON](t, c.Lore("resource", "import", f, "--yes", "--json"))
	if kept.Kept != 1 || resShow(t, c, "a1").Values["provider"] != "hetzner" {
		t.Fatalf("a re-import keeps a row edited in lore since: %+v", kept)
	}
	over := th.DecodeJSON[importJSON](t, c.Lore("resource", "import", f, "--yes", "--overwrite", "--json"))
	if over.Updated != 1 || resShow(t, c, "a1").Values["provider"] != "aws" {
		t.Fatalf("--overwrite replaces the edit: %+v", over)
	}
	c.MustLore("resource", "add", "host", "byhand", "--set", "provider=aws")
	up := resWriteFile(t, c, "up.yaml", "resources:\n  - kind: host\n    name: byhand\n    fields:\n      provider: hetzner\n      cores: 4\n")
	c.Lore("resource", "import", up, "--yes").Fails(t, resExitRefused, "E_RESOURCE_BATCH", "already exists", "--upsert-by name")
	c.MustLore("resource", "import", up, "--yes", "--upsert-by", "name")
	if v := resShow(t, c, "byhand").Values; v["provider"] != "hetzner" || v["cores"] != "4" {
		t.Fatalf("--upsert-by name updates the row: %v", v)
	}
	c.Lore("resource", "import", up, "--yes", "--upsert-by", "id").Fails(t, resExitInvalid)
}

// A big import (200 hosts from csv, 100 services with refs to them from
// yaml) lands in one batch each; list sees every row, and a second run
// creates nothing. (Sizes keep the run near a minute: an import costs
// about 0.15 s per row, 0.4 s per row with refs, see bugs.md.)
func TestResourcesImportHundredsOfRows(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	const n, nSvc = 200, 100
	var csv strings.Builder
	csv.WriteString("name,provider,ip\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&csv, "h%03d,%s,10.0.%d.%d\n", i, []string{"aws", "hetzner"}[i%2], i/250, i%250)
	}
	f := resWriteFile(t, c, "hosts.csv", csv.String())
	rep := th.DecodeJSON[importJSON](t, c.Lore("resource", "import", f, "--kind", "host", "--yes", "--json"))
	if rep.Created != n || rep.Errors != 0 || len(rep.Rows) != n {
		t.Fatalf("200-row csv import = created %d errors %d rows %d", rep.Created, rep.Errors, len(rep.Rows))
	}
	var y strings.Builder
	y.WriteString("resources:\n")
	for i := 0; i < nSvc; i++ {
		fmt.Fprintf(&y, "  - kind: service\n    name: s%03d\n    fields:\n      runs_on: h%03d\n      port: %d\n", i, i, 1000+i)
	}
	sf := resWriteFile(t, c, "services.yaml", y.String())
	srep := th.DecodeJSON[importJSON](t, c.Lore("resource", "import", sf, "--yes", "--json"))
	if srep.Created != nSvc || srep.Errors != 0 {
		t.Fatalf("100-row yaml import = %+v", srep.Created)
	}
	if l := resList(t, c, "service", "--where", "runs_on=h050"); l.Count != 1 || l.Data[0].Name != "s050" {
		t.Fatalf("a ref filter over the imported rows = %+v", l)
	}
	if l := resList(t, c, "host"); l.Count != n {
		t.Fatalf("host list count = %d, want %d", l.Count, n)
	}
	again := th.DecodeJSON[importJSON](t, c.Lore("resource", "import", f, "--kind", "host", "--yes", "--json"))
	if again.Created != 0 || again.Updated+again.Kept != n {
		t.Fatalf("re-import of 200 rows: %+v", again.Created)
	}
}
