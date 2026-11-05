// move_cmd.go — `lore move <id> --to <target>` (BATCH 2: the one command
// that gives a row to another place; `lore share` is removed, a row has one
// home). Rows of core nouns move on the 0.1.18 core and keep their id
// (runCoreMove, PROV5). Rows still on the v0.1.17 path are written to the
// target under a NEW id with no pointer back (§6.4), then the original is
// deleted. Lore shows the text and asks first.
//
//	runMove ─┬─ core table ─▶ runCoreMove (row_shortcut.go)
//	         └─ v0.1.17 ─▶ findMoveSource ─▶ routeWrite (write-time leak
//	              check) ─▶ ask ─▶ copyForMove ─▶ leakCheckMoveCopy ─▶ write
//	              ─▶ deleteMoved
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/stores"

	"github.com/spf13/cobra"
)

// Fields a moved v0.1.17 row never carries: they would point back at the
// narrower row or its project, or carry the old row's live state. The names
// are the real columns (dbent/schema: superseded_by_id, source_ref, …).
var moveDropFields = []string{"_live", "superseded_by_id", "source_ref", "origin_id", "origin_url", "repo_id"}

// Actor columns of a moved v0.1.17 row (§6.4: no origin fields). The new
// row is the mover's statement, so created_by_actor_id names the mover; every other
// *_actor_id / *_actor_ids column (validated by, reviewed by, assigned to,
// members …) would tell the wider audience who handled the original, so it
// is cleared.
const (
	moveCreatorField     = "created_by_actor_id"
	moveActorFieldSuffix = "_actor_id"
	moveActorListSuffix  = "_actor_ids"
)

// moveFlags are the flags of move.
type moveFlags struct {
	to  toFlags
	yes bool
	// rows of core nouns (CLI_REFERENCE 2.7): --drop-internal
	// (MQ11, BATCH 7) and --with-time totals|none (TQ3).
	dropInternal bool
	withTime     string
	withChildren bool // --with-children: a task with its subtree (TASK 6.14, B7)
	// asJSON (CLI_REFERENCE 2.7 --json): the core move result as
	// JSON on stdout; the preview line goes to stderr so stdout parses.
	asJSON bool
}

func newMoveCommand() *cobra.Command {
	f := &moveFlags{}
	cmd := &cobra.Command{
		Use:   "move <id> --to <target>",
		Short: "Move a row to another store (same id for rows on the core; asks first)",
		Example: `  lore move mem_0b77 --to agency
  lore move tsk_0201 --to repo --drop-internal`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMove(cmd.Context(), args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.to.To, flagTo, "", "the target: repo | <org> | <org>/<folder> | me (required)")
	cmd.Flags().BoolVar(&f.yes, flagYes, false, "confirm without a prompt")
	cmd.Flags().BoolVar(&f.dropInternal, flagDropInternal, false, "into a client repo: remove the row's internal metadata values instead of refusing (MQ11)")
	cmd.Flags().StringVar(&f.withTime, flagWithTime, "", "totals | none (default none): time totals for a task moved into a client repo (TQ3; totals not built yet)")
	cmd.Flags().BoolVar(&f.withChildren, "with-children", false, "a task: move its whole subtree too; every sub-task keeps its id and parent (TASK 6.14)")
	cmd.Flags().BoolVar(&f.asJSON, flagJSONOut, false, "print the result as JSON (rows on the 0.1.18 core)")
	return cmd
}

// moveSource is the row being moved and where it lives.
type moveSource struct {
	table, id string
	doc       map[string]any
	// file is its .lore/data file (checkout or mirror); "" when it is only
	// on a live ref (its owner's live writer must delete it).
	file string
	// project is the external project holding it (nil = this checkout).
	project *externalProject
}

func runMove(ctx context.Context, id string, f *moveFlags) error {
	if table, ok := coreTableOf(id); ok {
		// Core-owned rows are written only by the 0.1.18 core: a move
		// keeps the id (PROV5).
		return runCoreMove(ctx, table, id, f)
	}
	if f.dropInternal || f.withTime != "" || f.withChildren || f.asJSON {
		return errcodes.New(errcodes.Unsupported, "--drop-internal, --with-time, --with-children and --json apply to rows on the 0.1.18 core only; "+id+" still moves the v0.1.17 way")
	}
	if f.to.To == "" {
		return errcodes.New(errcodes.InvalidInput, "--to is required (repo, <org>, <org>/<folder> or me)")
	}
	me, err := resolveIdentityHere(ctx)
	if err != nil {
		return err
	}
	src, err := findMoveSource(ctx, id)
	if err != nil {
		return err
	}
	var c commonFlags
	// The moved row is meant to carry its own text: routeWrite's
	// write-time leak check allows exactly this row (leak_write.go).
	route, err := routeWrite(withMovedSource(ctx, id), &c, f.to, singularKind(src.table), rowText(src.doc))
	if err != nil {
		return err
	}
	who := "everyone with access to " + targetAudience(route)
	fmt.Printf("This becomes visible to %s:\n  %q\n", who, oneLine(rowText(src.doc)))
	// An agent confirms like a person (FA-Q-2, OQ25 agents override: the
	// agent asks its human in its own conversation; lore does not block),
	// exactly as the core move does.
	if !f.yes {
		if !stdinIsTTY() {
			return errcodes.New(errcodes.InvalidInput, "not moved: confirm on a terminal (or --yes); nothing written")
		}
		if !confirmYes("Move it? [y/N] ") {
			fmt.Println("nothing written")
			return nil
		}
	}
	newID, err := ids.New(strings.SplitN(id, "_", 2)[0])
	if err != nil {
		return errcodes.New(errcodes.Internal, "mint id").WithCause(err)
	}
	targetRoot, targetPID, err := routeRoot(route)
	if err != nil {
		return err
	}
	mover := actorIDFor(me.StableKey)
	doc := copyForMove(src.doc, newID, targetPID, mover, cliClock().Now())
	body, err := canonjson.Encode(doc)
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode the moved row").WithCause(err)
	}
	if err := checkReferences(ctx, route, docRefs(doc, newID)); err != nil {
		return err
	}
	if route.Repo {
		if err := leakCheckMoveCopy(ctx, id, filepath.ToSlash(filepath.Join(".lore/data", src.table, newID+".json")), body); err != nil {
			return err
		}
	}
	dst := filepath.Join(targetRoot, projresolve.MarkerDir, lsync.DataDirName, src.table, newID+".json")
	if err := os.MkdirAll(filepath.Dir(dst), externalDirMode); err != nil {
		return errcodes.New(errcodes.Internal, "write the moved row").WithCause(err)
	}
	if err := atomicWriteFile(dst, body, externalFileMode); err != nil {
		return errcodes.New(errcodes.Internal, "write the moved row").WithCause(err)
	}
	if route.Repo {
		if err := ensureMoverProjection(targetRoot, mover, me.DisplayName); err != nil {
			return err
		}
	}
	if route.Project != nil {
		if err := route.Project.importFiles(ctx); err != nil {
			return err
		}
	}
	if err := deleteMoved(ctx, src); err != nil {
		return err
	}
	label := route.Label
	if label == "" {
		label = "this repo"
	}
	fmt.Printf("✓ %s moved from %s, in %s (the copy has no pointer back)\n", newID, id, redactIfQuiet(label))
	return nil
}

