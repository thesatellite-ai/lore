package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/schema"
)

// The time_spent view of `lore sql` (TIME_TRACKING_DESIGN 7.4, 9):
//
//	lens setup ─► createTimeSpent (before query_only)
//	   tasks_all (time_entries, time_open) + actors_all (owner_actor_id)
//	   ─► spans, open ones ending at read time
//	   ─► counted_seconds per worker, owner_counted_seconds per owner (TQ1)
//	   ─► TEMP TABLE task_time_spans + task_time_clients (meta key time.client_key)
//	   ─► TEMP VIEW time_spent (joins tasks, projects, actors)
//
// Built on the lens, not in the cache file: the spans are derived from the
// rows at read time, so open spans are current and nothing can go stale.
// Trashed tasks are tombstones in the cache, so their time is never here
// (DQ10 default; `--include-deleted` cannot reach it from the cache).

// Names of the derived objects.
const (
	tblTimeSpans   = "task_time_spans"
	tblTimeClients = "task_time_clients"
	viewTimeSpent  = "time_spent"
	// TimeClientKey is the default metadata key name the view's client
	// column reads (TQ8); LensOptions.TimeClientKey carries the policy
	// time.client_key (FB-TASK-4).
	TimeClientKey = "client"
	// openEntryPrefix marks an open span's entry_id (design 7.4).
	openEntryPrefix = "open:"
	flagSep         = ","
)

// Column names the view reads from the projected tables.
const (
	colTimeEntries = "time_entries"
	colTimeOpen    = "time_open"
	colProjectID   = "project_id"
	colOwnerActor  = "owner_actor_id"
	colDisplayName = "display_name"
	colKind        = "kind"
	colTitle       = "title"
	colStatus      = "status"
)

// Fixed reads of the time view (schema constants only, no input).
const (
	qActorOwners = `SELECT id, "` + colOwnerActor + `" FROM "` + schema.TableActors + lensViewSuffix + `" WHERE "` + colOwnerActor + `" IS NOT NULL`
	qTaskTime    = `SELECT id, coalesce("` + colProjectID + `", ''), coalesce("` + colTimeEntries + `", ''), coalesce("` + colTimeOpen + `", '') FROM "` +
		schema.TableTasks + lensViewSuffix + `" ORDER BY id`
)

// metaSQL reads the task docs of the i-th attached source that hold a
// metadata path (bound twice) for table (bound once).
func metaSQL(i int) (string, error) {
	sq, err := ident(attachedName(i))
	if err != nil {
		return "", err
	}
	return "SELECT id, json_extract(doc, ?) FROM " + sq + `."` + tblRows + `" WHERE tbl = ? AND deleted = 0 AND json_extract(doc, ?) IS NOT NULL`, nil
}

// spanRow is one row of task_time_spans.
type spanRow struct {
	taskID, projectID, entryID, source, actorID, sessionRef, ownerID string
	start, end                                                       time.Time
	day                                                              string
	seconds, counted, ownerCounted                                   int64
	open, manual                                                     bool
	flags                                                            string
}

// createTimeSpent builds task_time_spans and the time_spent view on the
// lens connection (call before PRAGMA query_only). A lens without a tasks
// table gets nothing.
func (l *Lens) createTimeSpent(ctx context.Context, tables map[string][]srcTable, now time.Time) error {
	// time_entries, time_open and project_id came in one schema version
	if !hasCols(tables, schema.TableTasks, colTimeEntries, colTimeOpen, colProjectID) {
		return nil
	}
	owners, err := l.actorOwners(ctx, tables)
	if err != nil {
		return err
	}
	spans, err := l.readSpans(ctx, owners, now)
	if err != nil {
		return err
	}
	countSpans(spans)
	clients, err := l.taskClients(ctx, tables)
	if err != nil {
		return err
	}
	return l.writeTimeSpent(ctx, tables, spans, clients)
}

