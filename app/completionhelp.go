package app

// completionhelp.go — cobra's generated `lore completion` commands carry
// long prose but no Examples block, so `lore completion --help` was the one
// help page in the tree without a line to copy. exampleCompletion adds one
// to the group and to each shell, after InitDefaultCompletionCmd made them.

import "github.com/spf13/cobra"

// completionExamples is the Examples block of each shell's command; the
// group shows the bash and zsh one-liners (the shells most people run).
var completionExamples = map[string]string{
	"bash":       "  # this shell only\n  source <(lore completion bash)\n  # every new shell (Linux)\n  lore completion bash > /etc/bash_completion.d/lore",
	"zsh":        "  # this shell only\n  source <(lore completion zsh)\n  # every new shell (compinit must be on)\n  lore completion zsh > \"${fpath[1]}/_lore\"",
	"fish":       "  # this shell only\n  lore completion fish | source\n  # every new shell\n  lore completion fish > ~/.config/fish/completions/lore.fish",
	"powershell": "  # this shell only\n  lore completion powershell | Out-String | Invoke-Expression",
}

// exampleCompletion sets the Examples of `lore completion` and its shells;
// a tree without the completion command (disabled) is left alone.
func exampleCompletion(root *cobra.Command) {
	for _, c := range root.Commands() {
		if c.Name() != "completion" {
			continue
		}
		c.Example = "  # bash, this shell only\n  source <(lore completion bash)\n  # zsh, this shell only\n  source <(lore completion zsh)"
		for _, sh := range c.Commands() {
			if ex, ok := completionExamples[sh.Name()]; ok {
				sh.Example = ex
			}
		}
	}
}
