//go:build stories && !windows

package stories

// O_stale_fact_test.go — devco:D-941 (bug hunt H-190, owner feature F-2)
// on the owner-shaped machine (story O): the admin keeps notes about the
// platform repo's code in a folder of a personal store, each naming its
// evidence (fact_evidence: file:line, or a commit) and the day it was
// checked (fact_verified_at). A week later a file one note names changes.
// Nothing said that note may no longer hold. Now lint reports it with the
// changed file and the command that re-verifies it (which runs where
// printed and clears it), doctor counts it, and search and ask mark it and
// rank it below a fresh note with the same match, also when only a
// comment on it matched (with D-942); show and ask about the file say it is
// stale and print the same re-verify command (D-953); a stale note holding
// the question's common words ranks below a fresh note holding its rare
// ones (D-1007).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStoryOStaleFactFlagged(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	commitOn := func(day string) {
		pc.With("GIT_AUTHOR_DATE="+day+"T10:00:00Z", "GIT_COMMITTER_DATE="+day+"T10:00:00Z").CommitAll("billing on " + day)
	}
	var fresh, stale, byCommit string
	// shimGit puts a git that logs its arguments (to the returned file)
	// first on ada's PATH, one-shot (a read server's git is not counted),
	// until done.
	shimGit := func(dir string) (calls string, done func()) {
		realGit, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		calls = filepath.Join(dir, "git-calls.txt")
		shim := filepath.Join(dir, "bin")
		if err := os.MkdirAll(shim, 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\nexec '" + realGit + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		oldPath, oldServer := ada.Env("PATH"), ada.Env("LORE_READ_SERVER")
		path := oldPath
		if path == "" {
			path = os.Getenv("PATH")
		}
		ada.SetEnv("PATH", shim+string(os.PathListSeparator)+path)
		ada.SetEnv("LORE_READ_SERVER", "0") // count the git of this process, not a server's
		return calls, func() { ada.SetEnv("PATH", oldPath); ada.SetEnv("LORE_READ_SERVER", oldServer) }
	}

	s.Step("D-941/facts-with-evidence-in-a-store-folder", func() {
		pc.WriteFile("billing/refund.go", "package billing\n\n// Refund caps a refund at the order total.\nfunc Refund() {}\n")
		pc.WriteFile("billing/card.go", "package billing\n\n// Card refunds go back to the original card.\nfunc Card() {}\n")
		commitOn("2026-11-01")
		first := pc.Git("rev-parse", "--short", "HEAD")
		// the repo keeps its notes in a folder of the org store (no .lore folder)
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "billing").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "billing", "--own", "--yes").OK(t)
		fresh = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "quokka refunds says exports run nightly and take forty minutes",
			"--meta", "fact_evidence=billing/card.go:3", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		stale = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "quokka refunds says a refund is capped at the order total",
			"--meta", "fact_evidence=billing/refund.go:3", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		byCommit = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "Billing started in one commit with both payment paths.",
			"--meta", "fact_evidence="+first, "--json").OK(t))
		if fresh == "" || stale == "" || byCommit == "" {
			t.Fatal("an add --json printed no id")
		}
		ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).Says(t, "lint: no findings")
	})

	s.Step("D-941/the-evidence-changes", func() {
		pc.WriteFile("billing/refund.go", "package billing\n\n// Refund no longer caps the amount.\nfunc Refund() {}\n")
		commitOn("2026-11-10")
	})

	var fix string
	s.Step("D-941/lint-and-doctor-report-it", func() {
		out := ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).
			Says(t, "stale_fact "+stale, "changed after 2026-11-02: billing/refund.go (in ", "stale_fact "+byCommit, "2 finding(s)").Stdout
		if strings.Contains(out, "stale_fact "+fresh) {
			t.Fatalf("lint reports the fact whose evidence did not change:\n%s", out)
		}
		m := regexp.MustCompile(`lore meta set --id ` + stale + `[^;]*--pair fact_verified_at=\d{4}-\d{2}-\d{2}`).FindString(out)
		if m == "" || !strings.Contains(m, "--store ") {
			t.Fatalf("no re-verify command naming the row's store for %s:\n%s", stale, out)
		}
		fix = m
		ada.Lore(pc.Dir, "doctor").Says(t, "stale_fact: 2 row(s)", "lore lint --code stale_fact")
	})

	s.Step("D-941/search-and-ask-mark-it-below-the-fresh-row", func() {
		var sr struct {
			Data struct {
				Hits []struct {
					ID    string `json:"id"`
					Stale string `json:"stale"`
				} `json:"hits"`
			} `json:"data"`
		}
		raw := ada.Lore(pc.Dir, "search", "--query", "quokka refunds", "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(raw), &sr); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		order := map[string]int{}
		for i, h := range sr.Data.Hits {
			order[h.ID] = i + 1
			if (h.ID == stale) != (h.Stale != "") {
				t.Fatalf("stale mark on the wrong hit: %s", raw)
			}
		}
		if order[fresh] == 0 || order[stale] == 0 || order[fresh] > order[stale] {
			t.Fatalf("search: want %s before %s:\n%s", fresh, stale, raw)
		}
		ada.Lore(pc.Dir, "search", "--query", "quokka refunds").OK(t).Says(t, "[stale: evidence changed since 2026-11-02 (billing/refund.go)]")
		ans := ada.Lore(pc.Dir, "ask", "--query", "quokka refunds", "--json").OK(t).Stdout
		var ar struct {
			Rows []struct {
				ID    string `json:"id"`
				Stale string `json:"stale"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(ans), &ar); err != nil {
			t.Fatalf("%v: %s", err, ans)
		}
		pos := map[string]int{}
		for i, r := range ar.Rows {
			pos[r.ID] = i + 1
			if (r.ID == stale) != (r.Stale != "") {
				t.Fatalf("ask: stale mark on the wrong row: %s", ans)
			}
		}
		if pos[fresh] == 0 || pos[stale] == 0 || pos[fresh] > pos[stale] {
			t.Fatalf("ask: want %s before %s:\n%s", fresh, stale, ans)
		}
	})

	// devco:D-953 (bug hunt H-212): show says the note is stale and how to
	// clear it, and so does ask when the question names the evidence file
	// (its rows then come from lore context --for-file): the same command
	// lint printed, which the last step runs.
	s.Step("D-953/show-and-ask-about-the-file-say-stale-and-how-to-clear-it", func() {
		ada.Lore(pc.Dir, "show", "--id", stale).OK(t).Says(t, "  stale: evidence changed since 2026-11-02 (billing/refund.go)\n  fix: when it still holds: "+fix+";")
		if out := ada.Lore(pc.Dir, "show", "--id", fresh).OK(t).Stdout; strings.Contains(out, "stale") {
			t.Fatalf("show of the fresh note says stale:\n%s", out)
		}
		ada.Lore(pc.Dir, "ask", "--query", "is a refund in billing/refund.go capped at the order total").OK(t).
			Says(t, `stale="evidence changed since 2026-11-02 (billing/refund.go)"`, "re-verify each stale row", "  "+stale+": when it still holds: "+fix+";")
		var ar struct {
			Applies []struct {
				ID       string `json:"id"`
				Stale    string `json:"stale"`
				Reverify string `json:"reverify"`
			} `json:"applies"`
		}
		ans := ada.Lore(pc.Dir, "ask", "--query", "is a refund in billing/refund.go capped at the order total", "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(ans), &ar); err != nil {
			t.Fatalf("%v: %s", err, ans)
		}
		marked := false
		for _, a := range ar.Applies {
			if a.ID == stale {
				marked = a.Stale != "" && strings.Contains(a.Reverify, fix)
			} else if a.Stale != "" {
				t.Fatalf("ask --json marks %s stale:\n%s", a.ID, ans)
			}
		}
		if !marked {
			t.Fatalf("ask --json: the applied stale note has no stale and reverify:\n%s", ans)
		}
	})

	// devco:D-941 with D-942: a comment on the stale note matches the
	// query and the note itself does not. The note is listed for its
	// comment (the comment under it, never above it) and still marked and
	// ranked as stale: a fresh note with the same words comes first.
	s.Step("D-941/a-comment-on-a-stale-row-keeps-it-stale", func() {
		fresh2 := oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "walrus ledger: refunds post to the ledger the same day",
			"--meta", "fact_evidence=billing/card.go:3", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		ada.Lore(pc.Dir, "comment", "add", "--on-table", "memories", "--on-id", stale, "--body", "walrus ledger: refunds post to the ledger the same day").OK(t)
		var sr struct {
			Data struct {
				Hits []struct {
					ID         string `json:"id"`
					Stale      string `json:"stale"`
					ViaComment string `json:"via_comment"`
					CommentOn  struct {
						ID string `json:"id"`
					} `json:"comment_on"`
				} `json:"hits"`
			} `json:"data"`
		}
		raw := ada.Lore(pc.Dir, "search", "--query", "walrus ledger", "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(raw), &sr); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		hits := sr.Data.Hits
		at := -1
		for i, h := range hits {
			if h.ID == stale {
				at = i
			}
		}
		if at < 1 || hits[0].ID != fresh2 {
			t.Fatalf("want the fresh note %s first, then the stale note %s listed for its comment:\n%s", fresh2, stale, raw)
		}
		if hits[at].Stale == "" || hits[at].ViaComment == "" {
			t.Fatalf("the stale note listed for its comment is not marked stale:\n%s", raw)
		}
		if at+1 >= len(hits) || hits[at+1].CommentOn.ID != stale {
			t.Fatalf("the comment is not directly under its row:\n%s", raw)
		}
	})

	// devco:D-1007 (set 8 ask 6): the stale note holds three of the
	// question's common words (refund, order, total), a fresh note its two
	// rare ones (heron, export). The stale note ranked first in search and
	// took ask's first slot: 0.9 of its score did not outweigh one more
	// word. A stale fact's words now count by their rarity, so the fresh
	// note leads in both.
	s.Step("D-1007/a-stale-note-on-common-words-ranks-below-a-fresh-rare-one", func() {
		heron := oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "heron export writes one file per day", "--json").OK(t))
		for _, b := range []string{"order total shows in the cart", "an order ships in two days", "the order page lists items", "total tax is per line", "the total is rounded up", "a grand total row closes the invoice"} {
			ada.Lore(pc.Dir, "memory", "add", "--body", b).OK(t)
		}
		q := "how does the heron export handle the order total of a refund"
		var sr struct {
			Data struct {
				Hits []struct {
					ID string `json:"id"`
				} `json:"hits"`
			} `json:"data"`
		}
		raw := ada.Lore(pc.Dir, "search", "--query", q, "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(raw), &sr); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		order := map[string]int{}
		for i, h := range sr.Data.Hits {
			order[h.ID] = i + 1
		}
		if order[heron] != 1 || order[stale] == 0 {
			t.Fatalf("search: want the fresh note %s first and the stale %s listed:\n%s", heron, stale, raw)
		}
		var ar struct {
			Rows []struct {
				ID string `json:"id"`
			} `json:"rows"`
		}
		ans := ada.Lore(pc.Dir, "ask", "--query", q, "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(ans), &ar); err != nil {
			t.Fatalf("%v: %s", err, ans)
		}
		if len(ar.Rows) == 0 || ar.Rows[0].ID != heron {
			t.Fatalf("ask: want the fresh note %s first:\n%s", heron, ans)
		}
	})

	s.Step("D-941/the-printed-fix-runs-and-clears-it", func() {
		ada.Lore(pc.Dir, strings.Fields(strings.TrimPrefix(fix, "lore "))...).OK(t)
		out := ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).Says(t, "1 finding(s)", "stale_fact "+byCommit).Stdout
		if strings.Contains(out, "stale_fact "+stale) {
			t.Fatalf("the re-verified fact is still reported:\n%s", out)
		}
	})

	// devco:D-950 (bug hunt H-209): a store whose every note names its own
	// evidence made each lore ask take ~15 s, one git log per note's file.
	// Forty notes matching the question, each naming another file: ask (and
	// search, lint) checks them all with one git log over every path, and
	// still marks the stale ones.
	s.Step("D-950/ask-checks-every-row-evidence-with-one-git-log", func() {
		var lines strings.Builder
		for i := range 40 {
			file := fmt.Sprintf("billing/gen/f%02d.go", i)
			pc.WriteFile(file, "package gen\n")
			fmt.Fprintf(&lines, `{"op":"memory add","input":{"body":"otter payout note %d: payouts settle after the batch closes","meta":["fact_evidence=%s:1","fact_verified_at=2026-11-02"]}}`+"\n", i, file)
		}
		commitOn("2026-11-12")
		dir := t.TempDir()
		batch := filepath.Join(dir, "notes.jsonl")
		if err := os.WriteFile(batch, []byte(lines.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
		calls, done := shimGit(dir)
		defer done()
		for _, cmd := range [][]string{{"ask", "--query", "otter payout", "--json"}, {"search", "--query", "otter payout", "--json"}, {"lint", "--code", "stale_fact"}} {
			if err := os.WriteFile(calls, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			out := ada.Lore(pc.Dir, cmd...).Stdout
			if !strings.Contains(out, "billing/gen/f") {
				t.Fatalf("lore %s: no row is marked stale:\n%s", cmd[0], out)
			}
			raw, _ := os.ReadFile(calls)
			// one git log over every path item, plus (lint only, which
			// reads every row) one for the later changes of the one note
			// naming a commit: never one per note. The first command asks
			// git; the next ones at the same HEAD may answer from what it
			// kept (devco:D-954), so they ask at most that much.
			pathLogs, evidenceLogs := 0, 0
			for l := range strings.SplitSeq(string(raw), "\n") {
				if strings.HasPrefix(l, "--literal-pathspecs log") {
					evidenceLogs++
					if strings.Contains(l, " --since=") {
						pathLogs++
					}
				}
			}
			if pathLogs > 1 || (cmd[0] == "ask" && pathLogs != 1) || evidenceLogs > 2 {
				t.Fatalf("lore %s ran %d git logs for the notes' evidence (%d over paths), want one over every path:\n%s", cmd[0], evidenceLogs, pathLogs, raw)
			}
		}
	})

	// devco:D-954 (bug hunt H-213): the one git log still cost ~90 ms of
	// every warm lore ask. The answers are kept per code repo and HEAD in
	// the lore home's cache: asked again at the same HEAD, ask starts no
	// git for the notes' evidence and marks the same rows; a new commit
	// moves HEAD, so the next ask asks git again and sees the change.
	s.Step("D-954/ask-at-the-same-head-asks-git-nothing", func() {
		calls, done := shimGit(t.TempDir())
		defer done()
		ask := func() (string, int) {
			if err := os.WriteFile(calls, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			out := ada.Lore(pc.Dir, "ask", "--query", "otter payout", "--json").Stdout
			raw, _ := os.ReadFile(calls)
			logs := 0
			for l := range strings.SplitSeq(string(raw), "\n") {
				if strings.HasPrefix(l, "--literal-pathspecs log") {
					logs++
				}
			}
			return out, logs
		}
		first, _ := ask()
		again, logs := ask()
		if logs != 0 {
			t.Fatalf("lore ask at an unchanged HEAD ran %d evidence git logs, want none (the answers are kept per HEAD)", logs)
		}
		if !strings.Contains(again, "billing/gen/f") || strings.Count(again, "stale") != strings.Count(first, "stale") {
			t.Fatalf("lore ask from the kept answers marks other rows:\nfirst:\n%s\nagain:\n%s", first, again)
		}
		pc.WriteFile("billing/gen/extra.go", "package gen\n")
		commitOn("2026-11-13")
		if _, logs := ask(); logs != 1 {
			t.Fatalf("lore ask after a new commit ran %d evidence git logs, want one (a new HEAD asks again)", logs)
		}
	})

	// devco:D-1033 (bug hunt H-298): a team wrote fact_evidence as people
	// do, one text value with the repo's folder in front, semicolons,
	// line-range lists, a note in brackets and a bare file after a path in
	// its folder; the old comma split read it as paths git never knew, so
	// none of their stale notes was flagged. Now the note is stale where it
	// should be, and a file the repo does not hold next to one it does is
	// said (the check could not run for it; lint names it). Asked from
	// outside the code repo, ask says nothing of it (no line per note) and
	// lint and doctor count the notes they could not check.
	s.Step("D-1033/free-form-evidence-and-ask-outside-the-code-repo", func() {
		written := oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "narwhal refunds: a refund goes back to the original card",
			"--meta", "fact_evidence=billing/card.go:3 (origin/main 1a2b3c4); platform/billing/refund.go:1-3,4", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		gone := oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "narwhal refunds: the refund log lists every refund",
			"--meta", "fact_evidence=billing/card.go:3, docs/gone.md:2", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).Says(t, "stale_fact "+written, "changed after 2026-11-02: billing/refund.go (in ")
		ada.Lore(pc.Dir, "lint", "--code", "unchecked_fact").OK(t).Says(t, "unchecked_fact "+gone, "(docs/gone.md), so it could not be checked")
		ada.Lore(pc.Dir, "ask", "--query", "narwhal refunds").OK(t).
			Says(t, "[stale: evidence changed since 2026-11-02 (billing/refund.go)]", "evidence could not be checked: "+gone)
		ada.Lore(pc.Dir, "show", "--id", gone).OK(t).Says(t, "  evidence could not be checked (not in this repo: docs/gone.md)")
		outside := filepath.Dir(pc.Dir)
		for _, cmd := range [][]string{{"ask", "--query", "narwhal refunds"}, {"search", "--query", "narwhal refunds"}} {
			out := ada.Lore(outside, cmd...).OK(t).Says(t, written).Stdout
			if strings.Contains(out, "could not be checked") || strings.Contains(out, "[stale") {
				t.Fatalf("lore %s outside the code repo prints a line about evidence it cannot check:\n%s", cmd[0], out)
			}
		}
		ada.Lore(outside, "lint", "--code", "unchecked_fact").OK(t).Says(t, "row(s) name fact_evidence that could not be checked: there is no git repo here")
		// doctor reads rows only in a project (outside one it skips them):
		// in the repo it counts the note naming a file the repo lacks
		ada.Lore(pc.Dir, "doctor").Says(t, "unchecked_fact: 1 row(s)' evidence (fact_evidence) could not be checked")
	})
}
