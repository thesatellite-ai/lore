//go:build journeys && !windows

package journeys

// agents_test.go — persona 5, coding agents: a developer whose Claude Code
// and Codex sessions write lore notes in the same repo at the same time as
// the developer does, through the CLI and through `lore api`.
//
//	day 1   init, first note; a Claude Code session and a Codex session write
//	day 2   new sessions of both tools (same actors); LORE_HUMAN=1 in a tool
//	        terminal keeps the write the person's
//	day 3   person + Claude + Codex write at the same instant, many times
//	day 4   a Claude session is killed mid-write, at every git step and at
//	        random moments
//	day 5   `lore api` started from a Claude Code session, REST and CLI
//	        writes interleaved, each side sees the other's at once
//	day 6   undo: an agent deletes the person's note (restore), an agent
//	        edits it (edit back), a purge regretted
//	week 2  the setup page, closed halfway while the tools keep writing,
//	        finished the next day (TestJourneyAgentsSetup)
//	week 3  the setup removed and re-added; each tool keeps one actor
//
// Parts (each its own World, run in parallel): TestJourneyAgents (days 1-3,
// 5, 6), TestJourneyAgentsKilled (day 4), TestJourneyAgentsSetup (weeks 2-3).
//
// Agents are simulated the way lore detects them (logic/agentdetect): the
// tool's own environment markers. Every agent run first blanks every
// marker lore knows (the test may itself run inside a coding tool, whose
// variables the harness passes through) and turns detection back on.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The persona (placeholders only).
const (
	jAgentsName  = "Ada Agents"
	jAgentsEmail = "ada.agents@example.test"
	jAgentsRepo  = "github.com/agents-co/app"
)

// Tools as lore names them (actors.label, the "via <tool>" display).
const (
	jAgentsClaude = "claude_code"
	jAgentsCodex  = "codex"
)

// jAgentsTool is how one coding tool's session looks to lore.
type jAgentsTool struct {
	slug string
	env  func(session string) []string
}

var (
	jClaude = jAgentsTool{slug: jAgentsClaude, env: func(s string) []string {
		return []string{"CLAUDE_CODE_CHILD_SESSION=1", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=" + s}
	}}
	jCodex = jAgentsTool{slug: jAgentsCodex, env: func(s string) []string {
		return []string{"CODEX_THREAD_ID=" + s}
	}}
)

// jAgentsBlank blanks every variable agent detection reads and turns
// detection on (the harness base env has LORE_HUMAN=1, LORE_NO_DETECT=1).
func jAgentsBlank() []string {
	out := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0"}
	for _, n := range agentdetect.AllowedNames(nil) {
		out = append(out, n+"=")
	}
	return out
}

// jAgentsAs is the clone as seen from a tool's session ("" tool = the
// person's own terminal, LORE_HUMAN=1 as the harness sets it).
func jAgentsAs(c *teamharness.Clone, tool *jAgentsTool, session string) *teamharness.Clone {
	if tool == nil {
		return c
	}
	return c.With(append(jAgentsBlank(), tool.env(session)...)...)
}

// jAgentsRun runs lore in the clone from a tool's session and records the
// result for the step's readable-output invariant.
func jAgentsRun(m *Machine, c *teamharness.Clone, tool *jAgentsTool, session string, args ...string) teamharness.Result {
	m.w.T.Helper()
	r := jAgentsAs(c, tool, session).Lore(args...)
	m.results = append(m.results, r)
	return r
}

// jAgentsRow is the part of a memory a person reads back.
type jAgentsRow struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

type jAgentsActor struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}

type jAgentsWho struct {
	Data struct {
		Identity struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			ActorID string `json:"actor_id"`
		} `json:"identity"`
	} `json:"data"`
}

// jAgentsAdd writes a memory and returns its id.
func jAgentsAdd(t *testing.T, m *Machine, c *teamharness.Clone, tool *jAgentsTool, session, body string) string {
	t.Helper()
	r := jAgentsRun(m, c, tool, session, "memory", "add", "--body", body, "--json")
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		ID string `json:"id"`
	}
	r.OK(t)
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("memory add --json: %v\n%s", err, r)
	}
	id := out.Data.ID
	if id == "" {
		id = out.ID
	}
	if !strings.HasPrefix(id, "mem_") {
		t.Fatalf("memory add printed no id: %s", r)
	}
	return id
}

// jAgentsRows is every memory of the clone by body, read by the person.
func jAgentsRows(t *testing.T, m *Machine, c *teamharness.Clone) map[string]jAgentsRow {
	t.Helper()
	r := m.LoreIn(c, "memory", "list", "--json")
	out := teamharness.DecodeJSON[struct {
		Count int          `json:"count"`
		Data  []jAgentsRow `json:"data"`
	}](t, r)
	rows := map[string]jAgentsRow{}
	for _, row := range out.Data {
		if have, dup := rows[row.Body]; dup {
			t.Errorf("memory %q listed twice (%s, %s)", row.Body, have.ID, row.ID)
		}
		rows[row.Body] = row
	}
	if out.Count != len(out.Data) {
		t.Errorf("memory list says count %d but lists %d", out.Count, len(out.Data))
	}
	return rows
}

