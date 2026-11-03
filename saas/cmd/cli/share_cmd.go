// share_cmd.go — `lore share <id> --to <target>` and `lore move <id> --to
// <target>` (LORE_TEAM_SPEC §6.4, guard 4): widening who can read a row is
// one deliberate step. Lore shows the text and asks; agents can never pass
// --yes (a prompt injected into a client-writable row cannot make an agent
// share); the copy gets a NEW id and no pointer back to the narrower row,
// so the id rule never fires on a shared copy and the wider audience
// learns nothing about the original. `move` also deletes the original.
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

// Fields a shared copy never carries: they would point back at the
// narrower row or its project, or carry the old row's live state. The names
// are the real columns (dbent/schema: superseded_by_id, source_ref, …).
var shareDropFields = []string{"_live", "superseded_by_id", "source_ref", "origin_id", "origin_url", "repo_id"}

// Actor columns of a shared copy (§6.4: no origin fields). The copy is the
// sharer's statement, so created_by_actor_id names the sharer; every other
// *_actor_id / *_actor_ids column (validated by, reviewed by, assigned to,
// members …) would tell the wider audience who handled the original, so it
// is cleared.
const (
	shareCreatorField     = "created_by_actor_id"
	shareActorFieldSuffix = "_actor_id"
	shareActorListSuffix  = "_actor_ids"
)

// shareFlags are the flags of share and move.
type shareFlags struct {
	to  toFlags
	yes bool
}

func newShareCommand() *cobra.Command { return newShareLikeCommand(false) }
func newMoveCommand() *cobra.Command  { return newShareLikeCommand(true) }

func newShareLikeCommand(move bool) *cobra.Command {
	f := &shareFlags{}
	use, short, example := "share <id> --to <target>", "Copy a row to a wider audience (new id; shows the text and asks)",
		`  lore share mem_0c12 --to repo
  lore share mem_77 --to agency`
	if move {
		use, short, example = "move <id> --to <target>", "Move a row to another project (new id; the original is deleted; asks first)",
			`  lore move mem_0b77 --to agency`
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Example: example,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShare(cmd.Context(), args[0], f, move)
		},
	}
	cmd.Flags().StringVar(&f.to.To, flagTo, "", "the target: repo | <org> | <org>/<folder> | me (required)")
	cmd.Flags().BoolVar(&f.yes, flagYes, false, "confirm without a prompt (never accepted from agent actors)")
	return cmd
}

// sharedRow is the row being shared and where it lives.
type sharedRow struct {
	table, id string
	doc       map[string]any
	// file is its .lore/data file (checkout or mirror); "" when it is only
	// on a live ref (its owner's live writer must delete it for move).
	file string
	// project is the external project holding it (nil = this checkout).
	project *externalProject
}

