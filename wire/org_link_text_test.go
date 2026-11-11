package wire

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// linkPID is the project id a `lore org link` / unlink report names.
var linkPID = regexp.MustCompile(`project (prj_[0-9a-f]+)`)

// wantLines fails unless got is exactly want (with {pid} replaced).
func wantLines(t *testing.T, what string, got []string, pid string, want ...string) {
	t.Helper()
	for i := range want {
		want[i] = strings.ReplaceAll(want[i], "{pid}", pid)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s:\n got:\n%s\nwant:\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// pidOf is the project id named in a report's lines.
func pidOf(t *testing.T, lines []string) string {
	t.Helper()
	m := linkPID.FindStringSubmatch(strings.Join(lines, "\n"))
	if m == nil {
		t.Fatalf("no project id in %v", lines)
	}
	return m[1]
}

// TestOrgLinkUnlinkTexts pins every line of `lore org link` (client,
// --own, --store, --dry-run) and `lore org unlink` (--dry-run, done,
// nothing to unlink, unknown org). Owner report (0.1.18-dev): --own said
// "one of agency's OWN repos of agency" and the done line named the
// remote twice ("<remote> (<id>, <remote>)").
func TestOrgLinkUnlinkTexts(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A client repo: --store naming the org's store, dry run, then the link.
	rep, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme", Store: orgTestStoreURL, DryRun: true}, true)
	if err != nil {
		t.Fatalf("link --store --dry-run: %v", err)
	}
	pid := pidOf(t, rep.Lines)
	client := []string{
		"This marks github.com/acme/app as a CLIENT repo of agency: internal notes default to github.com/agency/notes acme/,",
		"and the leak guards turn on for everyone in agency.",
		"  link: project {pid} at github.com/acme/app → github.com/agency/notes@main:acme/ (default=false)",
	}
	wantLines(t, "link --store --dry-run", rep.Lines, pid, append(append([]string{}, client...), "  --dry-run: nothing written")...)
	if rep, err = h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("link: %v", err)
	}
	wantLines(t, "link", rep.Lines, pid, append(append([]string{}, client...),
		"✓ github.com/acme/app ({pid}) linked to github.com/agency/notes acme/ (created; pushed)",
		"  nothing was written to this repo")...)

	// --own on a client repo: refused with the next step.
	_, err = h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme", Own: true}, true)
	if env := errs.EnvelopeOf(err).Error; env.Code != errs.InvalidInput ||
		env.Message != "--own is only for repos matching the org entry's own: patterns: github.com/acme/app does not match agency's own: patterns (github.com/agency/*)" ||
		env.Hint != "link it without --own (as a client repo), or add a matching pattern to own: in agency's lore-org.yaml and run lore org refresh agency" {
		t.Fatalf("link --own in a client repo: %+v", env)
	}

	// One of the org's own repos: the org is named once, no comma left hanging.
	site := w.ownRepo(t, mira)
	hs := orgHostAt(t, site, mira)
	if rep, err = hs.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "site", Own: true, DryRun: true}, true); err != nil {
		t.Fatalf("link --own --dry-run: %v", err)
	}
	sitePID := pidOf(t, rep.Lines)
	own := []string{
		"This marks github.com/agency/site as one of agency's OWN repos: internal notes default to github.com/agency/notes site/.",
		"  link: project {pid} at github.com/agency/site → github.com/agency/notes@main:site/ (default=false)",
	}
	wantLines(t, "link --own --dry-run", rep.Lines, sitePID, append(append([]string{}, own...), "  --dry-run: nothing written")...)
	if rep, err = hs.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "site", Own: true}, true); err != nil {
		t.Fatalf("link --own: %v", err)
	}
	wantLines(t, "link --own", rep.Lines, sitePID, append(append([]string{}, own...),
		"✓ github.com/agency/site ({pid}) linked to github.com/agency/notes site/ (created; pushed)",
		"  nothing was written to this repo")...)

	// Unlink the client repo: dry run, done, then nothing left.
	unlink := []string{
		"This removes 1 link(s) of project {pid} from agency; their folders and notes stay in the store:",
		"  unlink: github.com/acme/app (client) → github.com/agency/notes@main:acme/",
		"Afterwards this repo follows its own rules (own:/repos: patterns, setups) for everyone in agency.",
	}
	if rep, err = h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "agency", DryRun: true}, true); err != nil {
		t.Fatalf("unlink --dry-run: %v", err)
	}
	wantLines(t, "unlink --dry-run", rep.Lines, pid, append(append([]string{}, unlink...), "  --dry-run: nothing written")...)
	if rep, err = h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "agency"}, true); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	wantLines(t, "unlink", rep.Lines, pid, append(append([]string{}, unlink...),
		"✓ unlinked {pid} from agency (1 link(s) removed; pushed)",
		"  nothing was written to this repo")...)
	if rep, err = h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "agency", Project: "acme"}, true); err != nil {
		t.Fatalf("unlink again: %v", err)
	}
	wantLines(t, "unlink again", rep.Lines, pid, "no link of {pid} in agency's stores (folder acme/); nothing to unlink")

	_, err = h.Unlink(ctx, opsorgcmd.UnlinkIn{Org: "northwind"}, true)
	if env := errs.EnvelopeOf(err).Error; env.Code != errs.NotFound || env.Message != `no org entry named "northwind" in ~/.lore/config.yaml` ||
		env.Hint != "name one of the org entries: agency" {
		t.Fatalf("unlink of an unknown org: %+v", env)
	}
}
