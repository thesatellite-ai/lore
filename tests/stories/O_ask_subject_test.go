//go:build stories && !windows

package stories

// O_ask_subject_test.go — devco D-973 (bug hunt H-232, Sync benchmark set
// 6 ask 2) on the owner-shaped machine (story O): an own repo of the org
// linked to its folder keeps a long as-built job queue note and eight
// short rows sharing its common words (job, run, first, still running).
// lore ask cut the note to its "Workers" section, which repeats those
// words, and dropped the "Caller API" section holding the question's
// subject (a duplicate key); now the cut leads with the subject section
// and prints no section holding only the common words under it.

import (
	"strings"
	"testing"
)

func TestStoryOAskExcerptKeepsSubjectSection(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	common := "The first run of a job and every retry run go through the same claim, and the worker returns the run to the queue when it shuts down while the run is still running. "
	subject := "A duplicate key while the earlier attempt is non-terminal gives back that attempt id and writes nothing."

	s.Step("D-973/a-long-note-and-short-rows", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "jobsapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "jobsapp", "--own", "--yes").OK(t)
		note := "# As built: job queue\n\n## Overview\n\nThe job queue runs every background job. Each attempt of a job is a run, kept for thirty days.\n\n## Workers\n\n" + strings.Repeat(common, 5) +
			"\n\n## Caller API\n\nThe caller API takes the job type, the payload and an optional key. " + subject + " Payloads are JSON objects.\n\n## Operations\n\n" + strings.Repeat("Operators watch the queue depth on the dashboard and pause a job type when its runs fail. ", 5)
		ada.Lore(pc.Dir, "architecturenote", "add", "--title", "As built: job queue", "--body", note, "--meta", "fact_state=live", "--yes").OK(t)
		for _, w := range []string{"export", "report", "billing", "mail", "cleanup", "image", "reindex", "sync"} {
			ada.Lore(pc.Dir, "memory", "add", "--body", "The "+w+" job returns the first run id while the run is still running.", "--yes").OK(t)
		}
	})

	s.Step("D-973/the-cut-keeps-the-subject-section", func() {
		r := ada.Lore(pc.Dir, "ask", "--query", "what does the job queue return for a duplicate key while the first run is still running").OK(t)
		out := r.Out()
		if !strings.Contains(out, subject) || !strings.Contains(out, "Caller API (matching section)") {
			t.Fatalf("[O D-973] the cut note dropped the section holding the question's subject:\n%s", r)
		}
		if strings.Contains(out, "> Workers") {
			t.Errorf("[O D-973] a section holding only the question's common words was printed under the subject section:\n%s", r)
		}
		// the printed next step runs where printed and prints the note whole
		id := ""
		for l := range strings.SplitSeq(out, "\n") {
			if i := strings.Index(l, "lore show --id "); i >= 0 {
				id = strings.Fields(l[i+len("lore show --id "):])[0]
			}
		}
		if id == "" {
			t.Fatalf("[O D-973] no lore show --id line under the cut note:\n%s", r)
		}
		ada.Lore(pc.Dir, "show", "--id", id).OK(t).Says(t, "Operators watch the queue depth")
	})
}
