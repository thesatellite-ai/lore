package onboard

import (
	"regexp"
	"strings"
	"testing"
)

// positionalLore is a lore command in a fix text whose first value comes
// right after the command words: `lore setup edit {setup}`,
// `lore store set-url <id>`. Named args everywhere: a value follows its
// --flag (`lore setup edit --name {setup}`).
var positionalLore = regexp.MustCompile(`\blore( [a-z][a-z-]*){1,3} [{<]`)

// loreProse are words after "lore" that start a sentence about lore, not
// a command ("lore cannot read {repo}", "lore picks {setup}").
var loreProse = map[string]bool{"can": true, "cannot": true, "read": true, "picks": true, "skill": true, "writes": true, "finds": true, "uses": true}

// TestFixesShowTheNamedForm: every fix text and every lore command a fix
// runs names each value with its flag, as `lore <cmd> --help` shows it;
// doctor printed `lore setup edit side --default` after the named-args pass.
func TestFixesShowTheNamedForm(t *testing.T) {
	for k, f := range fixes {
		for _, m := range positionalLore.FindAllString(f.Text, -1) {
			if words := strings.Fields(m); !loreProse[words[1]] {
				t.Errorf("fix %v: text shows a positional value %q: %s", k, m, f.Text)
			}
		}
		if len(f.Command) == 0 || f.Command[0] != cmdLore {
			continue
		}
		for i, a := range f.Command[1:] {
			if strings.HasPrefix(a, "-") {
				break
			}
			if strings.ContainsAny(a, "{<") {
				t.Errorf("fix %v: command %v has a value %q before any flag (arg %d)", k, f.Command, a, i+1)
			}
		}
	}
}
