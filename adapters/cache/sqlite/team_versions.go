package sqlite

import (
	"context"
	"database/sql"
)

// TeamVersion is one writer (person or agent) on one machine and the lore
// version of its newest stamped save (FORMAT_EVOLUTION rule 2 "Teammates'
// versions in doctor", FQ3, OQ7). Machine is "" for saves into a code repo,
// which carry `_w {v}` only (Q-R3-9).
type TeamVersion struct {
	Actor    string
	Machine  string
	Version  string
	LastSeen string // updated_at of that save (RFC 3339 text), "" when absent
}

// teamVersionsSQL reads the `_w` stamps straight from `_rows`, the raw copy
// of every envelope: the newest stamped save per (updated_by, _w.m) by HLC.
// Rows without a stamp are older than 0.1.18 and are not listed (OQ7: no
// stamp means older than 0.1.18; doctor says so in its own words).
//
// DECISION TEAMV-VIEW (ov6-upgrade): the team_versions "cache" is computed
// on read from `_rows` instead of a second stored table, so it can never
// drift from the rows and needs no rebuild step (OQ7's "rebuilt from the
// files when missing" holds trivially: `_rows` is rebuilt from the files).
const teamVersionsSQL = `
WITH stamped AS (
	SELECT COALESCE(json_extract(doc, '$.updated_by'), json_extract(doc, '$.created_by'), '') AS actor,
	       COALESCE(json_extract(doc, '$._w.m'), '') AS machine,
	       json_extract(doc, '$._w.v') AS version,
	       COALESCE(json_extract(doc, '$.updated_at'), json_extract(doc, '$.created_at'), '') AS seen,
	       hlc
	FROM "` + tblRows + `"
	WHERE deleted = 0 AND json_valid(doc) AND json_type(doc, '$._w.v') = 'text'
), ranked AS (
	SELECT actor, machine, version, seen,
	       ROW_NUMBER() OVER (PARTITION BY actor, machine ORDER BY hlc DESC) AS rn
	FROM stamped
)
SELECT actor, machine, version, seen FROM ranked WHERE rn = 1 ORDER BY actor, machine`

// ViewTeamVersions is the same read as a view every cache file holds, so
// `lore sql` can query it (CLI_REFERENCE 3.6, TEAMV-VIEW): columns actor,
// machine, version, last_seen.
const ViewTeamVersions = "team_versions"

func init() {
	internalDDL = append(internalDDL, `CREATE VIEW IF NOT EXISTS "`+ViewTeamVersions+`" (actor, machine, version, last_seen) AS `+teamVersionsSQL)
}

// TeamVersions lists every writer and machine with the lore version of its
// newest save, for `lore doctor`'s "Team versions" section.
func (c *Cache) TeamVersions(ctx context.Context) ([]TeamVersion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(); err != nil {
		return nil, err
	}
	return QueryTeamVersions(ctx, c.db)
}

// QueryTeamVersions runs the team versions read on any handle of a cache
// file (doctor opens the file read-only, without the Cache).
func QueryTeamVersions(ctx context.Context, db *sql.DB) ([]TeamVersion, error) {
	rows, err := db.QueryContext(ctx, teamVersionsSQL)
	if err != nil {
		return nil, dbErr("read team versions", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor; a close error after Err() is not actionable
	var out []TeamVersion
	for rows.Next() {
		var t TeamVersion
		if err := rows.Scan(&t.Actor, &t.Machine, &t.Version, &t.LastSeen); err != nil {
			return nil, dbErr("scan team versions", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("read team versions", err)
	}
	return out, nil
}
