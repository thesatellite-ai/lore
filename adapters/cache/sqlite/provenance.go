package sqlite

// Provenance caches (PROVENANCE_DESIGN 9.2): row_provenance, row_lineage,
// row_moves and repo_urls, the query shape of `_o`, `_w`, `shared_as` and
// repos.urls for `lore sql`.
//
//	_rows (every envelope, byte for byte)
//	  ├─ row_provenance  one row per live row: _o facts + _w stamp
//	  ├─ row_lineage     copied_from and v0.1.17 shared_as ids
//	  ├─ row_moves       one row per _o.moves entry (FQ4)
//	  └─ repo_urls       one row per repos.urls entry (10.2)
//
// DECISION PROV-VIEWS (ov20-prov): like team_versions (TEAMV-VIEW) these
// are views over `_rows`, not stored tables: they can never drift from the
// rows, need no rebuild step ("rebuilt from the files when missing" holds
// because `_rows` is), are never synced and carry no audit fields. Each
// view has a leading `_source` column like the typed tables, so a combined
// cache keeps sources apart. In a client repo they hold only what that
// repo's files hold (the reduced _o of 8.1), so no internal origin shows.

// Names of the provenance views.
const (
	viewProvenance = "row_provenance"
	viewLineage    = "row_lineage"
	viewMoves      = "row_moves"
	viewRepoURLs   = "repo_urls"
)

// ProvenanceViews lists the provenance views every cache file holds, for a
// lens that exposes them next to the typed tables.
func ProvenanceViews() []string {
	return []string{viewProvenance, viewLineage, viewMoves, viewRepoURLs}
}

// liveDocs is the shared filter: live rows whose document is JSON.
const liveDocs = `deleted = 0 AND json_valid(doc)`

// provenanceDDL creates the views (idempotent); appended to internalDDL.
var provenanceDDL = []string{
	`CREATE VIEW IF NOT EXISTS "` + viewProvenance + `" AS SELECT
		source AS _source, id AS row_id, tbl AS table_name,
		COALESCE(json_type(doc, '$._o') = 'object', 0) AS has_origin,
		json_extract(doc, '$._o.org') AS org_id,
		json_extract(doc, '$._o.project') AS project_id,
		json_extract(doc, '$._o.repo') AS repo_id,
		json_extract(doc, '$._o.dir') AS dir,
		json_extract(doc, '$._o.store') AS store_id,
		json_extract(doc, '$._o.folder') AS folder_id,
		json_extract(doc, '$._o.git.branch') AS git_branch,
		json_extract(doc, '$._o.git.commit') AS git_commit,
		json_extract(doc, '$._o.git.state') AS git_state,
		json_extract(doc, '$._o.cmd') AS cmd,
		json_extract(doc, '$._o.plugin') AS plugin_id,
		json_extract(doc, '$._o.import.source') AS import_source,
		json_extract(doc, '$._o.import.src') AS import_src_id,
		json_extract(doc, '$._o.import.ext_id') AS import_ext_id,
		json_extract(doc, '$._o.copied_from') AS copied_from,
		json_extract(doc, '$._w.v') AS w_version,
		json_extract(doc, '$._w.m') AS w_machine
	FROM "` + tblRows + `" WHERE ` + liveDocs,
	`CREATE VIEW IF NOT EXISTS "` + viewLineage + `" AS
	SELECT source AS _source, id AS row_id, 'copied_from' AS kind,
		json_extract(doc, '$._o.copied_from') AS other_id, NULL AS at
	FROM "` + tblRows + `" WHERE ` + liveDocs + ` AND json_type(doc, '$._o.copied_from') = 'text'
	UNION ALL
	SELECT r.source, r.id, 'shared_as',
		CASE j.type WHEN 'text' THEN j.value ELSE json_extract(j.value, '$.id') END,
		CASE j.type WHEN 'object' THEN json_extract(j.value, '$.at') END
	FROM "` + tblRows + `" r, json_each(r.doc, '$.shared_as') j
	WHERE r.deleted = 0 AND json_valid(r.doc) AND json_type(r.doc, '$.shared_as') = 'array'`,
	`CREATE VIEW IF NOT EXISTS "` + viewMoves + `" AS SELECT
		r.source AS _source, r.id AS row_id,
		json_extract(j.value, '$.at') AS at,
		json_extract(j.value, '$.by') AS by_actor,
		json_extract(j.value, '$.moved_from') AS moved_from,
		json_extract(j.value, '$.moved_to') AS moved_to
	FROM "` + tblRows + `" r, json_each(r.doc, '$._o.moves') j
	WHERE r.deleted = 0 AND json_valid(r.doc) AND json_type(r.doc, '$._o.moves') = 'array'`,
	`CREATE VIEW IF NOT EXISTS "` + viewRepoURLs + `" AS SELECT
		r.source AS _source, r.id AS repo_id,
		json_extract(j.value, '$.url') AS url,
		json_extract(j.value, '$.kind') AS kind,
		json_extract(j.value, '$.how') AS how,
		json_extract(j.value, '$.from') AS from_date,
		json_extract(j.value, '$.to') AS to_date,
		json_extract(j.value, '$.by') AS by_actor
	FROM "` + tblRows + `" r, json_each(r.doc, '$.urls') j
	WHERE r.tbl = 'repos' AND r.deleted = 0 AND json_valid(r.doc) AND json_type(r.doc, '$.urls') = 'array'`,
}

func init() { internalDDL = append(internalDDL, provenanceDDL...) }
