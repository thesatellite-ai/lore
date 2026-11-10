package wire

// readonly_notice.go — FORMAT_EVOLUTION rule 2 on reads ("read commands
// print one line on stderr and succeed"): the line a read command prints
// when this lore may not write the checkout's project (an unknown feature
// or a floor above this binary in .lore/data/_meta.json). Writes are
// refused by the engine's format gate (engine/format.go); this is only
// the read side. A host prints it once per command on stderr, never on
// stdout, so --json stays clean (the old binary: format_gate.go).

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/formatgate"
)

// upgradeBinary is the command the notice's fix names (`lore upgrade`).
const upgradeBinary = "lore"

// ReadOnlyNotice is the read-only line for this checkout, "" when this
// lore may write it, outside a project, before `lore init` wrote
// _meta.json (FMT-ROOT: nothing declared, nothing gated), or when the file
// cannot be read (that is the write gate's fail-closed E_OLD_LORE, not a
// read command's business).
func (a *App) ReadOnlyNotice(ctx context.Context) string {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return ""
	}
	d, exists, err := rowfs.ReadRepoFormat(top)
	if err != nil || !exists {
		return ""
	}
	return formatgate.ReadOnlyLine(formatgate.Check(a.opts.Version, d), upgradeBinary)
}