// jAgentsActors is the actor list by id.
func jAgentsActors(t *testing.T, m *Machine, c *teamharness.Clone) map[string]jAgentsActor {
	t.Helper()
	out := teamharness.DecodeJSON[struct {
		Data []jAgentsActor `json:"data"`
	}](t, m.LoreIn(c, "actor", "list", "--json"))
	by := map[string]jAgentsActor{}
	for _, a := range out.Data {
		by[a.ID] = a
	}
	return by
}

// jAgentsWhoami is whoami from a tool's session.
func jAgentsWhoami(t *testing.T, m *Machine, c *teamharness.Clone, tool *jAgentsTool, session string) jAgentsWho {
	t.Helper()
	return teamharness.DecodeJSON[jAgentsWho](t, jAgentsRun(m, c, tool, session, "whoami", "--json"))
}

// jAgentsDataFiles checks the repo store's files are whole: every row file
// parses, and nothing half-written (a temp file, an empty file) is left.
func jAgentsDataFiles(t *testing.T, c *teamharness.Clone) (memories int) {
	t.Helper()
	root := filepath.Join(c.Dir, ".lore", "data")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if !strings.HasSuffix(p, ".json") {
			t.Errorf("leftover file in the repo store: .lore/data/%s", rel)
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		var v map[string]any
		if jerr := json.Unmarshal(b, &v); jerr != nil || len(v) == 0 {
			t.Errorf("half-written row file .lore/data/%s (%d bytes): %v", rel, len(b), jerr)
		}
		if strings.HasPrefix(rel, "memories"+string(filepath.Separator)) {
			memories++
		}
		return nil
	})
	if err != nil {
		t.Errorf("read the repo store's files: %v", err)
	}
	return memories
}