func runShare(ctx context.Context, id string, f *shareFlags, move bool) error {
	if f.to.To == "" {
		return errcodes.New(errcodes.InvalidInput, "--to is required (repo, <org>, <org>/<folder> or me)")
	}
	me, err := resolveIdentityHere(ctx)
	if err != nil {
		return err
	}
	if f.yes && me.Kind == identityKindAgent {
		return errcodes.New(errcodes.InvalidInput, "agents cannot confirm a share with --yes; a person must run it (§6.4)").
			WithHint("ask the owner to run `lore share " + id + " --to " + f.to.To + "`")
	}
	src, err := findRowForShare(ctx, id)
	if err != nil {
		return err
	}
	var c commonFlags
	route, err := routeWrite(ctx, &c, f.to, singularKind(src.table), rowText(src.doc))
	if err != nil {
		return err
	}
	if route.Repo && src.project == nil && !move {
		return errcodes.New(errcodes.InvalidInput, id+" is already in this repo")
	}
	who := "everyone with access to " + targetAudience(route)
	fmt.Printf("This becomes visible to %s:\n  %q\n", who, oneLine(rowText(src.doc)))
	if !f.yes {
		if !stdinIsTTY() || me.Kind == identityKindAgent {
			return errcodes.New(errcodes.InvalidInput, "not shared: confirm on a terminal (or --yes, people only); nothing written")
		}
		verb := "Share"
		if move {
			verb = "Move"
		}
		if !confirmYes(verb + " it? [y/N] ") {
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
	sharer := actorIDFor(me.StableKey)
	doc := copyForShare(src.doc, newID, targetPID, sharer, cliClock().Now())
	body, err := canonjson.Encode(doc)
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode the copy").WithCause(err)
	}
	if err := checkReferences(ctx, route, docRefs(doc, newID)); err != nil {
		return err
	}
	if route.Repo {
		if err := leakCheckDocs(ctx, "share", map[string][]byte{filepath.ToSlash(filepath.Join(".lore/data", src.table, newID+".json")): body}); err != nil {
			return err
		}
	}
	dst := filepath.Join(targetRoot, projresolve.MarkerDir, lsync.DataDirName, src.table, newID+".json")
	if err := os.MkdirAll(filepath.Dir(dst), externalDirMode); err != nil {
		return errcodes.New(errcodes.Internal, "write the copy").WithCause(err)
	}
	if err := atomicWriteFile(dst, body, externalFileMode); err != nil {
		return errcodes.New(errcodes.Internal, "write the copy").WithCause(err)
	}
	if route.Repo {
		if err := ensureSharerProjection(targetRoot, sharer, me.DisplayName); err != nil {
			return err
		}
	}
	if route.Project != nil {
		if err := route.Project.importFiles(ctx); err != nil {
			return err
		}
	}
	if !move && src.project != nil {
		if err := recordShareProvenance(ctx, src, newID); err != nil {
			return err
		}
	}
	verb := "shared copy of"
	if move {
		verb = "moved from"
		if err := deleteShared(ctx, src); err != nil {
			return err
		}
	}
	label := route.Label
	if label == "" {
		label = "this repo"
	}
	note := "the copy has no pointer back"
	if !move && src.project != nil {
		note += "; the original records the copy"
	}
	fmt.Printf("✓ %s %s %s, in %s (%s)\n", newID, verb, id, redactIfQuiet(label), note)
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

// copyForShare is the shared copy: new id, the target's project id, fresh
// timestamps, none of shareDropFields, the sharer as its creator and no
// other actor column (see shareCreatorField).
func copyForShare(doc map[string]any, newID, projectID, sharer string, now time.Time) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	for _, k := range shareDropFields {
		delete(out, k)
	}
	for k := range out {
		switch {
		case k == shareCreatorField:
		case strings.HasSuffix(k, shareActorFieldSuffix):
			out[k] = nil
		case strings.HasSuffix(k, shareActorListSuffix):
			out[k] = []any{}
		}
	}
	if _, ok := out[shareCreatorField]; ok {
		// Only tables that have the column: an unknown key would land in
		// the row's extra fields.
		if sharer == "" {
			out[shareCreatorField] = nil
		} else {
			out[shareCreatorField] = sharer
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

// findRowForShare locates id in this checkout's .lore/data or, failing
// that, in an external source loaded here (store or personal).
func findRowForShare(ctx context.Context, id string) (*sharedRow, error) {
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
			r = &sharedRow{table: row.Table, id: id, doc: doc}
		}
		r.project = p
		return r, nil
	}
	return nil, errcodes.New(errcodes.NotFound, id+" was not found here (this repo, its linked projects, global or personal)")
}

// findInData finds <table>/<id>.json under a data dir.
func findInData(data, id string) (*sharedRow, bool) {
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
	return &sharedRow{table: filepath.Base(filepath.Dir(matches[0])), id: id, doc: doc, file: matches[0]}, true
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

// deleteShared removes the original after a move (a file delete; the
// project's flush or the checkout's next sync carries it).
func deleteShared(ctx context.Context, src *sharedRow) error {
	if src.file == "" {
		return errcodes.New(errcodes.NotImplemented, src.id+" exists only on a live ref; delete it with `lore "+singularKind(src.table)+" delete` after the copy")
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

// ensureSharerProjection makes sure the client repo holds an actor file for
// the person a shared copy names (created_by_actor_id), so every clone can
// resolve the name. It is written as the F18 client projection {id, display
// name, kind} and never replaces an existing file: the export keeps an
// existing one projected (client_actor_export.go).
func ensureSharerProjection(root, actorID, displayName string) error {
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

// shareProvenanceField lists, on the internal (narrower) row, the ids of the
// copies shared from it (§6.4: "the internal row records the copy"). The
// copy itself never points back; the wider audience cannot read this field.
// It is not a schema column: lsync keeps it as one of the row's extra
// fields, so it travels with the row file.
const shareProvenanceField = "shared_as"

// recordShareProvenance appends copyID to the source row's
// shareProvenanceField in its store or personal project and imports the
// change, so the command's flush commits it to that project.
func recordShareProvenance(ctx context.Context, src *sharedRow, copyID string) error {
	doc := make(map[string]any, len(src.doc)+1)
	for k, v := range src.doc {
		doc[k] = v
	}
	var list []any
	if prev, ok := doc[shareProvenanceField].([]any); ok {
		list = append(list, prev...)
	}
	doc[shareProvenanceField] = append(list, copyID)
	path := src.file
	if path == "" {
		path = filepath.Join(src.project.Root, projresolve.MarkerDir, lsync.DataDirName, src.table, src.id+".json")
	}
	b, err := canonjson.Encode(doc)
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode the provenance").WithCause(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), externalDirMode); err != nil {
		return errcodes.New(errcodes.Internal, "record the provenance").WithCause(err)
	}
	if err := atomicWriteFile(path, b, externalFileMode); err != nil {
		return errcodes.New(errcodes.Internal, "record the provenance").WithCause(err)
	}
	return src.project.importFiles(ctx)
}
