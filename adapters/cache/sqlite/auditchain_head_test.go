package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/auditchain"
)

// TestAuditChainHeadAnchor (SECURITY, P3-AUDIT-3): Verify catches what the
// links alone cannot: an edit of the NEWEST entry, deleting the newest
// entries (tail truncation) or every entry, and deleting or editing the
// head anchor; a later append never launders a tamper (the anchor stays
// where it was, so the chain keeps reporting broken). An untouched chain,
// imported then appended to, is intact.
func TestAuditChainHeadAnchor(t *testing.T) {
	const chain = "K"
	cases := []struct {
		name       string
		tamper     []string // SQL run on the file after three entries (two imported, one appended)
		appendMore bool     // one more Append after the tamper
		wantBroken string   // "" intact, "newest" = the newest id, "anchored" = the anchored (deleted) id
		// wantAnchored: after the run the chain has an anchor again
		// (adoption); then deleting the newest entry must be caught.
		wantAnchored bool
	}{
		{name: "untouched"},
		{name: "untouched, then appended", appendMore: true},
		{name: "newest entry edited", tamper: []string{`UPDATE audit_chain SET action = 'rules.delete' WHERE pos = 3`}, wantBroken: "newest"},
		{name: "newest entry deleted", tamper: []string{`DELETE FROM audit_chain WHERE pos = 3`}, wantBroken: "anchored"},
		{name: "two newest deleted", tamper: []string{`DELETE FROM audit_chain WHERE pos >= 2`}, wantBroken: "anchored"},
		{name: "every entry deleted", tamper: []string{`DELETE FROM audit_chain`}, wantBroken: "anchored"},
		{name: "anchor deleted", tamper: []string{`DELETE FROM audit_chain_head`}, wantBroken: "newest"},
		{name: "anchor hash edited", tamper: []string{`UPDATE audit_chain_head SET hash = 'x'`}, wantBroken: "newest"},
		{name: "newest edited, then appended", tamper: []string{`UPDATE audit_chain SET reason = 'hidden' WHERE pos = 3`}, appendMore: true, wantBroken: "any"},
		{name: "newest deleted, then appended", tamper: []string{`DELETE FROM audit_chain WHERE pos = 3`}, appendMore: true, wantBroken: "any"},
		{name: "anchor deleted, then appended", tamper: []string{`DELETE FROM audit_chain_head`}, appendMore: true, wantBroken: "any"},
		// G-HOST-2: a chain written before the anchor existed (no anchor, no
		// anchored marker) verifies by its links and is adopted by its first
		// write; a broken link is never adopted.
		{name: "never anchored (old build)", tamper: []string{`DELETE FROM audit_chain_head`, `DELETE FROM audit_chain_anchored`}},
		{name: "never anchored, then appended: adopted", tamper: []string{`DELETE FROM audit_chain_head`, `DELETE FROM audit_chain_anchored`}, appendMore: true, wantAnchored: true},
		{name: "never anchored, older entry edited, then appended", tamper: []string{`DELETE FROM audit_chain_head`, `DELETE FROM audit_chain_anchored`, `UPDATE audit_chain SET action = 'x' WHERE pos = 1`}, appendMore: true, wantBroken: "any"},
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "machine.db")
			ch := openChain(t, path)
			a := auditchain.Record{ID: "aud_old1", CreatedAt: "2026-01-01T00:00:00Z", ActorID: "act_x", Action: "memories.write"}
			b := auditchain.Record{ID: "aud_old2", CreatedAt: "2026-01-02T00:00:00Z", ActorID: "act_x", Action: "rules.write", PrevLogHash: auditchain.Hash(a)}
			if _, err := ch.Import(ctx, chain, "/x/lore.db", []auditchain.Record{a, b}, now); err != nil {
				t.Fatal(err)
			}
			newest, err := ch.Append(ctx, chain, auditchain.Record{ActorID: "act_y", Action: "rules.write"}, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.tamper) > 0 {
				dsn, derr := fileDSN(path)
				if derr != nil {
					t.Fatal(derr)
				}
				db, oerr := sql.Open(driverName, dsn)
				if oerr != nil {
					t.Fatal(oerr)
				}
				for _, q := range c.tamper {
					if _, xerr := db.ExecContext(ctx, q); xerr != nil {
						t.Fatal(xerr)
					}
				}
				if cerr := db.Close(); cerr != nil {
					t.Fatal(cerr)
				}
			}
			if c.appendMore {
				if _, aerr := ch.Append(ctx, chain, auditchain.Record{ActorID: "act_y", Action: "tasks.write"}, now); aerr != nil {
					t.Fatal(aerr)
				}
			}
			broken, _, err := ch.Verify(ctx, chain)
			if err != nil {
				t.Fatal(err)
			}
			switch c.wantBroken {
			case "":
				if broken != "" {
					t.Fatalf("an untouched chain reported broken at %s", broken)
				}
			case "newest", "anchored":
				if broken != newest.ID {
					t.Fatalf("broken at %q, want the newest entry %s", broken, newest.ID)
				}
			default:
				if broken == "" {
					t.Fatal("a tamper was laundered by a later append: the chain reports intact")
				}
			}
			if c.wantAnchored {
				dsn, derr := fileDSN(path)
				if derr != nil {
					t.Fatal(derr)
				}
				db, oerr := sql.Open(driverName, dsn)
				if oerr != nil {
					t.Fatal(oerr)
				}
				if _, xerr := db.ExecContext(ctx, `DELETE FROM audit_chain WHERE pos = (SELECT MAX(pos) FROM audit_chain)`); xerr != nil {
					t.Fatal(xerr)
				}
				if cerr := db.Close(); cerr != nil {
					t.Fatal(cerr)
				}
				if b2, _, verr := ch.Verify(ctx, chain); verr != nil || b2 == "" {
					t.Fatalf("an adopted chain must catch a deleted newest entry: %q %v", b2, verr)
				}
			}
		})
	}
}
