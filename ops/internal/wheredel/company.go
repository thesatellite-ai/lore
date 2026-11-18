package wheredel

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// CompanyGuard is the Spec.Guard of a noun whose company rows only an
// admin may take out (rule: must; hotfix: critical/high; devco D-659):
// the bulk form refuses, nothing written, when any match is one and the
// caller is not an admin of the org (profilestore.RequireAdminForRows).
// verb is "delete" or "archive"; keep is the --where term that leaves the
// company rows out ("severity:should,may"), offered as the member's own
// way on. The admin's line repeats the member's --where.
func CompanyGuard(env profilestore.PlaceEnv, noun, verb string, where []string, keep string) func(ctx context.Context, hits []Located) error {
	return func(ctx context.Context, hits []Located) error {
		rows := make([]profilestore.Placed, 0, len(hits))
		for _, h := range hits {
			rows = append(rows, profilestore.Placed{Source: h.Source, Row: h.Row})
		}
		line := Line(noun, verb, where) + " --yes"
		narrow := "add --where " + shellQuote(keep) + " to " + verb + " only the rest: " + Line(noun, verb, append(append([]string{}, where...), keep)) + " --dry-run"
		return profilestore.RequireAdminForRows(ctx, env, rows, verb, line, narrow)
	}
}

// Line is the `lore <noun> <verb> --where '…' …` command for where, each
// term quoted for a POSIX shell.
func Line(noun, verb string, where []string) string {
	var b strings.Builder
	b.WriteString("lore " + noun + " " + verb)
	for _, w := range where {
		b.WriteString(" --where " + shellQuote(w))
	}
	return b.String()
}

// shellQuote single-quotes s for a POSIX shell ('…', a ' inside as '\”).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