// jAgentsIdentity writes the person's identity as setup leaves it.
func jAgentsIdentity(t *testing.T, m *Machine) {
	t.Helper()
	if err := os.MkdirAll(m.LorePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("identity:\n    key: human:%s\n    name: %s\n", jAgentsEmail, jAgentsName)
	if err := os.WriteFile(m.LorePath("config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// jAgentsDay1 is the journey's first day, which every part starts from:
// init, Ada's first note, then a Claude Code and a Codex note. It returns
// the World, the laptop, the repo and the three actors.
func jAgentsDay1(t *testing.T) (w *World, m *Machine, app *teamharness.Clone, person, claudeActor, codexActor string) {
	w = NewWorld(t)
	ada := w.Persona(jAgentsName, jAgentsEmail)
	m = ada.Laptop()
	jAgentsIdentity(t, m)
	app = m.Clone(w.Remote(jAgentsRepo), "app")

	via := func(tool string) string { return jAgentsName + " via " + tool }

	w.Step("day1/init-and-first-note", func() {
		m.LoreIn(app, "init").OK(t)
		id := jAgentsAdd(t, m, app, nil, "", "run the linter before every push")
		who := jAgentsWhoami(t, m, app, nil, "")
		person = who.Data.Identity.ActorID
		if who.Data.Identity.Kind != "human" || who.Data.Identity.Name != jAgentsName {
			t.Errorf("whoami at the person's terminal: %+v, want %s (human)", who.Data.Identity, jAgentsName)
		}
		if row := jAgentsRows(t, m, app)["run the linter before every push"]; row.ID != id || row.CreatedBy != person {
			t.Errorf("first note %+v, want id %s by %s", row, id, person)
		}
	})

	w.Step("day1/claude-and-codex-write", func() {
		jAgentsAdd(t, m, app, &jClaude, "cc-1", "claude: tests live next to the code")
		jAgentsAdd(t, m, app, &jCodex, "cx-1", "codex: use table-driven tests")
		rows := jAgentsRows(t, m, app)
		claudeActor = rows["claude: tests live next to the code"].CreatedBy
		codexActor = rows["codex: use table-driven tests"].CreatedBy
		for tool, id := range map[string]string{jAgentsClaude: claudeActor, jAgentsCodex: codexActor} {
			if id == "" || id == person {
				t.Errorf("the %s note is stamped %q, want the %s agent (not the person %s)", tool, id, tool, person)
			}
		}
		if claudeActor == codexActor {
			t.Errorf("claude and codex notes share actor %s", claudeActor)
		}
		actors := jAgentsActors(t, m, app)
		want := map[string]jAgentsActor{
			person:      {ID: person, Kind: "human", DisplayName: jAgentsName},
			claudeActor: {ID: claudeActor, Kind: "agent", DisplayName: via(jAgentsClaude)},
			codexActor:  {ID: codexActor, Kind: "agent", DisplayName: via(jAgentsCodex)},
		}
		for id, wa := range want {
			if got := actors[id]; got != wa {
				t.Errorf("actor %s: %+v, want %+v", id, got, wa)
			}
		}
		if len(actors) != len(want) {
			t.Errorf("actor list has %d actors, want %d: %+v", len(actors), len(want), actors)
		}
		for tool, wantID := range map[*jAgentsTool]string{&jClaude: claudeActor, &jCodex: codexActor} {
			who := jAgentsWhoami(t, m, app, tool, "s-who").Data.Identity
			if who.ActorID != wantID || who.Kind != "agent" || who.Name != via(tool.slug) {
				t.Errorf("whoami in a %s session: %+v, want %s %q (agent)", tool.slug, who, wantID, via(tool.slug))
			}
		}
	})
	return w, m, app, person, claudeActor, codexActor
}

func TestJourneyAgents(t *testing.T) {
	t.Parallel()
	w, m, app, person, claudeActor, codexActor := jAgentsDay1(t)

	w.AdvanceDays(1)
	w.Step("day2/new-sessions-same-actors", func() {
		jAgentsAdd(t, m, app, &jClaude, "cc-2", "claude: second session")
		jAgentsAdd(t, m, app, &jCodex, "cx-2", "codex: second thread")
		rows := jAgentsRows(t, m, app)
		if got := rows["claude: second session"].CreatedBy; got != claudeActor {
			t.Errorf("a new Claude Code session writes as %s, want the same actor %s", got, claudeActor)
		}
		if got := rows["codex: second thread"].CreatedBy; got != codexActor {
			t.Errorf("a new Codex thread writes as %s, want the same actor %s", got, codexActor)
		}
		if n := len(jAgentsActors(t, m, app)); n != 3 {
			t.Errorf("new sessions added actors: %d, want 3", n)
		}
	})

	w.Step("day2/detection-off-with-LORE_HUMAN", func() {
		// The person types in Claude Code's terminal pane and says so.
		human := app.With(append(append(jAgentsBlank(), jClaude.env("cc-2")...), "LORE_HUMAN=1")...)
		r := human.Lore("memory", "add", "--body", "typed by hand in the tool's terminal")
		m.results = append(m.results, r)
		r.OK(t)
		if got := jAgentsRows(t, m, app)["typed by hand in the tool's terminal"].CreatedBy; got != person {
			t.Errorf("LORE_HUMAN=1 inside a Claude Code session wrote as %s, want the person %s", got, person)
		}
		who := teamharness.DecodeJSON[jAgentsWho](t, human.Lore("whoami", "--json")).Data.Identity
		if who.ActorID != person || who.Kind != "human" {
			t.Errorf("whoami with LORE_HUMAN=1 in a tool session: %+v, want the person", who)
		}
	})

	w.AdvanceDays(1)
	w.Step("day3/person-claude-codex-at-once", func() {
		const each = 6
		writers := []struct {
			tool *jAgentsTool
			name string
		}{{nil, "ada"}, {&jClaude, "claude"}, {&jCodex, "codex"}}
		var wg sync.WaitGroup
		res := make([][]teamharness.Result, len(writers))
		for i, wr := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := range each {
					res[i] = append(res[i], jAgentsAs(app, wr.tool, "par").Lore("memory", "add", "--body", fmt.Sprintf("at once %s %d", wr.name, k)))
				}
			}()
		}
		wg.Wait()
		for i := range res {
			for _, r := range res[i] {
				m.results = append(m.results, r)
				if r.Code != 0 {
					t.Errorf("a write at the same instant failed: %s", r)
				}
			}
		}
		rows := jAgentsRows(t, m, app)
		wantBy := map[string]string{"ada": person, "claude": claudeActor, "codex": codexActor}
		for _, wr := range writers {
			for k := range each {
				body := fmt.Sprintf("at once %s %d", wr.name, k)
				if row, ok := rows[body]; !ok {
					t.Errorf("%q was lost", body)
				} else if row.CreatedBy != wantBy[wr.name] {
					t.Errorf("%q stamped %s, want %s", body, row.CreatedBy, wantBy[wr.name])
				}
			}
		}
		if files := jAgentsDataFiles(t, app); files != len(rows) {
			t.Errorf("%d memory files on disk, memory list shows %d", files, len(rows))
		}
		if n := len(jAgentsActors(t, m, app)); n != 3 {
			t.Errorf("writing at once made %d actors, want 3", n)
		}
	})

	w.AdvanceDays(2) // day 4 is TestJourneyAgentsKilled
	var api *jAgentsAPI
	w.Step("day5/api-started-from-a-claude-session", func() {
		// Ada starts `lore api` from a Claude Code pane for a tool that
		// cannot shell out. The server is hers: a request with no
		// X-Lore-Agent is her write (REST 9.7), a request naming an agent
		// is still stamped as her (the header is attribution and policy,
		// never a different actor), whatever tool started the server.
		api = jAgentsStartAPI(t, app, &jClaude, "cc-api")
		plain := api.add("", "api: written by a script")
		named := api.add(jAgentsCodex, "api: written by codex over REST")
		rows := jAgentsRows(t, m, app)
		for body, id := range map[string]string{"api: written by a script": plain, "api: written by codex over REST": named} {
			if rows[body].ID != id {
				t.Errorf("REST note %q: the CLI lists %+v, want id %s", body, rows[body], id)
			}
			if rows[body].CreatedBy != person {
				t.Errorf("REST note %q is stamped %s, want the person %s (not the tool that started lore api: %s)", body, rows[body].CreatedBy, person, claudeActor)
			}
		}
		if n := len(jAgentsActors(t, m, app)); n != 3 {
			t.Errorf("REST writes made %d actors, want 3", n)
		}
	})

	w.Step("day5/cli-and-rest-see-each-other", func() {
		id := jAgentsAdd(t, m, app, &jCodex, "cx-5", "cli: codex while the api runs")
		if got := api.get(id); got.Body != "cli: codex while the api runs" || got.CreatedBy != codexActor {
			t.Errorf("REST reads the CLI's new note as %+v, want codex's body by %s", got, codexActor)
		}
		jAgentsRun(m, app, &jCodex, "cx-5", "memory", "edit", id, "--body", "cli: codex edited").OK(t)
		if got := api.get(id).Body; got != "cli: codex edited" {
			t.Errorf("REST still reads %q after the CLI edit", got)
		}
		api.call(http.MethodPatch, "/v1/memories/"+id, "", `{"body":"rest: edited over REST"}`, nil)
		if got := jAgentsRows(t, m, app)["rest: edited over REST"]; got.ID != id || got.UpdatedBy != person {
			t.Errorf("the CLI reads the REST edit as %+v, want id %s updated by %s", got, id, person)
		}
	})

	w.Step("day5/rest-and-cli-interleaved", func() {
		const each = 6
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for k := range each {
				api.add("", fmt.Sprintf("mixed rest %d", k))
			}
		}()
		var cli []teamharness.Result
		go func() {
			defer wg.Done()
			for k := range each {
				cli = append(cli, jAgentsAs(app, &jClaude, "cc-mix").Lore("memory", "add", "--body", fmt.Sprintf("mixed cli %d", k)))
			}
		}()
		wg.Wait()
		for _, r := range cli {
			m.results = append(m.results, r)
			if r.Code != 0 {
				t.Errorf("a CLI write beside REST writes failed: %s", r)
			}
		}
		rows, seen := jAgentsRows(t, m, app), api.bodies()
		for k := range each {
			for body, by := range map[string]string{fmt.Sprintf("mixed rest %d", k): person, fmt.Sprintf("mixed cli %d", k): claudeActor} {
				if rows[body].CreatedBy != by {
					t.Errorf("CLI reads %q as %+v, want it by %s", body, rows[body], by)
				}
				if !seen[body] {
					t.Errorf("REST does not list %q", body)
				}
			}
		}
		if len(seen) != len(rows) {
			t.Errorf("REST lists %d memories, the CLI %d", len(seen), len(rows))
		}
	})

	const lint = "run the linter before every push"
	w.AdvanceDays(1)
	w.Step("day6/codex-deletes-adas-note-she-restores-it", func() {
		id := jAgentsRows(t, m, app)[lint].ID
		jAgentsRun(m, app, &jCodex, "cx-6", "memory", "delete", id, "--reason", "looks stale", "--yes").OK(t)
		if _, ok := jAgentsRows(t, m, app)[lint]; ok {
			t.Errorf("%q still listed after codex deleted it", lint)
		}
		trash := m.LoreIn(app, "trash", "list", "--by", codexActor, "--json").OK(t)
		if !strings.Contains(trash.Stdout, id) {
			t.Errorf("trash list --by codex does not show %s: %s", id, trash.Stdout)
		}
		m.LoreIn(app, "trash", "restore", id, "--yes").OK(t)
		got := jAgentsRows(t, m, app)[lint]
		if got.ID != id || got.CreatedBy != person {
			t.Errorf("restored note %+v, want id %s created by Ada %s", got, id, person)
		}
		if n := jAgentsDataFiles(t, app); n != len(jAgentsRows(t, m, app)) {
			t.Errorf("after restore %d memory files, list shows %d", n, len(jAgentsRows(t, m, app)))
		}
	})

	w.Step("day6/claude-edits-adas-note-she-edits-back", func() {
		id := jAgentsRows(t, m, app)[lint].ID
		jAgentsRun(m, app, &jClaude, "cc-6", "memory", "edit", id, "--body", "run the linter sometimes").OK(t)
		got := jAgentsRows(t, m, app)["run the linter sometimes"]
		if got.ID != id || got.UpdatedBy != claudeActor || got.CreatedBy != person {
			t.Errorf("after Claude's edit: %+v, want id %s created by Ada, updated by Claude %s", got, id, claudeActor)
		}
		m.LoreIn(app, "memory", "edit", id, "--body", lint).OK(t)
		got = jAgentsRows(t, m, app)[lint]
		if got.ID != id || got.UpdatedBy != person || got.CreatedBy != person {
			t.Errorf("after editing back: %+v, want id %s created and updated by Ada %s", got, id, person)
		}
		if _, ok := jAgentsRows(t, m, app)["run the linter sometimes"]; ok {
			t.Errorf("Claude's wording is still listed after Ada edited it back")
		}
	})

	w.Step("day6/purge-then-regret", func() {
		// Ada commits the notes with her code, then Codex deletes one and
		// she empties it from the trash; later she wants it back.
		app.Git("add", "-A")
		app.Git("commit", "-q", "-m", "notes")
		const gone = "codex: use table-driven tests"
		id := jAgentsRows(t, m, app)[gone].ID
		jAgentsRun(m, app, &jCodex, "cx-6", "memory", "delete", id, "--yes").OK(t)
		m.LoreIn(app, "trash", "purge", id, "--confirm").OK(t)
		if _, ok := jAgentsRows(t, m, app)[gone]; ok {
			t.Errorf("%q still listed after the purge", gone)
		}
		// Both ways back say it is gone for good and how to read it.
		for _, args := range [][]string{{"trash", "restore", id, "--yes"}, {"trash", "restore", id, "--from-history"}} {
			r := m.LoreIn(app, args...).Refused(t, "E_PURGED", "lore trash show "+id+" --from-history")
			if strings.Contains(r.Out(), "by act_") {
				t.Errorf("the refusal names the purger by actor id, not by name: %s", r)
			}
		}
		m.LoreIn(app, "trash", "restore", id, "--yes").Says(t, "purged by "+jAgentsName)
		show := m.LoreIn(app, "trash", "show", id, "--from-history").OK(t)
		t.Logf("trash show --from-history:\n%s", show.Out())
		show.Says(t, "not committed yet") // where the text was read from
		if !strings.Contains(show.Out(), gone) {
			t.Errorf("trash show --from-history does not give back the purged text: %s", show)
		}
		readded := jAgentsAdd(t, m, app, nil, "", gone)
		if got := jAgentsRows(t, m, app)[gone]; got.ID != readded || got.CreatedBy != person {
			t.Errorf("re-added note %+v, want id %s by Ada", got, readded)
		}
	})
}

// TestJourneyAgentsKilled is the journey's day 4 on its own World (it runs
// beside TestJourneyAgents): agent sessions that end in the middle of a write.
// The kill points are spread over jAgentsKillClones clones of the repo on the
// laptop (Ada keeps a few worktrees, one per task), each killed in turn, so
// the part stays short; each clone gets every check after every kill.
func TestJourneyAgentsKilled(t *testing.T) {
	t.Parallel()
	w, m, app, _, claudeActor, _ := jAgentsDay1(t)
	clones := []*teamharness.Clone{app}
	for k := 1; k < jAgentsKillClones; k++ {
		c := m.Clone(w.Remote(jAgentsRepo), fmt.Sprintf("app-task%d", k))
		m.LoreIn(c, "init").OK(t)
		clones = append(clones, c)
	}
	w.AdvanceDays(3)
	w.Step("day4/claude-killed-at-every-git-step", func() {
		// One probe write per clone counts its git commands; the session is
		// then killed before each of them (and after the last), the kill
		// points dealt out over the clones.
		type job struct {
			n    int
			when string
		}
		jobs := make([][]job, len(clones))
		for k, c := range clones {
			// The clone's first write of the day does a little more than the
			// next (one more git call), so the count is taken on the second.
			jAgentsAdd(t, m, c, &jClaude, "cc-probe", fmt.Sprintf("claude: first write of the day %d", k))
			before := len(w.GitCalls(m.Name))
			jAgentsAdd(t, m, c, &jClaude, "cc-probe", fmt.Sprintf("claude: probe write %d", k))
			calls := len(w.GitCalls(m.Name)[before:])
			if got := jAgentsRows(t, m, c)[fmt.Sprintf("claude: probe write %d", k)].CreatedBy; got != claudeActor {
				t.Errorf("Claude's note in clone %s is stamped %s, want the same Claude actor %s", c.Dir, got, claudeActor)
			}
			if k == 0 {
				t.Logf("a memory add runs %d git commands; the session is killed before each", calls)
			}
			for n := 1 + k; n <= calls; n += len(clones) {
				jobs[k] = append(jobs[k], job{n, teamharness.KillBefore})
			}
			if k == len(clones)-1 {
				jobs[k] = append(jobs[k], job{calls, teamharness.KillAfter}) // its last git step done, then gone
			}
		}
		var died atomic.Int32
		jAgentsEachClone(t, m, clones, func(k int, c *teamharness.Clone, rec *jAgentsRec) {
			counter := filepath.Join(w.Root, fmt.Sprintf("kill-agents-%d", k))
			for _, j := range jobs[k] {
				_ = os.Remove(counter)
				body := fmt.Sprintf("claude: killed at git call %d %s", j.n, j.when)
				r := jAgentsAs(c, &jClaude, "cc-killed").With(teamharness.EnvKillOn+"= ", teamharness.EnvKillWhen+"="+j.when,
					teamharness.EnvKillNth+"="+fmt.Sprint(j.n), teamharness.EnvKillCounter+"="+counter).Lore("memory", "add", "--body", body)
				if r.Code != 0 {
					died.Add(1)
				} else {
					t.Logf("clone %d: the write killed at git call %d %s finished", k, j.n, j.when)
				}
				jAgentsAfterKill(t, rec, c, body, claudeActor, r)
			}
		})
		t.Logf("%d of the killed writes died before they finished", died.Load())
		var want int32
		for _, js := range jobs {
			want += int32(len(js))
		}
		if died.Load() != want {
			t.Errorf("only %d of %d kills landed: a write that is not killed tests nothing here", died.Load(), want)
		}
	})

	w.Step("day4/claude-window-closed-mid-write", func() {
		// No git step to hook: the window closes at any moment of the write,
		// including between the row file and its audit line.
		start := time.Now()
		jAgentsAdd(t, m, app, &jClaude, "cc-timed", "claude: timing probe")
		full := time.Since(start)
		jAgentsEachClone(t, m, clones, func(k int, c *teamharness.Clone, rec *jAgentsRec) {
			for i := 1 + k; i <= jAgentsTimedKills; i += len(clones) {
				d := full * time.Duration(i) / jAgentsTimedKills
				body := fmt.Sprintf("claude: window closed at %d/%d", i, jAgentsTimedKills)
				jAgentsAfterKill(t, rec, c, body, claudeActor, jAgentsKillAt(t, c, &jClaude, d, body))
			}
		})
	})
}

// jAgentsKillClones is how many clones the kill points are spread over.
const jAgentsKillClones = 4

// jAgentsRec collects the lore runs of goroutines for the step's
// readable-output invariant (Machine.results is not safe for them).
type jAgentsRec struct {
	mu sync.Mutex
	rs []teamharness.Result
}

func (r *jAgentsRec) add(res teamharness.Result) teamharness.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rs = append(r.rs, res)
	return res
}

