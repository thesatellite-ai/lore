//go:build userscen

package userscen

// campaign_sync_cli_test.go — the sync area's 46 commands as a matrix,
// through the real binary:
//
//   - every command's --help carries an Examples block whose lines are lore
//     commands (a person learns the command from its help);
//   - bad input on every command that takes input: the exact E_ code, its
//     exit, a phrase of the message and a hint naming the next step, in
//     human mode and in the --json error envelope;
//   - ids: a malformed id is E_INVALID_ID (exit 20) and a well-formed id no
//     row has is E_NOT_FOUND (exit 27) on every id-taking command
//     (ops/internal/lookup, owner decision BATCH2 #11);
//   - reads: the --json of each read command is one JSON document, and
//     `lore api` answers the same document for the same op (REST parity);
//   - `lore migrate unlock` with no lock prints its sentence, never a raw
//     "line:" field name.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestCampaignSync_HelpHasExamples: `lore <cmd> --help` of every sync-area
// command exits 0 and shows at least one example that is a lore command.
func TestCampaignSync_HelpHasExamples(t *testing.T) {
	_, _, c := cliSolo(t)
	for _, path := range camSyncCommands {
		name := strings.Join(path, " ")
		r := c.Lore(append(append([]string{}, path...), "--help")...).OK(t)
		ex := cliBlock(r.Stdout, "Examples:")
		found := false
		for _, l := range ex {
			if strings.HasPrefix(strings.TrimSpace(l), "lore ") {
				found = true
			}
		}
		if !found {
			t.Errorf("lore %s --help: no example line (a lore command):\n%s", name, r.Stdout)
		}
	}
}

// TestCampaignSync_BadInputMatrix: every refusal of the sync area names
// its code, exits with that code's exit and says what to do next.
func TestCampaignSync_BadInputMatrix(t *testing.T) {
	_, _, c := cliSolo(t)
	const unknownTask = "tsk_01a1b7b892587de4895ee2ad4e285e1c" // well formed, no such row
	cases := []camSyncRefusal{
		{[]string{"commit", "show"}, "E_INVALID_INPUT", 20, "missing required field id"},
		{[]string{"row", "show", "nope"}, "E_INVALID_ID", 20, "is not a row id"},
		{[]string{"row", "show", unknownTask}, "E_NOT_FOUND", 27, unknownTask},
		{[]string{"row", "move", "nope", "--to", "repo"}, "E_INVALID_ID", 20, "is not a row id"},
		{[]string{"trash", "show", "nope"}, "E_INVALID_ID", 20, "is not a row id"},
		{[]string{"trash", "show", unknownTask}, "E_NOT_FOUND", 27, unknownTask},
		{[]string{"trash", "restore", "nope"}, "E_INVALID_ID", 20, "neither a row id nor a batch id"},
		{[]string{"trash", "restore", unknownTask}, "E_NOT_FOUND", 27, unknownTask},
		{[]string{"trash", "purge"}, "E_INVALID_INPUT", 20, "give exactly one of"},
		{[]string{"trash", "list", "--since", "garbage"}, "E_INVALID_INPUT", 20, "want a duration"},
		{[]string{"trash", "list", "--limit", "-1"}, "E_INVALID_INPUT", 20, "--limit must be 1 or more"},
		{[]string{"trash", "list", "--to", "nope"}, "E_NOT_FOUND", 27, "no store named nope"},
		{[]string{"sync", "resolve", "nope"}, "E_INVALID_INPUT", 20, "take"},
		{[]string{"sync", "peek", "nope"}, "E_INVALID_INPUT", 20, "nope"},
		{[]string{"sync", "promote", "nope"}, "E_INVALID_INPUT", 20, "branch"},
		{[]string{"sync", "merge-rows", "a", "b", "c"}, "E_INVALID_INPUT", 20, `unexpected value "b"`},
		{[]string{"sync", "purge"}, "E_INVALID_INPUT", 20, "--archived-before is required"},
		{[]string{"sync", "ci-merge"}, "E_INVALID_INPUT", 20, "base"},
		{[]string{"sync", "trash", "restore", "999"}, "E_NOT_FOUND", 27, "no local copy #999"},
		{[]string{"live", "purge"}, "E_INVALID_INPUT", 20, "ids"},
		{[]string{"live", "show", "nope"}, "E_NOT_FOUND", 27, "is not live"},
		{[]string{"live", "purge", "tsk_nope"}, "E_INVALID_INPUT", 20, "is not a full row id"},
		{[]string{"land", "run", "nope"}, "E_INVALID_ID", 20, "is not a row id"},
		{[]string{"land", "run", unknownTask}, "E_NOT_FOUND", 27, unknownTask},
		{[]string{"migrate", "enable", "nope"}, "E_INVALID_INPUT", 20, "is not a feature this lore knows"},
		{[]string{"migrate", "rollback"}, "E_NOT_FOUND", 27, "no migration to roll back"},
		{[]string{"store", "forget", "nope"}, "E_NOT_FOUND", 27, "no store named nope"},
		{[]string{"store", "remove", "nope"}, "E_NOT_FOUND", 27, "no store named nope"},
		{[]string{"store", "set-url", "nope", "https://github.com/acme/x"}, "E_NOT_FOUND", 27, "no store named nope"},
		{[]string{"store", "visibility", "nope", "internal"}, "E_NOT_FOUND", 27, "no store named nope"},
		{[]string{"store", "open", "nope"}, "E_NOT_FOUND", 27, "no store named nope"},
		{[]string{"move", "nope", "--to", "me"}, "E_NOT_FOUND", 27, "no store named me"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			camSyncRefuses(t, c, tc)
		})
	}
}