// hasCols reports whether every source copy set of table has all cols in
// at least one source.
func hasCols(tables map[string][]srcTable, table string, cols ...string) bool {
	for _, st := range tables[table] {
		ok := true
		for _, c := range cols {
			ok = ok && slices.Contains(st.cols, c)
		}
		if ok {
			return true
		}
	}
	return false
}

// actorOwners maps an agent actor to its owner (owner_actor_id).
func (l *Lens) actorOwners(ctx context.Context, tables map[string][]srcTable) (map[string]string, error) {
	out := map[string]string{}
	if !hasCols(tables, schema.TableActors, colOwnerActor) {
		return out, nil
	}
	rs, err := l.conn.QueryContext(ctx, qActorOwners)
	if err != nil {
		return nil, dbErr("time_spent: read actors", err)
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var id, own string
		if err = rs.Scan(&id, &own); err != nil {
			return nil, dbErr("time_spent: read actors", err)
		}
		out[id] = own
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("time_spent: read actors", err)
	}
	return out, nil
}

// readSpans decodes every task's time entries (live ones) and open spans.
func (l *Lens) readSpans(ctx context.Context, owners map[string]string, now time.Time) ([]spanRow, error) {
	rs, err := l.conn.QueryContext(ctx, qTaskTime)
	if err != nil {
		return nil, dbErr("time_spent: read tasks", err)
	}
	defer func() { _ = rs.Close() }()
	ownerOf := func(a string) string {
		if o := owners[a]; o != "" {
			return o
		}
		return a
	}
	var out []spanRow
	seen := map[string]bool{}
	for rs.Next() {
		var id, proj, entries, open string
		if err = rs.Scan(&id, &proj, &entries, &open); err != nil {
			return nil, dbErr("time_spent: read tasks", err)
		}
		if seen[id] {
			continue // one id in two sources: its spans count once
		}
		seen[id] = true
		list, _ := timeentry.Decode([]byte(entries)) // a bad list counts as none (lint reports it)
		closed := map[string]bool{}
		for _, e := range list {
			closed[e.ID.String()] = true
			if e.Deleted() {
				continue
			}
			a := e.ActorID.String()
			out = append(out, spanRow{taskID: id, projectID: proj, entryID: e.ID.String(), source: string(e.Source), actorID: a,
				sessionRef: e.SessionRef, ownerID: ownerOf(a), start: e.StartTime(), end: e.EndTime(), day: e.Day,
				seconds: e.Seconds, manual: e.Source == timeentry.SourceManual, flags: strings.Join(e.Flags, flagSep)})
		}
		ol, _ := timeentry.DecodeOpen([]byte(open))
		for _, o := range ol {
			if closed[o.ID.String()] {
				continue // the closed entry wins (OV7-2)
			}
			a := o.ActorID.String()
			st := o.StartTime()
			secs := max(int64(now.Sub(st)/time.Second), 0)
			out = append(out, spanRow{taskID: id, projectID: proj, entryID: openEntryPrefix + o.ID.String(), source: string(o.Source), actorID: a,
				ownerID: ownerOf(a), start: st, end: now, day: st.Local().Format(timeentry.DayLayout), seconds: secs, open: true})
		}
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("time_spent: read tasks", err)
	}
	return out, nil
}

// countSpans fills counted_seconds (per worker: actor + session) and
// owner_counted_seconds (per owner, TQ1): spans of one group sorted by
// start, end, id each count only the part not covered by earlier spans;
// manual entries always count in full (design 7.4). Deterministic.
func countSpans(spans []spanRow) {
	assign := func(key func(spanRow) string, set func(*spanRow, int64)) {
		groups := map[string][]int{}
		for i, s := range spans {
			if s.manual {
				set(&spans[i], s.seconds)
				continue
			}
			k := key(s)
			groups[k] = append(groups[k], i)
		}
		for _, idx := range groups {
			slices.SortFunc(idx, func(a, b int) int {
				x, y := spans[a], spans[b]
				return cmp.Or(x.start.Compare(y.start), x.end.Compare(y.end), cmp.Compare(x.entryID, y.entryID))
			})
			var covered int64 // end of the covered prefix (unix seconds)
			first := true
			for _, i := range idx {
				s, e := spans[i].start.Unix(), spans[i].end.Unix()
				if e < s {
					e = s // clock skew counts 0 (11.3)
				}
				if !first && s < covered {
					s = covered
				}
				n := max(e-s, 0)
				set(&spans[i], n)
				if first || e > covered {
					covered = e
				}
				first = false
			}
		}
	}
	assign(func(s spanRow) string { return s.taskID + "|" + s.actorID + "|" + s.sessionRef }, func(s *spanRow, n int64) { s.counted = n })
	assign(func(s spanRow) string { return s.taskID + "|" + s.ownerID }, func(s *spanRow, n int64) { s.ownerCounted = n })
}

