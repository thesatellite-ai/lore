//go:build stories && !windows

package stories

// O_meta_input_rules_test.go — devco:D-922 (bug hunt H-180, the owner's
// 440-action import, item 2) on the owner-shaped machine (story O). Every
// metadata input rule a write refuses is said where it is refused, with a
// fix that runs as printed, and a dry run refuses what the write refuses:
//
//   - a reserved key name lists the reserved words and a name that passes;
//     the suggested name, run as printed, registers;
//   - an allowed value that is not snake_case prints the whole flag
//     corrected; run as printed, it registers;
//   - the same key registered in the store's own registry and then in a
//     folder: a command that reads only one of them has nothing to warn
//     about (the warning itself is TestMetaAddNamesOtherDefinition);
//   - `meta set --dry-run` on one row writes nothing, and refuses a
//     reserved key and a row kind without metadata with the write's code.

import (
	"regexp"
	"strings"
	"testing"
)

// oHintFlagRe is the corrected flag a metadata refusal's hint prints.
var oHintFlagRe = regexp.MustCompile(`--(name|values) (\S+)`)

func TestStoryOMetaInputRules(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	const folder = "feeds"
	ada.LoreIn(pc, "store", "folder", "add", "team", folder).OK(t)

	// printedFlag runs the meta add of args again with the flag the
	// refusal's hint printed in place of the refused one.
	printedFlag := func(step string, args []string, out string) []string {
		m := oHintFlagRe.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: the refusal printed no corrected flag:\n%s", step, out)
		}
		fixed := append([]string{}, args...)
		for i := range fixed {
			if fixed[i] == "--"+m[1] {
				fixed[i+1] = m[2]
			}
		}
		return fixed
	}

	s.Step("D-922/reserved-name-says-the-rule-and-a-name-that-passes", func() {
		args := []string{"meta", "add", "--name", "state", "--type", "text", "--to", oOrg, "--yes"}
		r := ada.LoreIn(pc, args...)
		oCheck(t, "D-922/reserved-lists-words", r.Code != 0 && strings.Contains(r.Out(), "E_META_RESERVED") &&
			strings.Contains(r.Out(), "the level words (org, project, repo)") && strings.Contains(r.Out(), "--name item_state"),
			"meta add --name state did not list the reserved words and a name that passes:\n%s", r)
		ada.LoreIn(pc, printedFlag("reserved", args, r.Out())...).OK(t)
	})

	s.Step("D-922/value-not-snake-case-prints-the-corrected-flag", func() {
		args := []string{"meta", "add", "--name", "feed_source", "--type", "text", "--strict", "--values", "pylon-feeds,raw", "--to", oOrg, "--yes"}
		r := ada.LoreIn(pc, args...)
		oCheck(t, "D-922/values-rule-and-fix", r.Code != 0 && strings.Contains(r.Out(), "a-z, 0-9 and _, starting with a letter") &&
			strings.Contains(r.Out(), "use --values prism_feeds,raw"),
			"meta add --values pylon-feeds did not say the rule and the fix:\n%s", r)
		ada.LoreIn(pc, printedFlag("values", args, r.Out())...).OK(t)
	})

	s.Step("D-922/same-key-at-store-and-folder-only-where-both-are-read", func() {
		// This repo is not set up: each command reads only the place it
		// names, so the two definitions never meet here and neither the
		// registration nor a bare meta set on a folder row has anything to
		// say. A repo reading both levels gets the warning at the second
		// registration (TestMetaAddNamesOtherDefinition, the issue replay).
		ada.LoreIn(pc, "meta", "add", "--name", "channel", "--type", "text", "--to", oOrg, "--yes").OK(t)
		r := ada.LoreIn(pc, "meta", "add", "--name", "channel", "--type", "text", "--to", oOrg+"/"+folder, "--yes").OK(t)
		if strings.Contains(r.Out(), "also registered") {
			t.Fatalf("a registration warned about a definition this command cannot see:\n%s", r)
		}
		mem := oAddedID(t, ada.LoreIn(pc, "memory", "add", "--body", "Feeds are cached for an hour.", "--to", oOrg+"/"+folder, "--yes", "--json").OK(t))
		set := ada.LoreIn(pc, "meta", "set", "--id", mem, "--pair", "channel=web", "--yes")
		oCheck(t, "D-922/folder-row-resolves-one-definition", set.Code == 0, "meta set on a folder row:\n%s", set)
	})

	s.Step("D-922/meta-set-dry-run-writes-nothing-and-checks-as-the-write", func() {
		mem := oAddedID(t, ada.LoreIn(pc, "memory", "add", "--body", "Feeds refresh every ten minutes.", "--to", oOrg+"/"+folder, "--yes", "--json").OK(t))
		if mem == "" {
			t.Fatal("memory add printed no id")
		}
		r := ada.LoreIn(pc, "meta", "set", "--id", mem, "--pair", "release_note=x", "--dry-run").OK(t)
		oCheck(t, "D-922/dry-run-says-nothing-written", strings.Contains(r.Out(), "nothing written (dry run)"), "meta set --dry-run:\n%s", r)
		if got := ada.LoreIn(pc, "meta", "get", "--id", mem).OK(t); strings.Contains(got.Out(), "release_note") {
			t.Fatalf("meta set --dry-run wrote the value:\n%s", got)
		}
		dry := ada.LoreIn(pc, "meta", "set", "--id", mem, "--pair", "state=x", "--dry-run")
		real := ada.LoreIn(pc, "meta", "set", "--id", mem, "--pair", "state=x", "--yes")
		oCheck(t, "D-922/dry-run-reserved-same-code", dry.Code != 0 && strings.Contains(dry.Out(), "E_META_RESERVED") && strings.Contains(real.Out(), "E_META_RESERVED"),
			"a reserved key: dry run\n%s\nwrite\n%s", dry, real)
		tag := oAddedID(t, ada.LoreIn(pc, "tag", "add", "--name", "feeds_team", "--to", oOrg+"/"+folder, "--yes", "--json").OK(t))
		if tag == "" {
			t.Fatal("tag add printed no id")
		}
		dry = ada.LoreIn(pc, "meta", "set", "--id", tag, "--pair", "owner_team=web", "--dry-run")
		real = ada.LoreIn(pc, "meta", "set", "--id", tag, "--pair", "owner_team=web", "--yes")
		oCheck(t, "D-922/dry-run-table-same-code", dry.Code != 0 && strings.Contains(dry.Out(), "E_META_TABLE") && strings.Contains(real.Out(), "E_META_TABLE") &&
			strings.Contains(real.Out(), "metadata goes on rows of"),
			"a row kind without metadata: dry run\n%s\nwrite\n%s", dry, real)
	})
}
