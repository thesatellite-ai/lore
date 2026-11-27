//go:build stories && !windows

package stories

// O_ask_setnine_test.go — devco D-1028 (bug hunt H-288, Sync benchmark
// set 9 asks a and c) on the owner-shaped machine (story O). An own repo
// of the org keeps a long as-built note about its widget monorepo holding
// many of a question's common words, a short memory naming the command
// `task deps-bump` the question quotes, and a 2,000-character note whose
// last section holds a rule. ask read the quoted command as two common
// words and printed the long note first, the memory far down search's
// hits; and it cut the 2,000-character note to its best 1,000
// characters, leaving the asked rule out. Now the row writing the quoted
// command comes first and a note up to 2,500 characters is printed whole.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskSetNine(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const (
		bump = "`task deps-bump` raises every package of the widget monorepo to its newest minor release, then rewrites the lock file."
		rule = "The importer stops after five failed pages in a row and marks the run failed."
	)
	pad := strings.Repeat("The widget importer reads each page of the feed and keeps a cursor per shop. ", 8)
	var short string
	s.Step("D-1028/a-long-note-a-quoted-command-and-a-short-note", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		var layout strings.Builder
		layout.WriteString("# Monorepo layout as built\n\nThe widget monorepo holds every package; each task in the Taskfile changes one package. ")
		layout.WriteString("A dependency release is raised by hand per package. Local builds leave files changed in the monorepo.\n\n")
		for n := 0; layout.Len() < 4000; n++ {
			fmt.Fprintf(&layout, "## Package %d\n\nPackage %d shares the lock file; each task names its package and runs in the monorepo.\n\n", n+1, n+1)
		}
		short = "## Overview\n" + pad + "\n\n## Retries\n" + pad + "\n\n## Limits\n" + pad + "\n" + rule
		ins := []map[string]any{
			{"op": "architecturenote add", "input": map[string]any{"title": "Monorepo layout as built", "body": layout.String()}},
			{"op": "memory add", "input": map[string]any{"body": bump}},
			{"op": "memory add", "input": map[string]any{"body": short}},
		}
		for i := range 8 {
			ins = append(ins, map[string]any{"op": "memory add", "input": map[string]any{"body": fmt.Sprintf("The monorepo task list for package %d lives in its Taskfile.", i+1)}})
		}
		var b strings.Builder
		for _, in := range ins {
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
	})

	s.Step("D-1028/the-row-writing-the-quoted-command-comes-first", func() {
		q := "What does `task deps-bump` change in the widget monorepo, and which files does it leave changed locally?"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		out := r.Out()
		at, note := strings.Index(out, "raises every package of the widget monorepo"), strings.Index(out, "Monorepo layout as built")
		if at < 0 || (note >= 0 && note < at) {
			t.Errorf("[O D-1028] ask %q: the memory writing the quoted command is not the first row:\n%s", q, r)
		}
	})

	s.Step("D-1028/a-short-note-is-printed-whole", func() {
		q := "when does the widget importer stop after failed pages"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		if !strings.Contains(r.Out(), rule) {
			t.Errorf("[O D-1028] ask %q: the asked rule of the 2,000-character note is not printed:\n%s", q, r)
		}
		if strings.Contains(r.Out(), fmt.Sprintf("the whole row is %d characters", len(short))) {
			t.Errorf("[O D-1028] ask %q: a note under 2,500 characters was cut:\n%s", q, r)
		}
	})
}