// taskClients reads each task's value of the metadata key the policy
// time.client_key names (default TimeClientKey), by its permanent mky_ id:
// the policy value is a key name, or the mky_ id itself so a renamed key
// keeps working (TQ8, FB-TASK-4).
func (l *Lens) taskClients(ctx context.Context, tables map[string][]srcTable) (map[string]string, error) {
	out := map[string]string{}
	if !hasCols(tables, schema.TableMetaKeys, "name") {
		return out, nil
	}
	clientKey := l.clientKey
	if clientKey == "" {
		clientKey = TimeClientKey
	}
	var keyID string
	err := l.conn.QueryRowContext(ctx, `SELECT id FROM "`+schema.TableMetaKeys+lensViewSuffix+`" WHERE name = ? OR id = ? ORDER BY id LIMIT 1`, clientKey, clientKey).Scan(&keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, dbErr("time_spent: read meta_keys", err)
	}
	path := `$.meta."` + keyID + `".v`
	for i := range l.sources {
		q, qerr := metaSQL(i)
		if qerr != nil {
			return nil, qerr
		}
		rs, qerr := l.conn.QueryContext(ctx, q, path, schema.TableTasks, path)
		if qerr != nil {
			return nil, dbErr("time_spent: read task metadata", qerr)
		}
		for rs.Next() {
			var id string
			var v sql.NullString
			if err = rs.Scan(&id, &v); err != nil {
				_ = rs.Close()
				return nil, dbErr("time_spent: read task metadata", err)
			}
			if v.Valid {
				out[id] = v.String
			}
		}
		err = rs.Err()
		_ = rs.Close()
		if err != nil {
			return nil, dbErr("time_spent: read task metadata", err)
		}
	}
	return out, nil
}

// spanTime is a span time in the TEXT form of the cache (UTC, sortable);
// NULL for a date-only manual entry.
func spanTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(timeentry.TimeLayout), Valid: true}
}

