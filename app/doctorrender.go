package app

// doctorrender.go — the context rows of `lore doctor` on cmd/lore
// (CONTEXT_BUDGET 6.11, CQ7, CQ8, 6.7, 8.6, 8.9), as the old binary printed
// them through renderhost.ContextDoctor: .lore/LORE.md against its budget,
// the launch set per @import target, Claude Code's measured startup size
// levels, double loads, more than 20 folder files and the target
// suggestion. wire cannot import renderhost, so an Around hook on
// doctor.run appends them to the human text; --json is unchanged.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// hookDoctorRender names the hook in errors and tests.
const hookDoctorRender = "doctor-render"

// doctorRenderHook runs after a successful `lore doctor` and appends the
// context warnings and notes for the checkout at wd.
//
// The rows are about the lore project the command runs in, read at its
// root (wd may be a subfolder of it); outside a project (a plain folder, a
// bare repository, a repo lore was never set up in) there is no LORE.md to
// measure, so there are no rows: "not rendered yet (lore render)" there
// sent the person to a command that cannot work in that folder.
func doctorRenderHook(rend *lateRender, wd, home string) hosthooks.Hook {
	return hosthooks.Hook{Name: hookDoctorRender, Around: func(ctx context.Context, run *hosthooks.Run, next hosthooks.Next) (op.Result, error) {
		res, err := next(ctx)
		if err != nil || run.Entry.Spec().Name != opsdoctor.OpRun || rend.h == nil {
			return res, err
		}
		root, ok := orgcfg.ProjectRoot(ctx, wd, home)
		if !ok {
			return res, nil
		}
		warnings, notes := rend.h.ContextDoctor(ctx, root)
		if len(warnings)+len(notes) == 0 {
			return res, nil
		}
		return res.WithTextEdit(func(s string) string {
			var b strings.Builder
			b.WriteString(s)
			if s != "" && !strings.HasSuffix(s, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\nContext:\n")
			for _, w := range warnings {
				b.WriteString("  WARN " + w + "\n")
			}
			for _, n := range notes {
				b.WriteString("  - " + n + "\n")
			}
			return b.String()
		}), nil
	}}
}
