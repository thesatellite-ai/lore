package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestUnknownWordValueHint: A-11, a value typed where a command word goes
// (`lore commit HEAD`, `lore task tree tsk_…`) names the command that
// takes it with --id; a mistyped command word keeps the bare refusal.
func TestUnknownWordValueHint(t *testing.T) {
	root := &cobra.Command{Use: RootName}
	mk := func(parent *cobra.Command, name, argflags string, sub bool) *cobra.Command {
		c := &cobra.Command{Use: name, Annotations: map[string]string{annotationOwner: annotationValue, AnnotationArgFlags: argflags}}
		if !sub {
			c.Run = func(*cobra.Command, []string) {}
		}
		parent.AddCommand(c)
		return c
	}
	commit := mk(root, "commit", "", true)
	mk(commit, "show", "id", false)
	task := mk(root, "task", "", true)
	mk(task, "cancel", "id", false)
	mk(task, "show", "id", false)
	mk(task, "add", "title...", false)
	tree := mk(task, "tree", "", true)
	mk(tree, "check", "", false)
	for _, tc := range []struct {
		g          *cobra.Command
		word, want string
	}{
		{commit, "HEAD", "lore commit show"},
		{commit, "HEAD~2", "lore commit show"},
		{commit, "3f2c9ab", "lore commit show"},
		{tree, "tsk_01a1d618daf47d04abc92373b13dcd17", "lore task show"},
		{task, "tsk_01a1d618daf47d04abc92373b13dcd17", "lore task show"},
	} {
		if !looksLikeValue(tc.word) {
			t.Errorf("%q: not read as a value", tc.word)
		}
		if got := idCommands(tc.g); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s %s: %q (want %q)", tc.g.CommandPath(), tc.word, got, tc.want)
		}
	}
	for _, w := range []string{"bogus", "shwo", "list"} {
		if looksLikeValue(w) {
			t.Errorf("%q read as a value: a typo would get a wrong hint", w)
		}
	}
}

// TestNearCommands: C-13, a word that names no command is answered with
// the commands of that name deeper down, else the near spellings.
func TestNearCommands(t *testing.T) {
	root := &cobra.Command{Use: RootName}
	add := func(parent *cobra.Command, name string) *cobra.Command {
		c := &cobra.Command{Use: name, Run: func(*cobra.Command, []string) {}}
		parent.AddCommand(c)
		return c
	}
	migrate := add(root, "migrate")
	add(migrate, "status")
	memory := add(root, "memory")
	add(memory, "list")
	add(root, "doctor")
	for word, want := range map[string]string{"status": "lore migrate status", "memroy": "lore memory", "doctr": "lore doctor"} {
		if got := nearCommands(root, word); len(got) != 1 || got[0] != want {
			t.Errorf("%q: %q (want %q)", word, got, want)
		}
	}
	if got := nearCommands(memory, "lst"); len(got) != 1 || got[0] != "lore memory list" {
		t.Errorf("memory lst: %q", got)
	}
	if got := nearCommands(root, "zznosuchword"); len(got) != 0 {
		t.Errorf("no near command: %q", got)
	}
}
