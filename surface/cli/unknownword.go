package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// unknownWord refuses a word typed after a group (or the root) that names
// none of its commands. When the word is a value rather than a mistyped
// command (a row id such as tsk_…, a commit sha, HEAD), the person meant
// to act on it: `lore commit HEAD`, `lore task tree tsk_…`. The refusal
// then names the command that takes it, with its flag (`lore commit show
// --id HEAD`), so the next try works; a plain typo keeps the bare line.
func unknownWord(cmd *cobra.Command, cfg config, word string) error {
	e := errs.New(UsageCode, fmt.Sprintf("unknown command %q for %q", word, cmd.CommandPath()))
	help := cmd.CommandPath() + " --help lists its commands"
	switch {
	case looksLikeValue(word):
		if cmds := idCommands(cmd); len(cmds) > 0 {
			for i, c := range cmds {
				cmds[i] = c + " --id " + word
			}
			e = e.WithHint("a value goes after its flag: " + strings.Join(cmds, " or "))
		} else {
			e = e.WithHint(cmd.CommandPath() + " takes a command, not a value; " + help)
		}
	default:
		// C-13: every refusal names a next step: the commands of that
		// name deeper down (`lore status` → `lore migrate status`), else
		// the near spellings, and always where the commands are listed.
		if near := nearCommands(cmd, word); len(near) > 0 {
			e = e.WithHint("did you mean " + strings.Join(near, " or ") + "? " + help)
		} else {
			e = e.WithHint(help)
		}
	}
	return printUsage(cmd, cfg, e)
}

// nearCommands lists, by path, the visible commands under g named word
// (at any depth), else the direct subcommands cobra finds spelled close to
// it; at most maxIDCommands.
func nearCommands(g *cobra.Command, word string) []string {
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			if !s.IsAvailableCommand() {
				continue
			}
			if s.Name() == word || slices.Contains(s.Aliases, word) {
				out = append(out, s.CommandPath())
			}
			walk(s)
		}
	}
	walk(g)
	if len(out) == 0 {
		if g.SuggestionsMinimumDistance <= 0 {
			g.SuggestionsMinimumDistance = 2
		}
		for _, s := range g.SuggestionsFor(word) {
			out = append(out, g.CommandPath()+" "+s)
		}
	}
	return out[:min(len(out), maxIDCommands)]
}

// revWord matches a git revision word lore commands take as --id: HEAD,
// HEAD~2, HEAD^, @.
var revWord = regexp.MustCompile(`^(HEAD|@)([~^][0-9]*)*$`)

// shaWord matches an abbreviated or full commit sha.
var shaWord = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// looksLikeValue reports whether word is a row id, a sha or a revision:
// something a command acts on, not a command name.
func looksLikeValue(word string) bool {
	if _, err := ids.ParseAny(word); err == nil {
		return true
	}
	return revWord.MatchString(word) || shaWord.MatchString(word)
}

// maxIDCommands caps how many commands one hint names.
const maxIDCommands = 3

// idCommands lists the commands, by path, that take a value with --id
// near the group g: its own visible commands whose first argument is
// --id (just show when it has one), else its parent's `show` (`lore task tree tsk_…` →
// `lore task show`).
func idCommands(g *cobra.Command) []string {
	if out := idLeaves(g); len(out) > 0 {
		return out
	}
	if p := g.Parent(); p != nil && p.HasParent() {
		for _, c := range idLeaves(p) {
			if strings.HasSuffix(c, " show") {
				return []string{c}
			}
		}
	}
	return nil
}

// idLeaves returns the paths of g's visible generated commands whose
// first positional stands for --id: only `show` when g has one, else at
// most maxIDCommands of them.
func idLeaves(g *cobra.Command) []string {
	var out []string
	for _, c := range g.Commands() {
		if !ours(c) || c.Hidden || c.HasSubCommands() {
			continue
		}
		first, _, _ := strings.Cut(c.Annotations[AnnotationArgFlags], ",")
		if first != "id" {
			continue
		}
		out = append(out, c.CommandPath())
	}
	for _, c := range out {
		if strings.HasSuffix(c, " show") { // the one a value most often meant
			return []string{c}
		}
	}
	return out[:min(len(out), maxIDCommands)]
}
