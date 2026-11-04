package tuigen

import (
	"context"
	"strings"
	"unicode"

	"dbent/gen/ent"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
)

// GuardCoreOwned makes the TUI read-only for the tables the 0.1.18 core
// owns (lsync.CoreOwned): their lore.db rows are a mirror of the core's
// row files, so a TUI create, edit or delete there would never reach the
// files (lsync does not export core-owned rows) and would be undone by the
// next import. Every such mutation on client is refused with a message
// naming the CLI command that writes the row. Not generated (enttui
// regenerates the register_*.go files only).
func GuardCoreOwned(client *ent.Client) {
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if table := tableOfType(m.Type()); lsync.CoreOwned(table) {
				return nil, errcodes.New(errcodes.Unsupported, table+" are written by the 0.1.18 core; the TUI shows them read-only").
					WithHint(hintOfType(m.Type()))
			}
			return next.Mutate(ctx, m)
		})
	})
}

// tableOfType is ent's table name for a type name (snake case, plural):
// Comment → comments, TaskList → task_lists, Memory → memories, Hotfix →
// hotfixes.
func tableOfType(t string) string {
	s := snake(t)
	switch {
	case strings.HasSuffix(s, "y"):
		return strings.TrimSuffix(s, "y") + "ies"
	case strings.HasSuffix(s, "x"):
		return s + "es"
	}
	return s + "s"
}

// cliHintOverrides names the real CLI command for the core-owned types whose
// command is not `lore <lowercased type> add|edit|delete` (the default of
// hintOfType). Keys are ent type names (ent.Type*); each value is the whole
// hint. Checked against the command tree by the tuigen tests, so a renamed
// command shows up as a failing case instead of a hint naming nothing.
var cliHintOverrides = map[string]string{
	"Actor":           "use the CLI: `lore actor register|deactivate …`",
	"CommitLink":      "use the CLI: `lore link …`",
	"ExternalSource":  "use the CLI: `lore external-source add|edit|delete …`",
	"KnowledgeRef":    "use the CLI: `lore task add|edit --ref …`",
	"Project":         "use the CLI: `lore project …`",
	"Repo":            "use the CLI: `lore repo add|archive|delete …`",
	"RuleVerifierRef": "no lore command writes rule verifier refs; they arrive with the rule's row files",
	"Team":            "use the CLI: `lore team add …`",
}

// hintOfType is the refusal hint for a core-owned type: the CLI command that
// writes it (TaskList → `lore tasklist add|edit|delete …`, overrides in
// cliHintOverrides).
func hintOfType(t string) string {
	if h, ok := cliHintOverrides[t]; ok {
		return h
	}
	return "use the CLI: `lore " + strings.ToLower(t) + " add|edit|delete …`"
}

// snake turns CamelCase into snake_case.
func snake(t string) string {
	var b strings.Builder
	for i, r := range t {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
