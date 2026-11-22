package wire

// migratedby.go — who `lore migrate run` records as having raised a
// project's format (T-1). It is the PERSON, never "<person> via <tool>
// (agent:auto/…)": a detected agent acts for its person; the agent goes
// into migrated_via only, the audit counterpart of the Lore-Agent commit
// trailer (gitcommit.AgentTrailer).
//
// devco:D-705: the person is recorded by display NAME only, never "Name
// <email>" and never an actor id. _meta.json is a committed file of the
// code repo, and in a client repo lore's own pre-commit leak check refuses
// a commit whose non-row files name the email of a person on an org
// store's roster (logic/leakcheck emailHits, F-4): an email there made
// `lore migrate run` produce the one commit lore itself refuses. An actor
// id is no way out: the same check refuses a roster actor id outside a row
// file unless that actor's row file is in the same commit (idHits,
// projectedActors), which a migration cannot promise. The name is what the
// commit's author line shows anyway; the rule is the same in every repo
// kind (one rule, no client/own branch). A _meta.json an earlier build
// wrote with "Name <email>" still reads and is shown as written.

import (
	"context"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
)

// migratedViaAgent is migrated_via when identity.agent_display hides the
// tool: an agent ran the migration, which one is not named.
const migratedViaAgent = "agent"

// migratedBy splits who into the person who ran the migration (their
// display name; "" when they have none, or it holds an address: no name is
// ever made up from an email) and the agent it ran through ("<tool>
// (<agent key>)", "agent" when identity.agent_display hides the tool, ""
// for the person, CI or an agent with no tool label).
func migratedBy(who identity.Resolved) (by, via string) {
	if name := strings.TrimSpace(who.Person().DisplayName); !strings.Contains(name, "@") {
		by = name
	}
	if key := strings.TrimSpace(who.Agent); key != "" {
		if who.HidesTool() {
			// #113 (devco:D-900): identity.agent_display hides the tool, and
			// the key carries its slug: record only that an agent ran it
			return by, migratedViaAgent
		}
		via = key
		if label := strings.TrimSpace(who.Label); label != "" {
			via = label + " (" + key + ")"
		}
	}
	return by, via
}

// migratedByName is the name a recorded migrated_by keeps: the value as it
// is when it holds no address; for lore 0.1.18's "Name <email>" (or a bare
// address) the name before the address, "" when there is none. hadEmail
// reports that the value named an address.
func migratedByName(v string) (name string, hadEmail bool) {
	v = strings.TrimSpace(v)
	if !strings.Contains(v, "@") {
		return v, false
	}
	if i := strings.Index(v, "<"); i >= 0 {
		name = strings.TrimSpace(v[:i])
	}
	if strings.Contains(name, "@") {
		name = ""
	}
	return name, true
}

// migrateMigratedByEmail is the migrate run step that takes the email out
// of a migrated_by lore 0.1.18 already wrote (devco:D-705 follow-up).
// 0.1.18's migrate recorded "Name <email>" and, in a client repo, lore's
// own pre-commit refused the commit, so the file was left uncommitted; a
// second run found every row current and said "Nothing to migrate",
// keeping the email. This step rewrites the value to the name only (the
// key is left out when no name remains), says so once with the commit to
// make, and under --dry-run only says what it would write.
func (a *App) migrateMigratedByEmail(ctx context.Context, dryRun bool) ([]string, error) {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" || !loreFolderAt(top) {
		return nil, nil //nolint:nilerr // no repo with lore data here: no _meta.json to look at
	}
	var out []string
	line, err := fixMigratedByEmail(top, dryRun)
	if err != nil {
		return nil, err
	}
	if line != "" {
		out = append(out, line)
	}
	// #113 (devco:D-900): a migrated_via that names the tool, written
	// before identity.agent_display hid it
	if mode, _ := agentDisplayMode(a.opts, os.Getenv); mode.HidesTool() {
		via, err := fixMigratedVia(top, dryRun)
		if err != nil {
			return nil, err
		}
		if via != "" {
			out = append(out, via)
		}
	}
	return out, nil
}

// fixMigratedVia is the migrate run step that takes the tool out of a
// recorded migrated_via when identity.agent_display hides it (#113): the
// value becomes "agent" (an agent ran it, which one is not named); under
// --dry-run it only says what it would write. "" when migrated_via is
// empty or already "agent". Every other key of _meta.json is kept.
func fixMigratedVia(top string, dryRun bool) (string, error) {
	pf, err := rowfs.ReadProjectFormat(top)
	if err != nil || !pf.Recorded || pf.Format < rowfs.ProjectFormatCurrent {
		return "", nil //nolint:nilerr // an unreadable _meta.json is the format step's report, not this one's
	}
	if v := strings.TrimSpace(pf.MigratedVia); v == "" || v == migratedViaAgent {
		return "", nil
	}
	const why = ": migrated_via names the agent's tool, and identity.agent_display hides it; "
	if dryRun {
		return rowfs.RepoMetaPath + why + "would keep migrated_via \"" + migratedViaAgent + "\", not written (dry run)", nil
	}
	if _, err := rowfs.WriteProjectFormat(top, pf.Format, rowfs.ProjectMigration{By: pf.MigratedBy, Via: migratedViaAgent, At: pf.MigratedAt}); err != nil {
		return "", err
	}
	return rowfs.RepoMetaPath + why + "now keeps migrated_via \"" + migratedViaAgent + "\"", nil
}

// fixMigratedByEmail is migrateMigratedByEmail on the checkout at top: the
// step line ("" when migrated_by names no address, or the project is not
// recorded in the current format: the run that raises it writes the name
// only anyway). Every other key of _meta.json is kept.
func fixMigratedByEmail(top string, dryRun bool) (string, error) {
	pf, err := rowfs.ReadProjectFormat(top)
	if err != nil || !pf.Recorded || pf.Format < rowfs.ProjectFormatCurrent {
		return "", nil //nolint:nilerr // an unreadable _meta.json is the format step's report, not this one's
	}
	name, had := migratedByName(pf.MigratedBy)
	if !had {
		return "", nil
	}
	keep := "the name only (" + name + ")"
	if name == "" {
		keep = "no migrated_by (no name to keep)"
	}
	const why = ": migrated_by names an email (lore 0.1.18 wrote \"Name <email>\", which lore's leak check refuses in a client repo's commit); "
	if dryRun {
		return rowfs.RepoMetaPath + why + "would keep " + keep + ", not written (dry run)", nil
	}
	if _, err := rowfs.WriteProjectFormat(top, pf.Format, rowfs.ProjectMigration{By: name, Via: pf.MigratedVia, At: pf.MigratedAt}); err != nil {
		return "", err
	}
	return rowfs.RepoMetaPath + why + "now keeps " + keep, nil
}
