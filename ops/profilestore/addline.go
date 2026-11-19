package profilestore

// addline.go — the lines a member is sent to when the write guard refuses
// an add outside the folder of a team they are on (devco D-694): the
// admin's own add with the full --to <org>/<folder> --yes, and the roster
// command that puts the member on the folder's team, so their own add then
// passes. Both work exactly as printed.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// orgFolderName is the company store's org folder: no team owns it.
const orgFolderName = "org"

// addKinds is the command word of each table a guarded add writes.
var addKinds = map[row.Table]string{
	schema.TableRules:     "rule",
	schema.TableHotfixes:  "hotfix",
	schema.TableMemories:  "memory",
	schema.TableDecisions: "decision",
}

// Row fields an add line carries, in the order they are printed.
var addFlagFields = []string{"title", "body", "severity"}

// addRowCommand is the admin's `lore <kind> add --title … --body …
// --severity … --to <to> --yes` that writes the row w adds; ok is false
// for a table no add command writes or a row with no body.
func addRowCommand(w engine.GuardedWrite, to string) (string, bool) {
	kind, ok := addKinds[w.After.Table]
	if !ok || fieldText(w.After, "body") == "" {
		return "", false
	}
	c := "lore " + kind + " add"
	for _, f := range addFlagFields {
		if v := fieldText(w.After, f); v != "" {
			c += " --" + f + " " + shellWord(v)
		}
	}
	return c + " --to " + to + " --yes", true
}

// teamLine is the roster command an admin runs to put the caller on the
// team of the company folder named folder (a team's folder is the folder
// named like its handle): `lore team member add` when that team exists,
// `lore team add` making it otherwise. ok is false for the org folder and
// a store root, which no team owns.
func teamLine(ctx context.Context, r Roster, folder string) (string, bool) {
	if folder == "" || folder == orgFolderName {
		return "", false
	}
	me := callerName(ctx, r)
	handle := "@" + folder
	teams, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return "", false
	}
	for _, t := range teams {
		if strings.TrimPrefix(fieldText(t, "handle"), "@") == folder && fieldText(t, "left_at") == "" {
			return "to put you on team " + handle + ", whose folder it is: lore team member add --handle " + handle + " --members " + me, true
		}
	}
	return "to make team " + handle + " for that folder with you on it: lore team add --handle " + handle + " --members " + me, true
}

// callerName is the caller as `--members` takes them: their @handle, else
// their act_ id.
func callerName(ctx context.Context, r Roster) string {
	id := r.Caller()
	aid, err := ids.ParseAny(id.String())
	if err == nil {
		if a, gerr := r.Get(ctx, schema.TableActors, aid); gerr == nil {
			if h := strings.TrimPrefix(fieldText(a, schema.FieldActorHandle), "@"); h != "" {
				return "@" + h
			}
		}
	}
	return id.String()
}
