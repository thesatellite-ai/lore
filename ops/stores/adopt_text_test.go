package stores

import (
	"strings"
	"testing"
)

// TestAdoptTextPrintsTheCommit (devco:D-710): --remove-source's next step
// is a commit that runs as printed (a bare `git commit` opened an editor,
// and lore's own hook then refused it): add and commit with a message
// naming the store folder the rows went to; a dry run prints no command.
func TestAdoptTextPrintsTheCommit(t *testing.T) {
	o := AdoptOut{Store: "acme", Folder: "client_a", From: "/work/notes-app", Written: 0, Removed: 3}
	want := `git -C /work/notes-app add -A .lore/data && git -C /work/notes-app commit -m "lore: move the notes to acme/client_a"`
	if got := o.Text(); !strings.Contains(got, "removed 3 row files from /work/notes-app/.lore/data: review and commit it ("+want+")") {
		t.Fatalf("adopt --remove-source text:\n%s\nwant the command %s", got, want)
	}
	o.DryRun = true
	if got := o.Text(); strings.Contains(got, "git -C") || !strings.Contains(got, "would remove 3 row files") {
		t.Fatalf("dry run text: %s", got)
	}
}

// TestAdoptDryRunReadsTheMovingRowInItsRepo (devco:D-724): a dry run of
// store adopt --from <other repo> (without --move) printed the moving
// row's compare as --store <folder>, where it is not yet, and its delete
// as a bare id that names nothing outside that repo: each command a dry
// run prints of the moved row now starts with a cd into the repo it
// reads (quoted when the path needs it) and reads it with --store repo;
// the run itself prints no cd (the row is in the folder then).
func TestAdoptDryRunReadsTheMovingRowInItsRepo(t *testing.T) {
	dup := []NearDuplicate{{Table: "rules", ID: "rul_moved", Existing: "rul_kept", Title: "use portless", Why: "same title", Percent: 100, Kind: "rule"}}
	o := AdoptOut{Store: "acme", Folder: "devbox", From: "/work/my app", Written: 2, DryRun: true, Duplicates: dup}
	got := o.Text()
	for _, want := range []string{
		"compare: cd '/work/my app' && lore show --id rul_moved --store repo && lore show --id rul_kept --store acme/devbox",
		"drop one: lore rule delete --id rul_kept --yes (the folder's), or the moved one: cd '/work/my app' && lore rule delete --id rul_moved --yes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry run text:\n%s\nwant %s", got, want)
		}
	}
	if strings.Contains(got, "--id rul_moved --store acme/devbox") {
		t.Errorf("dry run reads the moving row in the folder it is not in yet:\n%s", got)
	}
	o.From = "/work/notes-app"
	if got := o.Text(); !strings.Contains(got, "compare: cd /work/notes-app && lore show --id rul_moved --store repo") {
		t.Errorf("plain path: %s", got)
	}
	o.DryRun = false
	if got := o.Text(); strings.Contains(got, "cd ") || !strings.Contains(got, "compare: lore show --id rul_moved --store acme/devbox && lore show --id rul_kept --store acme/devbox") {
		t.Errorf("run text: %s", got)
	}
}

// TestAdoptCommitCommandQuotesTheRepoPath (devco:D-724, the class of the
// dry run's cd): the printed repo commit names a repo path with a space
// as one shell word, so it runs as printed.
func TestAdoptCommitCommandQuotesTheRepoPath(t *testing.T) {
	want := `git -C '/work/my app' add -A .lore/data && git -C '/work/my app' commit -m "lore: move the notes to acme/devbox"`
	if got := AdoptCommitCommand("/work/my app", "acme/devbox"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
