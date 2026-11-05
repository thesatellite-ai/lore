// support_bundle.go — the host behind `lore support-bundle` (S3.7): the
// operation support_bundle.run (ops/supportbundle) builds and writes the
// bundle; this binary supplies the parts that live in lore.db and the
// doctor (PASS3 ONE-PATH, P3-MISC-3). Every part degrades to empty, so a
// bundle is produced outside a project too.
//
// NEVER includes raw memory/decision content (--include-content only marks
// the bundle unsanitized, as in v0.1.17).
package main

import (
	"context"
	"encoding/json"

	"saas/pkg/aicoder/identity"

	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// supportBundleHost implements opssupportbundle.Host over lore.db.
type supportBundleHost struct{}

// Version implements opssupportbundle.Host.
func (supportBundleHost) Version() string { return version }

// Identity implements opssupportbundle.Host.
func (supportBundleHost) Identity(context.Context) opssupportbundle.Identity {
	r := identity.Resolve(identity.Inputs{})
	return opssupportbundle.Identity{Resolved: r.StableKey, Source: r.Step.String(), Stable: r.Step.Stable()}
}

// Doctor implements opssupportbundle.Host: the doctor report as JSON
// (null when it cannot be encoded; the report itself never fails the
// bundle, as in v0.1.17).
func (supportBundleHost) Doctor(ctx context.Context) opssupportbundle.RawJSON {
	report, _ := runDoctor(ctx, &doctorFlags{}) // a failing check is part of the report
	data, err := json.Marshal(report)
	if err != nil {
		return nil
	}
	return data
}

// Counts implements opssupportbundle.Host.
func (supportBundleHost) Counts(ctx context.Context) opssupportbundle.Counts {
	return gatherCounts(ctx, &doctorFlags{})
}

// Sessions implements opssupportbundle.Host.
func (supportBundleHost) Sessions(ctx context.Context) []opssupportbundle.Session {
	return supportSessions(ctx, &doctorFlags{})
}

func gatherCounts(ctx context.Context, f *doctorFlags) opssupportbundle.Counts {
	counts := opssupportbundle.Counts{}
	_, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return counts
	}
	defer func() { _ = client.Close() }() // read-only use

	// Core-owned tables are counted through the core (live rows of every
	// attached store); lore.db only holds a bridge copy of them. Without a
	// core (unit tests) lore.db answers as before.
	for _, table := range supportCoreTables {
		if n, ok := coreRowCount(ctx, table); ok {
			counts[table] = n
		}
	}
	if coreApp == nil {
		if n, err := client.Memory.Query().Count(ctx); err == nil {
			counts["memories"] = n
		}
		if n, err := client.Rule.Query().Count(ctx); err == nil {
			counts["rules"] = n
		}
		if n, err := client.Hotfix.Query().Count(ctx); err == nil {
			counts["hotfixes"] = n
		}
		if n, err := client.Decision.Query().Count(ctx); err == nil {
			counts["decisions"] = n
		}
		if n, err := client.Snapshot.Query().Count(ctx); err == nil {
			counts["snapshots"] = n
		}
	}
	// Machine-local tables stay on lore.db (Q-LOCAL: ent/lore.db keeps them
	// for 0.1.18 until a never-synced machine-local backend exists).
	if n, err := client.LearnCandidate.Query().Count(ctx); err == nil {
		counts["learn_candidates"] = n
	}
	// P3-LOCAL-7: the project's audit chain is in the machine store.
	if dbPath, derr := auditDBPath(oldProjectDir()); derr == nil && coreApp != nil {
		if recs, rerr := auditRecords(ctx, coreApp, dbPath); rerr == nil {
			counts["audit_log_rows"] = len(recs)
		}
	}
	// P3-LOCAL-5: render records live in the machine store (every repo's;
	// the bundle counts the machine's, as v0.1.17 counted its lore.db's).
	if n, ok := coreMachineRowCount(ctx, schema.TableRenderHistories); ok {
		counts["render_history_rows"] = n
	}
	return counts
}

// supportCoreTables are the core-owned tables whose counts a support
// bundle reports (the v0.1.17 bundle's set).
var supportCoreTables = []string{"memories", "rules", "hotfixes", "decisions", "snapshots"}

// coreRowCount counts table's live rows in every attached store through
// the core; ok=false with no core mounted or when a store cannot be read.
func coreRowCount(ctx context.Context, table string) (int, bool) {
	if coreApp == nil {
		return 0, false
	}
	if _, err := coreApp.Engine(ctx); err != nil { // opens the App: Stores() is empty before
		return 0, false
	}
	docs, err := coreDocs(ctx, table)
	if err != nil {
		return 0, false
	}
	return len(docs), true
}

// supportSessionLimit is how many sessions a bundle describes.
const supportSessionLimit = 10

// supportSessions reads the newest sessions from the machine store
// (P3-COORD-SESS-1), stripped; empty when the core is not mounted or the
// read fails.
func supportSessions(ctx context.Context, _ *doctorFlags) []opssupportbundle.Session {
	out := []opssupportbundle.Session{}
	if coreApp == nil {
		return out
	}
	rows, err := coreApp.Sessions(ctx, supportSessionLimit)
	if err != nil {
		return out
	}
	for _, r := range rows {
		out = append(out, stripSession(r))
	}
	return out
}

// stripSession is r without the four private fields (section 9).
func stripSession(r wire.SessionRow) opssupportbundle.Session {
	return opssupportbundle.Session{Tool: r.Tool, ToolVersion: r.ToolVersion, Signal: r.Signal, Flags: r.Flags,
		DeclaredHuman: r.DeclaredHuman, HasToolSessionID: r.ToolSessionID != "", HasModel: r.Model != "",
		HasCwd: r.Cwd != "", HasCI: r.CI != ""}
}