// jAgentsEachClone runs fn for every clone at once and records their runs.
func jAgentsEachClone(t *testing.T, m *Machine, clones []*teamharness.Clone, fn func(k int, c *teamharness.Clone, rec *jAgentsRec)) {
	t.Helper()
	rec := &jAgentsRec{}
	var wg sync.WaitGroup
	for k, c := range clones {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(k, c, rec)
		}()
	}
	wg.Wait()
	m.results = append(m.results, rec.rs...)
}

// jAgentsListenRe is the line `lore api` prints when it serves.
var jAgentsListenRe = regexp.MustCompile(`lore api listening on (http://127\.0\.0\.1:[0-9]+) \(token: ([^)]+)\)`)

// jAgentsAPI is a running `lore api`.
type jAgentsAPI struct {
	t           *testing.T
	base, token string
}

// jAgentsStartAPI runs `lore api --port 0` in the clone from a tool's
// session (nil = the person's terminal) until the test ends.
func jAgentsStartAPI(t *testing.T, c *teamharness.Clone, tool *jAgentsTool, session string) *jAgentsAPI {
	t.Helper()
	cmd := jAgentsAs(c, tool, session).Command("api", "--port", "0")
	cmd.Stdout = io.Discard
	se, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	found := make(chan []string, 1)
	go func() {
		sc := bufio.NewScanner(se)
		var seen []string
		for sc.Scan() {
			seen = append(seen, sc.Text())
			if mm := jAgentsListenRe.FindStringSubmatch(sc.Text()); mm != nil {
				found <- mm
				_, _ = io.Copy(io.Discard, se)
				return
			}
		}
		found <- []string{strings.Join(seen, " | ")}
	}()
	select {
	case mm := <-found:
		if len(mm) != 3 {
			t.Fatalf("lore api did not start: %s", mm[0])
		}
		b, err := os.ReadFile(mm[2])
		if err != nil {
			t.Fatal(err)
		}
		return &jAgentsAPI{t: t, base: mm[1], token: strings.TrimSpace(string(b))}
	case <-time.After(30 * time.Second):
		t.Fatal("lore api printed no listening line")
	}
	return nil
}