// targetAudience names who can read the target, for the prompt.
func targetAudience(route writeRoute) string {
	switch {
	case route.Repo && route.Situation != nil && len(route.Situation.Remotes) > 0:
		return strings.Join(route.Situation.Remotes, ", ")
	case route.Repo:
		return "this repo"
	case route.Label == personalLabel:
		return "only you (personal)"
	}
	return redactIfQuiet(route.Label)
}

// routeRoot returns the root of the routed project and its project id.
func routeRoot(route writeRoute) (string, string, error) {
	root := ""
	if route.Project != nil {
		root = route.Project.Root
	} else {
		d, err := os.Getwd()
		if err != nil {
			return "", "", errcodes.New(errcodes.Internal, "getwd").WithCause(err)
		}
		root = d
	}
	m, err := lsync.ReadProjectMeta(filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName))
	if err != nil {
		return "", "", errcodes.New(errcodes.NotProjectRoot, "the target project has no _meta.json").WithCause(err)
	}
	return root, m.ProjectID, nil
}

// copyForMove is a v0.1.17 row as written to the move target: new id, the
// target's project id, fresh timestamps, none of moveDropFields, the mover
// as its creator and no other actor column (see moveCreatorField).
func copyForMove(doc map[string]any, newID, projectID, mover string, now time.Time) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	for _, k := range moveDropFields {
		delete(out, k)
	}
	for k := range out {
		switch {
		case k == moveCreatorField:
		case strings.HasSuffix(k, moveActorFieldSuffix):
			out[k] = nil
		case strings.HasSuffix(k, moveActorListSuffix):
			out[k] = []any{}
		}
	}
	if _, ok := out[moveCreatorField]; ok {
		// Only tables that have the column: an unknown key would land in
		// the row's extra fields.
		if mover == "" {
			out[moveCreatorField] = nil
		} else {
			out[moveCreatorField] = mover
		}
	}
	out["id"] = newID
	if _, ok := out["project_id"]; ok && projectID != "" {
		out["project_id"] = projectID
	}
	ts := now.UTC().Format(time.RFC3339Nano)
	out["created_at"], out["updated_at"] = ts, ts
	return out
}

// docRefs lists the row ids a document points at (string values shaped
// like ids, other than its own), for the F15 direction check.
func docRefs(doc map[string]any, self string) []string {
	var out []string
	for k, v := range doc {
		s, ok := v.(string)
		if !ok || k == "id" || k == "project_id" || s == self {
			continue
		}
		if rowIDRe.MatchString(s) {
			out = append(out, s)
		}
	}
	return out
}

