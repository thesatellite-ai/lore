package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rosterWorld is a repo with a personal store me, a person (git email
// placeholder) and that person's Claude Code agent, each of whom has
// written to the repo and to me, and the agent's session started (a
// machine-local row only). It returns the world, the repo and me's folder.
func rosterWorld(t *testing.T) (*cliWorld, string, string) {
	t.Helper()
	w := newCLIWorld(t).person()
	// the person's git email, before lore init writes its first actor row
	if err := os.WriteFile(filepath.Join(w.env["HOME"], ".gitconfig"), []byte("[user]\n\temail = pat@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := w.initRepo(t)
	meDir := filepath.Join(t.TempDir(), "me")
	w.mustLore(t, dir, "store", "init", "me", "file://"+filepath.ToSlash(meDir), "--yes")
	w.mustLore(t, dir, "memory", "add", "--body", "person repo note")
	w.mustLore(t, dir, "memory", "add", "--to", "me", "--body", "person me note")
	w.claude("sess-0001")
	w.mustLore(t, dir, "agent", "start", "--tool", "claude_code")
	w.mustLore(t, dir, "memory", "add", "--body", "agent repo note")
	w.mustLore(t, dir, "memory", "add", "--to", "me", "--body", "agent me note")
	w.person()
	return w, dir, meDir
}

// rowWriters are the created_by / updated_by ids of every row file under
// data, and the ids of its actors rows.
func rowWriters(t *testing.T, data string) (writers, roster map[string]bool) {
	t.Helper()
	writers, roster = map[string]bool{}, map[string]bool{}
	err := filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r struct {
			Table     string `json:"_table"`
			ID        string `json:"id"`
			CreatedBy string `json:"created_by"`
			UpdatedBy string `json:"updated_by"`
		}
		if json.Unmarshal(b, &r) != nil {
			return nil // not a row file
		}
		if r.Table == "actors" {
			roster[r.ID] = true
		}
		for _, a := range []string{r.CreatedBy, r.UpdatedBy} {
			if a != "" {
				writers[a] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return writers, roster
}

// TestEveryWriterInTheStoreRoster (FB-ACTOR-ROSTER): every row a person
// or the person's agent writes to a store names a writer that store's
// roster holds, the agent's machine-local session row included nowhere;
// doctor reports nothing, and once a writer's actor row is gone from a
// store doctor names that writer and --fix puts the caller's row back.
func TestEveryWriterInTheStoreRoster(t *testing.T) {
	t.Parallel()
	w, dir, meDir := rosterWorld(t)
	for name, data := range map[string]string{"repo": filepath.Join(dir, ".lore", "data"), "me": filepath.Join(meDir, ".lore", "data")} {
		writers, roster := rowWriters(t, data)
		if len(writers) < 2 {
			t.Fatalf("%s: want the person and the agent among the writers, got %v", name, writers)
		}
		for a := range writers {
			if !roster[a] {
				t.Errorf("store %s: rows saved by %s, which its roster (%v) does not hold", name, a, roster)
			}
		}
	}
	if _, out := w.loreOut(t, dir, "doctor"); strings.Contains(out, "actor_roster") {
		t.Fatalf("doctor reports a roster gap on a clean world:\n%s", out)
	}

	// the person's actor row leaves me (a hand edit, an old build)
	var person string
	out := w.mustLore(t, dir, "actor", "list", "--json")
	var list struct {
		Data []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	for _, a := range list.Data {
		if a.Kind == "human" {
			person = a.ID
		}
	}
	if person == "" {
		t.Fatalf("no person in %s", out)
	}
	if err := os.Remove(filepath.Join(meDir, ".lore", "data", "actors", person+".json")); err != nil {
		t.Fatal(err)
	}
	_, out = w.loreOut(t, dir, "doctor")
	want := "store me holds rows saved by "
	if !strings.Contains(out, want) || !strings.Contains(out, person+"), an actor its roster does not hold") || !strings.Contains(out, "lore doctor --fix") {
		t.Fatalf("doctor must name the writer me's roster lacks:\n%s", out)
	}
	_, out = w.loreOut(t, dir, "doctor", "--fix")
	if _, err := os.Stat(filepath.Join(meDir, ".lore", "data", "actors", person+".json")); err != nil {
		t.Fatalf("doctor --fix did not write the actor row back: %v\n%s", err, out)
	}
	if _, out = w.loreOut(t, dir, "doctor"); strings.Contains(out, "an actor its roster does not hold") {
		t.Fatalf("doctor still reports the gap after --fix:\n%s", out)
	}
}

// TestActorListStores (FB-ACTOR-STORES, FB-ACTOR-KEY): an actor held by
// the repo and by me is listed once, with both stores in the text column
// and in --json stores, a count line names how many stores were merged,
// and the stable key the repo's copy leaves out (no email in committed
// files) comes from me's copy; the repo file still has none.
func TestActorListStores(t *testing.T) {
	t.Parallel()
	w, dir, _ := rosterWorld(t)
	text := w.mustLore(t, dir, "actor", "list")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 3 || lines[2] != "2 actors across 2 stores" {
		t.Fatalf("want two actors and the count line:\n%s", text)
	}
	for _, l := range lines[:2] {
		if !strings.HasSuffix(l, "  repo, me") {
			t.Errorf("line %q lacks the stores column repo, me", l)
		}
	}
	if strings.Index(lines[0], "repo, me") != strings.Index(lines[1], "repo, me") {
		t.Errorf("stores column not aligned:\n%s", text)
	}
	var list struct {
		Data []struct {
			ID        string   `json:"id"`
			Kind      string   `json:"kind"`
			StableKey string   `json:"stable_key"`
			Stores    []string `json:"stores"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(w.mustLore(t, dir, "actor", "list", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	for _, a := range list.Data {
		if strings.Join(a.Stores, ",") != "repo,me" {
			t.Errorf("%s stores %v, want [repo me]", a.ID, a.Stores)
		}
		if a.Kind == "human" && a.StableKey != "human:pat@example.com" {
			t.Errorf("person stable_key %q, want the key from me's copy", a.StableKey)
		}
		b, err := os.ReadFile(filepath.Join(dir, ".lore", "data", "actors", a.ID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "example.com") {
			t.Errorf("the repo's actor file must not hold an email:\n%s", b)
		}
	}
	// one store only: v0.1.17's lines, no column, no count (nothing merged)
	if got := w.mustLore(t, dir, "actor", "list", "--store", "me"); strings.Contains(got, "across") || strings.Contains(got, "repo, me") || strings.Count(got, "\n") != 2 {
		t.Errorf("--store me:\n%s", got)
	}
}

// TestActorKeyBackfill (FB-ACTOR-KEY): an actor row in me written without
// the stable key (an older build) gets it on the person's next write there.
func TestActorKeyBackfill(t *testing.T) {
	t.Parallel()
	w, dir, meDir := rosterWorld(t)
	actors := filepath.Join(meDir, ".lore", "data", "actors")
	ents, err := os.ReadDir(actors)
	if err != nil {
		t.Fatal(err)
	}
	var file string
	for _, e := range ents {
		b, rerr := os.ReadFile(filepath.Join(actors, e.Name()))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if strings.Contains(string(b), `"human:pat@example.com"`) {
			file = filepath.Join(actors, e.Name())
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			delete(doc, "stable_key")
			nb, merr := json.MarshalIndent(doc, "", "  ")
			if merr != nil {
				t.Fatal(merr)
			}
			if err := os.WriteFile(file, append(nb, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if file == "" {
		t.Fatal("no person row in me")
	}
	w.mustLore(t, dir, "memory", "add", "--to", "me", "--body", "person second me note")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"stable_key": "human:pat@example.com"`) {
		t.Fatalf("the next write to me did not put the key back:\n%s", b)
	}
}
