// task_refs.go — `task add|edit --ref <id>` / `--ref commit:<sha>`
// (LORE_TEAM_SPEC §1 flags, F15, cookbook 12): pointers from a task to
// another row or to a commit, stored as knowledge_refs rows in the task's
// own project. A pointer to a row passes the F15 direction rule (inward
// only) before anything is written; a commit is not a row and needs none.
package main

import (
	"context"
	"path/filepath"
	"strings"

	"dbent/gen/ent"
	entKnowledgeRef "dbent/gen/ent/knowledgeref"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/xref"
)

const (
	flagRef = "ref"
	// commitRefPrefix marks a commit pointer: --ref commit:<sha>.
	commitRefPrefix = "commit:"
	// tableCommits is the dst_table of a commit pointer (a commit is not a
	// lore row; the sha is the id).
	tableCommits = "commits"
	// refRelation is the relation a --ref pointer records.
	refRelation = "mentions"
)

// refTables maps id prefixes to the tables a --ref may point at.
var refTables = map[string]string{
	ids.PrefixTask: tableTasks, ids.PrefixMission: tableMissions, ids.PrefixMemory: tableMemories,
	ids.PrefixRule: "rules", ids.PrefixDecision: "decisions", ids.PrefixComment: tableComments, ids.PrefixHandoff: "handoffs",
}

// taskRef is one parsed --ref.
type taskRef struct {
	table, id, projectID string
}

// parseTaskRefs splits --ref values into commit pointers and row pointers;
// rows go through checkReferences (F15).
func parseTaskRefs(raw []string) (refs []taskRef, rowRefs []string, err error) {
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if sha, ok := strings.CutPrefix(r, commitRefPrefix); ok {
			if sha == "" || strings.ContainsAny(sha, " /") {
				return nil, nil, errcodes.New(errcodes.InvalidInput, "--ref "+r+": want commit:<sha>")
			}
			refs = append(refs, taskRef{table: tableCommits, id: sha})
			continue
		}
		x, perr := xref.Parse(r)
		if perr != nil {
			return nil, nil, errcodes.New(errcodes.InvalidInput, perr.Error())
		}
		prefix, _, _ := strings.Cut(x.RowID, "_")
		table, ok := refTables[prefix]
		if !ok {
			return nil, nil, errcodes.New(errcodes.InvalidInput, "--ref "+r+": not a row id or commit:<sha>")
		}
		refs = append(refs, taskRef{table: table, id: x.RowID, projectID: x.ProjectID})
		rowRefs = append(rowRefs, r)
	}
	return refs, rowRefs, nil
}

// checkoutProjectID is this checkout's own project id ("" outside one): the
// project of a row a store task points back at (dst_project_id).
func checkoutProjectID() string {
	root := liveProjectRoot(mustGetwd())
	if root == "" {
		return ""
	}
	m, err := lsync.ReadProjectMeta(filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName))
	if err != nil {
		return ""
	}
	return m.ProjectID
}

// saveTaskRefs records the pointers of task id in its project's DB. A row
// that is not in that project is recorded with the project it lives in: the
// checkout's own project when the task was routed elsewhere (`--to <org>`).
func saveTaskRefs(ctx context.Context, client *ent.Client, projectID, id string, refs []taskRef, routed bool, here string) error {
	for _, r := range refs {
		exists, err := client.KnowledgeRef.Query().Where(entKnowledgeRef.SrcTable(tableTasks), entKnowledgeRef.SrcID(id),
			entKnowledgeRef.DstTable(r.table), entKnowledgeRef.DstID(r.id)).Exist(ctx)
		if err != nil {
			return errcodes.New(errcodes.Internal, "read refs").WithCause(err)
		}
		if exists {
			continue
		}
		c := client.KnowledgeRef.Create().SetSrcTable(tableTasks).SetSrcID(id).SetDstTable(r.table).SetDstID(r.id).
			SetRelation(refRelation).SetConfidence(entKnowledgeRef.ConfidenceExtracted)
		dst := r.projectID
		if dst == "" && r.table != tableCommits && routed && here != "" && here != projectID {
			dst = here
		}
		if dst != "" {
			c.SetDstProjectID(dst)
		}
		if _, err := c.Save(ctx); err != nil {
			return errcodes.New(errcodes.Internal, "save ref").WithCause(err)
		}
	}
	return nil
}
