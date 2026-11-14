package lookup

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// OnTable is the --on-table of a command that targets a row by --on-id
// (comment add, tag attach and detach, reminder add): rawTable when
// given, else the table the id's prefix names (devco:J-8: `comment add
// --on-id tsk_…` was refused "missing required field on_table" and the
// hint did not say tasks; `lore show <id>` already finds the table from
// the prefix). verb names the command for the errors ("comment add").
// rawID empty with rawTable empty returns "" and no error: the caller's
// own required check answers.
func OnTable(verb, rawTable, rawID string) (string, error) {
	if t := strings.TrimSpace(rawTable); t != "" {
		return t, nil
	}
	raw := strings.TrimSpace(rawID)
	if raw == "" {
		return "", nil
	}
	id, err := ids.ParseAny(raw)
	if err != nil {
		return "", BadID(verb+": --on-id "+Quote(raw)+" is not an id", err).
			WithHint("copy the row's whole id from the list or show output of its kind")
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return "", errs.Wrap(errs.Internal, verb+": built-in schema", err)
	}
	def, ok := reg.ByPrefix(id.Prefix())
	if !ok {
		return "", errs.New(errs.InvalidInput, verb+": "+id.String()+" names no built-in table, so --on-table is needed").
			WithHint("pass --on-table <table> with --on-id " + id.String())
	}
	return string(def.Name), nil
}

// maxQuotedRaw bounds a user-given value repeated in an error.
const maxQuotedRaw = 48

// Quote renders a user-given value for an error, truncated.
func Quote(s string) string {
	if len(s) > maxQuotedRaw {
		s = s[:maxQuotedRaw] + "…"
	}
	return `"` + s + `"`
}
