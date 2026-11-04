package lsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/ids"
)

// Tag bridge: the 0.1.18 core keeps a row's tags in its own tag_ids list
// (decision O11), while v0.1.17 readers (render, search, list --tag, the
// TUI) join lore.db's entity_tags. When a core-owned row file is imported,
// that row's entity_tags bindings in lore.db are replaced by its tag_ids,
// so old readers see the core's tags. Read-only like the rest of the core
// import: a binding of a core-owned row is never exported to (or removed
// from) .lore/data/entity_tags, see skipBridgeExport.
const (
	entityTagsTable = "entity_tags"
	tagIDsField     = "tag_ids"
	etEntityTable   = "entity_table"
	etEntityID      = "entity_id"
	etTagID         = "tag_id"
)

// bridgeTags replaces the entity_tags rows of (table, id) with the tag ids
// doc lists (doc nil: the row was deleted, every binding goes).
func (e *engine) bridgeTags(ctx context.Context, table, id string, doc map[string]any) error {
	if !e.present[entityTagsTable][etTagID] {
		return nil // this lore.db has no entity_tags table
	}
	var tagIDs []string
	if list, ok := doc[tagIDsField].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && s != "" {
				tagIDs = append(tagIDs, s)
			}
		}
	}
	keep := map[string]bool{}
	for _, t := range tagIDs {
		keep[t] = true
	}
	rows, err := e.tx.QueryContext(ctx, `SELECT tag_id FROM `+quoteIdent(entityTagsTable)+` WHERE entity_table = ? AND entity_id = ?`, table, id)
	if err != nil {
		return err
	}
	var drop []string
	have := map[string]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			_ = rows.Close()
			return err
		}
		have[t] = true
		if !keep[t] {
			drop = append(drop, t)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, t := range drop {
		if _, err := e.tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(entityTagsTable)+` WHERE entity_table = ? AND entity_id = ? AND tag_id = ?`, table, id, t); err != nil {
			return err
		}
	}
	for _, t := range tagIDs {
		if have[t] {
			continue
		}
		bid, err := ids.Deterministic(ids.PrefixEntityTag, table, id, t)
		if err != nil {
			return err
		}
		if _, err := e.tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+quoteIdent(entityTagsTable)+` (id, created_at, updated_at, entity_table, entity_id, tag_id) VALUES (?, ?, ?, ?, ?, ?)`,
			bid, e.now.UTC(), e.now.UTC(), table, id, t); err != nil {
			return err
		}
	}
	return nil
}

// skipBridgeExport reports whether an entity_tags row (dbDoc, or the file
// at full when the row is gone) binds a core-owned row: such a binding is
// the bridge's mirror of tag_ids and never written to or removed from
// .lore/data (a v0.1.17 file of it stays for `lore migrate run`).
func (e *engine) skipBridgeExport(table, full string, dbDoc map[string]any) bool {
	if table != entityTagsTable {
		return false
	}
	doc := dbDoc
	if doc == nil {
		raw, err := os.ReadFile(filepath.Clean(full))
		if err != nil || json.Unmarshal(raw, &doc) != nil {
			return false
		}
	}
	et, _ := doc[etEntityTable].(string)
	return e.isCoreOwned(strings.TrimSpace(et))
}