// rowIDRe matches one lore row id (prefix_ + 32 hex, ids.go).
var rowIDRe = regexp.MustCompile(`^[a-z]{3}_[0-9a-f]{32}$`)

// rowText is a row's human text (title and body).
func rowText(doc map[string]any) string {
	var parts []string
	for _, k := range []string{"title", "body", "content"} {
		if s, ok := doc[k].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " — ")
}

// singularKind turns a table name into the kind defaults use.
func singularKind(table string) string {
	switch {
	case strings.HasSuffix(table, "ies"):
		return strings.TrimSuffix(table, "ies") + "y"
	case strings.HasSuffix(table, "s"):
		return strings.TrimSuffix(table, "s")
	}
	return table
}

// findMoveSource locates id in this checkout's .lore/data or, failing
// that, in an external source loaded here (store or personal).
func findMoveSource(ctx context.Context, id string) (*moveSource, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	if r, ok := findInData(filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName), id); ok {
		return r, nil
	}
	rows, _ := externalRowsHere(ctx, cachedb.Query{})
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		p, err := openSourceProject(ctx, row.Source)
		if err != nil {
			return nil, err
		}
		r, ok := findInData(filepath.Join(p.Root, projresolve.MarkerDir, lsync.DataDirName), id)
		if !ok {
			var doc map[string]any
			if err := json.Unmarshal(row.Doc, &doc); err != nil {
				return nil, errcodes.New(errcodes.Internal, "read "+id).WithCause(err)
			}
			r = &moveSource{table: row.Table, id: id, doc: doc}
		}
		r.project = p
		return r, nil
	}
	return nil, errcodes.New(errcodes.NotFound, id+" was not found here (this repo, its linked projects, global or personal)")
}

// findInData finds <table>/<id>.json under a data dir.
func findInData(data, id string) (*moveSource, bool) {
	matches, _ := filepath.Glob(filepath.Join(data, "*", id+".json"))
	if len(matches) != 1 {
		return nil, false
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, false
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		return nil, false
	}
	return &moveSource{table: filepath.Base(filepath.Dir(matches[0])), id: id, doc: doc, file: matches[0]}, true
}

// openSourceProject opens the external project a cache source names.
func openSourceProject(ctx context.Context, source string) (*externalProject, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return nil, err
	}
	if folder, ok := strings.CutPrefix(source, cachedb.SourcePersonal+":"); ok {
		return openPersonalProject(ctx, s.Config, folder)
	}
	rest, _ := strings.CutPrefix(source, cachedb.SourceStore+":")
	orgName, folder, _ := strings.Cut(rest, "/")
	org := s.Config.FindOrg(orgName)
	if org == nil {
		return nil, errcodes.New(errcodes.InvalidInput, "source "+source+" names no org entry")
	}
	ref, err := findStoreFolder(ctx, s.Config, org, folder)
	if err != nil {
		return nil, err
	}
	return openStoreProject(ctx, s.Config, ref)
}

// deleteMoved removes the original after a move (a file delete; the
// project's flush or the checkout's next sync carries it).
func deleteMoved(ctx context.Context, src *moveSource) error {
	if src.file == "" {
		return errcodes.New(errcodes.NotImplemented, src.id+" exists only on a live ref; delete it with `lore "+singularKind(src.table)+" delete` after the move")
	}
	if err := os.Remove(src.file); err != nil {
		return errcodes.New(errcodes.Internal, "delete the original").WithCause(err)
	}
	if src.project != nil {
		return src.project.importFiles(ctx)
	}
	return nil
}

// importFiles brings files written straight into a mirror's .lore/data into
// its DB (a full pass), so the flush exports nothing stale over them.
func (p *externalProject) importFiles(ctx context.Context) error {
	_, err := p.pass(ctx, false)
	return err
}

// ensureMoverProjection makes sure the client repo holds an actor file for
// the person a moved v0.1.17 row names (created_by_actor_id), so every clone can
// resolve the name. It is written as the F18 client projection {id, display
// name, kind} and never replaces an existing file: the export keeps an
// existing one projected (client_actor_export.go).
func ensureMoverProjection(root, actorID, displayName string) error {
	if actorID == "" {
		return nil
	}
	path := filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName, tableActors, actorID+".json")
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	doc := stores.ClientActorProjection(map[string]any{
		"_table": tableActors, "_v": lsync.FormatVersion, "id": actorID,
		"display_name": displayName, "kind": identityKindHuman,
	}, nil, "", false)
	b, err := canonjson.Encode(doc)
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode the actor projection").WithCause(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), externalDirMode); err != nil {
		return errcodes.New(errcodes.Internal, "write the actor projection").WithCause(err)
	}
	if err := atomicWriteFile(path, b, externalFileMode); err != nil {
		return errcodes.New(errcodes.Internal, "write the actor projection").WithCause(err)
	}
	return nil
}
