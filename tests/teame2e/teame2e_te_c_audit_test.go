//go:build teame2e

package teame2e

// te-c audit: `lore audit log|verify` on a team with a real remote and a
// real external git write (later 611, ported from sync_e2e
// TestE2E_AuditVerifyAndLog). On cmd/lore the row files are the truth and
// the hash chain lives in the machine store (~/.lore/state/machine.db): it
// records this machine's lore writes only. A teammate's change arriving
// through git, or a hand edit of a row file, is data, not a chain entry:
// verify stays intact and the next write by lore chains on as usual; a
// forged chain entry is E_AUDIT_CHAIN_BROKEN (exit 23, P3-AUDIT-1). The
// model is coordinator decision P3-AUDIT-2 (akif3): editing a row file is
// not an audit event; git history is its record. Entries carry no row
// hashes on cmd/lore, so nothing here compares before/after hashes.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teCAuditLog is `lore audit log --json`'s envelope (the fields read here).
type teCAuditLog struct {
	Count int `json:"count"`
	Data  []struct {
		Action      string `json:"action"`
		TargetTable string `json:"target_table"`
		TargetID    string `json:"target_id"`
	} `json:"data"`
}

// teCReadAuditLog runs audit log --json in c and decodes it.
func teCReadAuditLog(t *testing.T, c *th.Clone) teCAuditLog {
	t.Helper()
	r := c.MustLore("audit", "log", "--json")
	var v teCAuditLog
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("audit log --json: %v %s", err, r)
	}
	return v
}

// teCTamperChain runs one SQL statement on the actor's machine store, the
// way an attacker with file access would (a plain SQLite connection, no
// lore code), and fails unless it changed a row.
func teCTamperChain(t *testing.T, a *th.Actor, sqlText string) {
	t.Helper()
	path := a.LorePath("state", "machine.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = db.Close() }() // read-only use after Exec; a close error changes nothing
	res, err := db.ExecContext(context.Background(), sqlText)
	if err != nil {
		t.Fatalf("tamper %s: %v", path, err)
	}
	if n, nerr := res.RowsAffected(); nerr != nil || n == 0 {
		t.Fatalf("tamper %s changed no row (%v)", path, nerr)
	}
}

// TestTeC_AuditLogVerifyWithExternalGitWrite: dev1's writes are logged with
// their ids; dev2 hand-edits dev1's rule file with plain git and pushes;
// dev1 pulls. verify stays intact (the pull is not a chain entry), dev1's
// next edit is logged and chains on, and forging an entry breaks verify in text and
// --json, naming the position.
func TestTeC_AuditLogVerifyWithExternalGitWrite(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("audit", "verify"), th.Cmd("audit", "log"))
	rid := mustID(t, rt.c1.MustLore("rule", "add", "--body", "first body", "--no-live"), "rul")
	rt.c1.CommitAll("rule")
	rt.c1.AdminPush("origin", "main")

	if n := teCRuleWrites(t, rt.c1, rid); n != 1 {
		t.Fatalf("dev1's rule write must be logged once with its id, got %d", n)
	}
	rt.c1.MustLore("audit", "verify").Says(t, "intact")

	// dev2: a real external write, plain git, no lore.
	rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")
	path := mainRowPath("", tblRules, rid)
	body, ok := rt.c2.ReadFile(path)
	if !ok || !strings.Contains(body, "first body") {
		t.Fatalf("dev2 must see the rule file %s: %q", path, body)
	}
	rt.c2.WriteFile(path, strings.Replace(body, "first body", "hand edited body", 1))
	rt.c2.CommitAll("hand edit")
	rt.c2.AdminPush("origin", "main")
	rt.c1.Git("pull", "-q", "--ff-only", "origin", "main")

	rt.c1.MustLore("rule", "show", rid).Says(t, "hand edited body")
	before := teCReadAuditLog(t, rt.c1).Count
	v := rt.c1.MustLore("audit", "verify")
	v.Says(t, "intact")
	if after := teCReadAuditLog(t, rt.c1).Count; after != before {
		t.Fatalf("a pulled change and a read must add no chain entry: %d -> %d", before, after)
	}

	rt.c1.MustLore("rule", "edit", rid, "--body", "edited by lore")
	if n := teCRuleWrites(t, rt.c1, rid); n != 2 {
		t.Errorf("dev1's edit after the external write is one more entry (want 2 for %s, got %d)", rid, n)
	}
	rt.c1.MustLore("audit", "verify").Says(t, "intact")

	teCTamperChain(t, rt.dev1, "UPDATE audit_chain SET action = 'forged' WHERE pos = 1")
	rt.c1.Lore("audit", "verify").Fails(t, th.ExitRefused, "E_AUDIT_CHAIN_BROKEN", "audit chain broken at")
	j := rt.c1.Lore("audit", "verify", "--json").Fails(t, th.ExitRefused, "E_AUDIT_CHAIN_BROKEN")
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(j.Stdout), &env); err != nil {
		t.Errorf("--json failure is one error envelope on stdout: %v %s", err, j)
	}
	// dev2's machine never saw dev1's chain: its own verify is untouched.
	rt.c2.MustLore("audit", "verify").Says(t, "intact")
}

// teCRuleWrites counts the rules entries of the chain naming id.
func teCRuleWrites(t *testing.T, c *th.Clone, id string) int {
	t.Helper()
	n := 0
	for _, e := range teCReadAuditLog(t, c).Data {
		if e.TargetID == id && e.TargetTable == tblRules && strings.HasPrefix(e.Action, tblRules+".") {
			n++
		}
	}
	return n
}