// call sends one request (agent: the X-Lore-Agent header, "" none) and
// decodes a 2xx JSON answer into out (nil: ignored); it returns the status.
func (a *jAgentsAPI) call(method, path, agent, body string, out any) int {
	a.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.base+path, rd)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if agent != "" {
		req.Header.Set("X-Lore-Agent", agent)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Errorf("%s %s: %v", method, path, err)
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		a.t.Errorf("%s %s → %d: %s", method, path, resp.StatusCode, raw)
		return resp.StatusCode
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Errorf("%s %s: decode %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

// add posts a memory and returns its id.
func (a *jAgentsAPI) add(agent, body string) string {
	a.t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	b, _ := json.Marshal(map[string]string{"body": body})
	a.call(http.MethodPost, "/v1/memories", agent, string(b), &out)
	return out.ID
}

// get reads one memory over REST.
func (a *jAgentsAPI) get(id string) jAgentsRow {
	a.t.Helper()
	var out struct {
		Data jAgentsRow `json:"data"`
	}
	a.call(http.MethodGet, "/v1/memories/"+id, "", "", &out)
	return out.Data
}

// bodies is every memory body the API lists.
func (a *jAgentsAPI) bodies() map[string]bool {
	a.t.Helper()
	var out struct {
		Data []jAgentsRow `json:"data"`
	}
	a.call(http.MethodGet, "/v1/memories", "", "", &out)
	got := map[string]bool{}
	for _, r := range out.Data {
		got[r.Body] = true
	}
	return got
}

// jAgentsTimedKills is how many moments of one write a session is killed at.
const jAgentsTimedKills = 8

// jAgentsKillAt starts an agent's memory add and SIGKILLs it after d (the
// session's window is closed at that moment), then waits for it.
func jAgentsKillAt(t *testing.T, c *teamharness.Clone, tool *jAgentsTool, d time.Duration, body string) teamharness.Result {
	t.Helper()
	cmd := jAgentsAs(c, tool, "cc-timed").Command("memory", "add", "--body", body)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Start(); err != nil {
		t.Errorf("start %v: %v", cmd.Args, err)
		return teamharness.Result{Args: cmd.Args[1:], Code: killed}
	}
	timer := time.AfterFunc(d, func() { _ = cmd.Process.Kill() })
	err := cmd.Wait()
	timer.Stop()
	code := 0
	if err != nil {
		code = killed
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() >= 0 {
			code = ee.ExitCode()
		}
	}
	return teamharness.Result{Args: cmd.Args[1:], Stdout: so.String(), Stderr: se.String(), Code: code}
}

// jAgentsAfterKill is what the person checks after an agent session died
// in the middle of writing body: the note is there whole (by the agent) or
// not at all, nothing half-written is left, and the person's next write
// works at once. Safe to call from a goroutine (no Fatal; runs go to rec).
func jAgentsAfterKill(t *testing.T, rec *jAgentsRec, c *teamharness.Clone, body, agent string, r teamharness.Result) {
	t.Helper()
	rec.add(r)
	next := rec.add(c.Lore("memory", "add", "--body", "after: "+body))
	if next.Code != 0 {
		t.Errorf("after an agent died mid-write (%s), the person's next write fails: %s", body, next)
	}
	list := rec.add(c.Lore("memory", "list", "--json"))
	var out struct {
		Data []jAgentsRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(list.Stdout), &out); list.Code != 0 || err != nil {
		t.Errorf("memory list after the kill at %q: %v: %s", body, err, list)
		return
	}
	rows := map[string]jAgentsRow{}
	for _, row := range out.Data {
		rows[row.Body] = row
	}
	if row, ok := rows[body]; ok && row.CreatedBy != agent {
		t.Errorf("the killed agent's note %q is stamped %s, want %s", body, row.CreatedBy, agent)
	}
	if r.Code == 0 {
		if _, ok := rows[body]; !ok {
			t.Errorf("memory add exited 0 but %q is not listed", body)
		}
	}
	if files := jAgentsDataFiles(t, c); files != len(out.Data) {
		t.Errorf("after the kill at %q: %d memory files on disk, memory list shows %d", body, files, len(out.Data))
	}
	if st := c.TryGit("status", "--porcelain"); st.Code != 0 {
		t.Errorf("git status after the kill at %q: %s", body, st.Stderr)
	}
}

// The setup part's names (placeholders only).
const (
	jAgentsSetup    = "personal"
	jAgentsLogin    = "ada-agents"
	jAgentsStore    = "github.com/ada-agents/lore-me"
	jAgentsStoreURL = "git@github.com:ada-agents/lore-me.git"
)

// jAgentsApply saves one setup step the way the setup page does; a refusal
// fails the test with what `setup check` says.
func jAgentsApply(t *testing.T, m *Machine, step string, answers any) {
	t.Helper()
	b, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Lore("", "setup", "apply", jAgentsSetup, "--step", step, "--answers", string(b), "--json")
	if r.Code != 0 {
		c := m.Lore("", "setup", "check", jAgentsSetup, "--step", step, "--answers", string(b), "--json")
		t.Fatalf("setup apply %s: %s\ncheck says: %s", step, r, c.Stdout)
	}
}

// TestJourneyAgentsSetup is weeks 2 and 3 on their own World (it runs beside
// the other parts): Ada moves from a hand-written identity to the setup
// page while her Claude Code and Codex sessions keep writing. She closes
// the page halfway, comes back the next day, later removes the setup and
// re-adds it. Through all of it each tool keeps one actor: the notes an
// agent wrote stay its own, and nothing splits into a second actor.
func TestJourneyAgentsSetup(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona(jAgentsName, jAgentsEmail)
	m := ada.Laptop()
	w.Remote(jAgentsStore) // the empty repo Ada made for her personal store
	app := m.Clone(w.Remote(jAgentsRepo), "app")
	via := func(tool string) string { return jAgentsName + " via " + tool }
	var person, claudeActor, codexActor string

	// sameActors checks whoami at the person's terminal and in each tool's
	// session, and that the actor list is still exactly those three.
	sameActors := func(when string) {
		t.Helper()
		for _, c := range []struct {
			tool     *jAgentsTool
			id, name string
			kind     string
		}{{nil, person, jAgentsName, "human"}, {&jClaude, claudeActor, via(jAgentsClaude), "agent"}, {&jCodex, codexActor, via(jAgentsCodex), "agent"}} {
			who := jAgentsWhoami(t, m, app, c.tool, "s-"+when).Data.Identity
			if who.ActorID != c.id || who.Kind != c.kind {
				t.Errorf("%s: whoami %+v, want %s (%s)", when, who, c.id, c.kind)
			}
			// With no setup the identity falls back to git, and its display
			// is git's user.name (owner decision 2, 2026-11-10), never the
			// email's local part: after the setup is removed the names stay.
			if who.Name != c.name {
				t.Errorf("%s: whoami name %q, want %q", when, who.Name, c.name)
			}
		}
		if a := jAgentsActors(t, m, app); len(a) != 3 {
			t.Errorf("%s: actor list has %d actors, want Ada and her two tools: %+v", when, len(a), a)
		}
	}

	w.AdvanceDays(7)
	w.Step("week2/setup-page-closed-halfway", func() {
		m.GitHubAccounts(t, GitHubAccount{Login: jAgentsLogin, Key: "id_ed25519"})
		jAgentsApply(t, m, "kind", map[string]any{"kind": "solo", "name": jAgentsSetup, "default": true})
		jAgentsApply(t, m, "identity", map[string]any{"name": jAgentsName, "email": jAgentsEmail})
		// Ada closes the page here; her tools keep working in the repo.
		m.LoreIn(app, "init").OK(t)
		jAgentsAdd(t, m, app, nil, "", "ada: half set up")
		jAgentsAdd(t, m, app, &jClaude, "cc-w2", "claude: written while the setup was half done")
		jAgentsAdd(t, m, app, &jCodex, "cx-w2", "codex: written while the setup was half done")
		rows := jAgentsRows(t, m, app)
		person = rows["ada: half set up"].CreatedBy
		claudeActor = rows["claude: written while the setup was half done"].CreatedBy
		codexActor = rows["codex: written while the setup was half done"].CreatedBy
		if person == "" || claudeActor == person || codexActor == person || claudeActor == codexActor {
			t.Errorf("half set up: notes stamped person %q, claude %q, codex %q; want three different actors", person, claudeActor, codexActor)
		}
		sameActors("half set up")
	})

	w.AdvanceDays(1)
	w.Step("week2/setup-page-next-day", func() {
		show := m.Lore("", "setup", "show", jAgentsSetup, "--json").OK(t)
		if !strings.Contains(show.Stdout, jAgentsEmail) {
			t.Errorf("the next day the setup forgot yesterday's answers: %s", show.Stdout)
		}
		jAgentsApply(t, m, "github", map[string]any{"github_account": jAgentsLogin, "host_alias": "github.com"})
		jAgentsApply(t, m, "personal_store", map[string]any{"personal_store": "create", "store_url": jAgentsStoreURL})
		jAgentsApply(t, m, "tools", map[string]any{"tools": []string{jAgentsClaude, jAgentsCodex}})
		jAgentsApply(t, m, "secrets", map[string]any{"backend": "none"})
		jAgentsApply(t, m, "folders", map[string]any{"folders": []string{filepath.Join(w.Root, "work", m.Name) + "/**"}, "default": true})
		jAgentsApply(t, m, "this_folder", map[string]any{})
		jAgentsApply(t, m, "summary", map[string]any{})
		jAgentsAdd(t, m, app, &jClaude, "cc-w2b", "claude: after the setup")
		if got := jAgentsRows(t, m, app)["claude: after the setup"].CreatedBy; got != claudeActor {
			t.Errorf("after finishing the setup Claude's note is stamped %s, want the same Claude actor %s", got, claudeActor)
		}
		sameActors("setup finished")
	})

	w.AdvanceDays(7)
	w.Step("week3/remove-setup-then-re-add", func() {
		m.Lore("", "setup", "remove", jAgentsSetup, "--json").OK(t)
		jAgentsAdd(t, m, app, &jCodex, "cx-w3", "codex: while the setup is gone")
		if got := jAgentsRows(t, m, app)["codex: while the setup is gone"].CreatedBy; got != codexActor {
			t.Errorf("with the setup removed Codex's note is stamped %s, want the same Codex actor %s", got, codexActor)
		}
		sameActors("setup removed")
		m.Lore("", "setup", "trash", "restore", jAgentsSetup, "--json").OK(t)
		jAgentsAdd(t, m, app, &jClaude, "cc-w3", "claude: setup is back")
		rows := jAgentsRows(t, m, app)
		for body, want := range map[string]string{
			"claude: written while the setup was half done": claudeActor,
			"codex: while the setup is gone":                codexActor,
			"claude: setup is back":                         claudeActor,
			"ada: half set up":                              person,
		} {
			if got := rows[body].CreatedBy; got != want {
				t.Errorf("after re-adding the setup %q is stamped %s, want %s", body, got, want)
			}
		}
		sameActors("setup re-added")
	})
}