// writeTimeSpent creates and fills the temp tables and the view.
func (l *Lens) writeTimeSpent(ctx context.Context, tables map[string][]srcTable, spans []spanRow, clients map[string]string) error {
	stmts := []string{
		`CREATE TEMP TABLE "` + tblTimeSpans + `" (task_id TEXT, project_id TEXT, entry_id TEXT, source TEXT, actor_id TEXT, session_ref TEXT, owner_actor_id TEXT,
			start_at TEXT, end_at TEXT, day TEXT, seconds INTEGER, counted_seconds INTEGER, owner_counted_seconds INTEGER, is_open INTEGER, is_manual INTEGER, flags TEXT)`,
		`CREATE TEMP TABLE "` + tblTimeClients + `" (task_id TEXT PRIMARY KEY, client TEXT)`,
	}
	for _, s := range stmts {
		if _, err := l.conn.ExecContext(ctx, s); err != nil {
			return dbErr("time_spent: create", err)
		}
	}
	ins := `INSERT INTO "` + tblTimeSpans + `" VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	for _, s := range spans {
		if _, err := l.conn.ExecContext(ctx, ins, s.taskID, s.projectID, s.entryID, s.source, s.actorID, nullText(s.sessionRef), s.ownerID,
			spanTime(s.start), spanTime(s.end), s.day, s.seconds, s.counted, s.ownerCounted, s.open, s.manual, nullText(s.flags)); err != nil {
			return dbErr("time_spent: fill", err)
		}
	}
	for id, c := range clients {
		if _, err := l.conn.ExecContext(ctx, `INSERT INTO "`+tblTimeClients+`" VALUES (?, ?)`, id, c); err != nil {
			return dbErr("time_spent: fill", err)
		}
	}
	view, err := timeSpentSQL(tables)
	if err != nil {
		return err
	}
	if _, err = l.conn.ExecContext(ctx, view); err != nil {
		return dbErr("time_spent: create view", err)
	}
	l.views = append(l.views, viewTimeSpent)
	return nil
}

// nullText is NULL for "".
func nullText(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// timeSpentSQL is the design 9 view, aligned to the tables this lens has
// (a missing table or column reads as NULL). Each joined table is grouped
// by id first: one id held by several sources (actors are in every store)
// must not multiply the spans.
func timeSpentSQL(tables map[string][]srcTable) (string, error) {
	// one returns a "(SELECT id, max(c) AS c, … GROUP BY id) alias" join
	// source for table, NULL columns where the lens lacks them.
	one := func(table, alias string, cols ...string) string {
		sel := []string{"id"}
		for _, c := range cols {
			if hasCols(tables, table, c) {
				sel = append(sel, `max("`+c+`") AS "`+c+`"`)
			} else {
				sel = append(sel, `NULL AS "`+c+`"`)
			}
		}
		if len(tables[table]) == 0 {
			return "(SELECT NULL AS " + strings.Join(append([]string{"id"}, quoteAll(cols)...), ", NULL AS ") + ") " + alias
		}
		return "(SELECT " + strings.Join(sel, ", ") + ` FROM "` + table + lensViewSuffix + `" GROUP BY id) ` + alias
	}
	sel := []string{
		"s.task_id", `t."` + colTitle + `" AS task_title`, `t."` + colStatus + `" AS task_status`,
		"s.project_id", `p."name" AS project_name`,
		"s.entry_id", "s.source", "s.is_manual", "s.is_open", "s.actor_id",
		`a."` + colDisplayName + `" AS worker_label`, `a."` + colKind + `" AS worker_kind`,
		"s.owner_actor_id", `o."` + colDisplayName + `" AS owner_label`,
		"s.session_ref", "s.start_at", "s.end_at", "datetime(s.start_at, 'localtime') AS start_local", "s.day",
		"s.seconds", "s.counted_seconds", "s.owner_counted_seconds", "s.flags",
		// task_deleted_at: when the task went into the trash, from the
		// trash table (lens_trash.go, built before this view); NULL for a
		// live task. Only `lore sql --include-deleted` lists a trashed
		// task's spans, so outside it the column is always NULL.
		`(SELECT max(d.deleted_at) FROM "` + viewTrash + `" d WHERE d.table_name = '` + schema.TableTasks + `' AND d.id = s.task_id) AS task_deleted_at`,
		"c.client",
	}
	from := `FROM "` + tblTimeSpans + `" s JOIN ` + one(schema.TableTasks, "t", colTitle, colStatus) + ` ON t.id = s.task_id` +
		` LEFT JOIN "` + tblTimeClients + `" c ON c.task_id = s.task_id` +
		` LEFT JOIN ` + one(schema.TableProjects, "p", "name") + ` ON p.id = s.project_id` +
		` LEFT JOIN ` + one(schema.TableActors, "a", colDisplayName, colKind) + ` ON a.id = s.actor_id` +
		` LEFT JOIN ` + one(schema.TableActors, "o", colDisplayName, colKind) + ` ON o.id = s.owner_actor_id`
	if _, err := ident(viewTimeSpent); err != nil {
		return "", errs.Wrap(errs.Internal, "time_spent: view name", err)
	}
	return `CREATE TEMP VIEW "` + viewTimeSpent + `" AS SELECT ` + strings.Join(sel, ", ") + " " + from, nil
}

// quoteAll double-quotes each column name.
func quoteAll(cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = `"` + c + `"`
	}
	return out
}
