// Package auditfill is the audit backfill of FORMAT_EVOLUTION section 3
// ("Audit fields"): rows written before 0.1.18 have no created_by /
// updated_by / deleted_by, and lore must never invent a writer.
//
//	Defaults(row) ─► tombstone? keep as is
//	              ─► created_by missing → created_by_actor_id when it is an actor id, else UnknownActor
//	              ─► updated_by missing → created_by when updated_at == created_at, else UnknownActor
//	              ─► deleted_by missing and deleted_at set → UnknownActor
//
// Pure (layer L1): imports model only. `lore migrate run` writes the result
// (the audit_fields data step in wire); running it twice changes nothing.
package auditfill