// TestCampaignSync_MigrateUnlockText: unlock with no lock prints the
// sentence itself (it printed "line: no migration lock on this machine",
// the generic field rendering of UnlockOut), and --json keeps both fields.
func TestCampaignSync_MigrateUnlockText(t *testing.T) {
	_, _, c := cliSolo(t)
	for i := 0; i < 2; i++ { // twice: the same answer, nothing changes
		r := c.MustLore("migrate", "unlock")
		if strings.TrimSpace(r.Stdout) != "no migration lock on this machine" {
			t.Fatalf("migrate unlock (run %d) prints the sentence alone:\n%q", i+1, r.Stdout)
		}
	}
	v := th.DecodeJSON[struct {
		Released *bool   `json:"released"`
		Line     *string `json:"line"`
	}](t, c.MustLore("migrate", "unlock", "--json"))
	if v.Released == nil || *v.Released || v.Line == nil || *v.Line == "" {
		t.Fatalf("migrate unlock --json: released=false and the line: %+v", v)
	}
}

// camSyncReads are read commands whose CLI --json and REST answer must be
// the same document, with the route `lore api` serves for each
// (docs/reference/openapi.json).
var camSyncReads = []struct {
	cli          []string
	method, path string
	body         string
}{
	{[]string{"trash", "list"}, "GET", "/v1/trashes", ""},
	{[]string{"trash", "stats"}, "POST", "/v1/trashes/stats", "{}"},
	{[]string{"store", "list"}, "GET", "/v1/stores", ""},
	{[]string{"live", "show"}, "GET", "/v1/live", ""},
	{[]string{"live", "log"}, "POST", "/v1/live/log", "{}"},
	{[]string{"migrate", "status"}, "POST", "/v1/migrate/status", "{}"},
	{[]string{"sync", "conflicts"}, "POST", "/v1/syncs/conflicts", "{}"},
}

// TestCampaignSync_ReadsJSONAndREST: each read's --json is one JSON object
// (twice the same), and `lore api` answers the same object.
func TestCampaignSync_ReadsJSONAndREST(t *testing.T) {
	tw := newTrashWorld(t, true)
	c := tw.c
	for _, s := range trashStores {
		id := trashID(t, c.MustLore("memory", "add", "note in "+s+" — ünï 🚀", "--to", s, "--json"))
		c.MustLore("memory", "delete", id, "--yes")
	}
	c.MustLore("task", "add", "a live task so the live ref exists", "--tasklist", tw.tasklists[trashStoreRepo])
	api := restStart(t, c)
	for _, rd := range camSyncReads {
		name := strings.Join(rd.cli, " ")
		cli := c.MustLore(append(append([]string{}, rd.cli...), "--json")...)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(cli.Stdout), &doc); err != nil || len(doc) == 0 {
			t.Errorf("lore %s --json: one JSON object: %v\n%s", name, err, cli.Stdout)
			continue
		}
		again := c.MustLore(append(append([]string{}, rd.cli...), "--json")...)
		if !restJSONEqual([]byte(cli.Stdout), []byte(again.Stdout)) {
			t.Errorf("lore %s --json twice: the same document\n%s\n%s", name, cli.Stdout, again.Stdout)
		}
		rep := api.do(t, restReq{method: rd.method, path: rd.path, body: rd.body})
		if rep.status != 200 {
			t.Errorf("%s %s: status %d: %s", rd.method, rd.path, rep.status, rep.body)
			continue
		}
		if !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
			t.Errorf("lore %s --json vs %s %s differ:\ncli:  %s\nrest: %s", name, rd.method, rd.path, cli.Stdout, rep.body)
		}
	}
	// The trash read the three stores' rows.
	if l := trashList(t, c); l.Count != len(trashStores) {
		t.Fatalf("trash list: %d rows, want one per store: %+v", l.Count, l)
	}
}
