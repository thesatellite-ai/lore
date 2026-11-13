package wire_test

import (
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
)

// helpSpecID is an internal spec or decision id in parentheses, the way
// they leaked into --help: "(CFG4)", "(DQ2)", "(OWNERQ-21)", "(PROV 9.1)",
// "(METADATA 3.19)", a bare "SQ10" after a semicolon, one opening a list
// ("(MQ11, BATCH 7)", "(TQ3; default …)", B3-24), or a dashed one
// anywhere ("P3-AUDIT-3", "TEAM-029"). Help is read by
// people who never saw the specs, so no summary, flag or example text
// may carry one (B3-3, B3-6).
var helpSpecID = regexp.MustCompile(`\(([A-Z][A-Z_]*-?[0-9][0-9.]*|[A-Z][A-Z_]{2,} [0-9][0-9.]*|[A-Z]{2,}-[0-9]+)\)|\([A-Z][A-Z_]{2,} [0-9][0-9.]*,|[;,] [A-Z]{2,}[0-9]+\)|\b[A-Z][A-Z0-9]*-[A-Z][A-Z0-9-]*[0-9]\b|\b[A-Z]{2,}-[0-9]{2,}\b|\([A-Z]{1,}[A-Z_]*-?[0-9][0-9.]*[;,]|[;,] [A-Z][A-Z_]{2,} [0-9][0-9.]*\)`)

// helpNotSpecIDs are standard names the dashed form also matches.
var helpNotSpecIDs = map[string]bool{"ISO-8601": true, "SHA-256": true, "SHA-1": true, "RFC-3339": true}

// TestHelpTextHasNoSpecIDs walks every operation's help text: its summary,
// every flag and argument description and every example title.
func TestHelpTextHasNoSpecIDs(t *testing.T) {
	h := opstest.New(t)
	for _, e := range h.App.Registry().Entries() {
		s := e.Spec()
		texts := []string{s.Summary}
		for _, x := range s.Examples {
			texts = append(texts, x.Title)
		}
		for _, f := range e.InputFields() {
			texts = append(texts, f.Desc)
		}
		for _, txt := range texts {
			for _, m := range helpSpecID.FindAllString(txt, -1) {
				if !helpNotSpecIDs[m] {
					t.Errorf("%s: help text carries the spec id %q: %q", s.Name, m, txt)
				}
			}
		}
	}
}

// TestSchemaTextHasNoSpecIDs walks every built-in table's description and
// every field's: `lore tui` lists the table descriptions, and the REST
// docs and field help print both (bug hunt B3-26: "(TP-14)", "(spec 4.3)",
// "(TASK 11)" on the tui's first screen).
func TestSchemaTextHasNoSpecIDs(t *testing.T) {
	for _, d := range schema.Builtin() {
		texts := []string{d.Description}
		for _, f := range d.Fields {
			texts = append(texts, f.Description)
		}
		for _, txt := range texts {
			for _, m := range schemaSpecID.FindAllString(txt, -1) {
				if !helpNotSpecIDs[m] {
					t.Errorf("table %s: description carries the spec id %q: %q", d.Name, m, txt)
				}
			}
		}
	}
}

// schemaSpecID is helpSpecID plus the lower-case "(spec 4.3)" form the
// schema text used.
var schemaSpecID = regexp.MustCompile(helpSpecID.String() + `|\(spec [0-9][0-9.]*\)|\b[A-Z]{2,} [0-9]+(\.[0-9]+)*\)`)
