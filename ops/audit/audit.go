// Package audit defines `lore audit log|verify` (layer L5): read and check
// the hash-chained audit log of knowledge changes (R16 #4, R27 #9, R37
// Block 5), with v0.1.17's spellings, output and --json shapes.
//
// Flow:
//
//	audit.log    ──▶ Env.AuditLog(limit)  ──▶ newest first, one line each
//	audit.verify ──▶ Env.AuditVerify      ──▶ ok: "✓ audit chain intact (N entries)"
//	                                        broken / changed outside lore:
//	                                        E_AUDIT_CHAIN_BROKEN (exit non-zero)
//
// Where the chain lives is the composition root's business (wire): the
// operations only ask Env. Both are reads with no engine write path, so the
// host runs them without a sync pass: a pass would export, and record as
// accepted, the very changes verify looks for (v0.1.17 audit_cmd.go).
//
// Invariant: an entry carries names, ids and hashes only, never a field
// value, so nothing here can print a secret.
package audit

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Noun is the CLI group and REST collection.
const Noun = "audit"

// Operation names.
const (
	OpLog    = "audit.log"
	OpVerify = "audit.verify"
)

// Envelope values of v0.1.17's --json (audit_cmd.go jsonKindAudit*).
const (
	SchemaVersion = 1
	KindLog       = "audit.log"
	KindVerify    = "audit.verify"
)

// DefaultLimit is how many entries `lore audit log` shows (v0.1.17
// defaultAuditLogLimit).
const DefaultLimit = 50

// ExternalWriteSuffix ends the action of entries recorded for changes lore
// found in the DB but did not make (v0.1.17 lsync.ChangeExternal).
const ExternalWriteSuffix = ".external-write"

// forcedMarker / forcedLabel: SECRETS 16 / SQ10, a forced reveal is
// printed loudly; the chain sink writes the marker into the entry's reason
// ("… FORCED (<why>)").
const (
	forcedMarker = " FORCED ("
	forcedLabel  = "FORCED"
)

// Entry is one audit entry in v0.1.17's --json form (saas audit.Record:
// the embedded Entry fields first, then id, created_at, prev_log_hash).
type Entry struct {
	ActingProjectID string `json:"acting_project_id,omitempty"`
	TargetProjectID string `json:"target_project_id,omitempty"`
	ActorID         string `json:"actor_id"`
	Action          string `json:"action"`
	TargetTable     string `json:"target_table,omitempty"`
	TargetID        string `json:"target_id,omitempty"`
	Override        bool   `json:"override"`
	BeforeHash      string `json:"before_hash,omitempty"`
	AfterHash       string `json:"after_hash,omitempty"`
	Reason          string `json:"reason,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Confirmed       bool   `json:"confirmed,omitempty"`
	ID              string `json:"id"`
	CreatedAt       string `json:"created_at"`
	PrevLogHash     string `json:"prev_log_hash,omitempty"`
}

// RowRef names one row (a hash mismatch).
type RowRef struct {
	Table string `json:"table"`
	ID    string `json:"id"`
}

// Report is what the chain check found (Env.AuditVerify).
type Report struct {
	// Entries is how many entries the chain holds.
	Entries int
	// BrokenAt is the id of the first entry whose link does not match its
	// predecessor ("" = intact).
	BrokenAt string
	// ChangedOutside lists rows changed in the store's local copy by
	// something other than lore since the last lore command.
	ChangedOutside []RowRef
	// ExternalWrites counts entries recorded for such changes earlier
	// (action *.external-write).
	ExternalWrites int
}

// Env is what the audit operations need; wire.App implements it.
type Env interface {
	// AuditLog returns the newest entries first; limit <= 0 means all.
	AuditLog(ctx context.Context, limit int) ([]Entry, error)
	// AuditVerify walks the whole chain and compares the rows with what
	// lore last recorded. It must not record anything itself.
	AuditVerify(ctx context.Context) (Report, error)
}

// LogIn is the input of audit.log.
type LogIn struct {
	Limit int  `json:"limit,omitempty" flag:"limit" desc:"entries to show (default 50)"`
	All   bool `json:"all,omitempty" flag:"all" desc:"show every entry (P3-AUDIT-3: v0.1.17's --limit 0)"`
}

// LogOut is v0.1.17's list envelope.
type LogOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Count         int     `json:"count,omitempty"`
	Data          []Entry `json:"data"`
}

// Text is v0.1.17's log: "<created> <action> <table>/<id>  by <actor>",
// a forced reveal flagged loudly.
func (o LogOut) Text() string {
	if len(o.Data) == 0 {
		return "· audit log is empty\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		loud := ""
		if _, why, ok := strings.Cut(r.Reason, forcedMarker); ok {
			loud = "  ! " + forcedLabel + " (" + strings.TrimSuffix(why, ")") + ")"
		}
		fmt.Fprintf(&b, "%s  %-28s %s/%s  by %s%s\n", r.CreatedAt, r.Action, r.TargetTable, r.TargetID, r.ActorID, loud)
	}
	return b.String()
}

// VerifyIn is the input of audit.verify (no flags beyond --json).
type VerifyIn struct{}

// VerifyResult is v0.1.17's verify --json data.
type VerifyResult struct {
	OK             bool     `json:"ok"`
	Entries        int      `json:"entries"`
	ChainBrokenAt  string   `json:"chain_broken_at,omitempty"`
	ChangedOutside []RowRef `json:"changed_outside_lore,omitempty"`
	ExternalWrites int      `json:"external_writes_recorded"`
}

// VerifyOut is v0.1.17's verify envelope (count = entries).
type VerifyOut struct {
	SchemaVersion int          `json:"schema_version"`
	Kind          string       `json:"kind"`
	Count         int          `json:"count,omitempty"`
	Data          VerifyResult `json:"data"`
}

// Text is v0.1.17's verify output of an intact chain (a broken one is an
// error, see verify).
func (o VerifyOut) Text() string {
	s := "✓ audit chain intact (" + strconv.Itoa(o.Data.Entries) + " entries)\n"
	if n := o.Data.ExternalWrites; n > 0 {
		s += fmt.Sprintf("! %d change(s) were made outside lore and later recorded (action *%s; see `lore audit log`)\n", n, ExternalWriteSuffix)
	}
	return s
}

// errorCodes are the codes the audit operations can return.
var errorCodes = []errs.Code{errs.AuditChainBroken, errs.NotImplemented, errs.NotAStore, errs.Internal}

// Ops returns the audit operations bound to env.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpLog, Summary: "Show recent audit entries, newest first", Permission: op.PermRead, Access: op.AccessRead, Errors: errorCodes,
			Examples: []op.Example{{Title: "the last 200 entries as JSON", CLI: "lore audit log --limit 200 --json", Want: `"data"`}}},
			func(ctx context.Context, in LogIn) (LogOut, error) { return logRun(ctx, env, in) }),
		op.Define(op.Spec{Name: OpVerify, Summary: "Check the audit chain and look for rows changed outside lore", Permission: op.PermRead, Access: op.AccessRead, Errors: errorCodes,
			Examples: []op.Example{{Title: "check the chain", CLI: "lore audit verify", Want: "audit chain intact"}}},
			func(ctx context.Context, _ VerifyIn) (VerifyOut, error) { return verify(ctx, env) }),
	}
}

// IsAuditOp reports whether name is an audit operation. The host runs them
// without opening the engine: they read the chain only, and verify must not
// trigger anything that records (see the package comment).
func IsAuditOp(name string) bool { return name == OpLog || name == OpVerify }

// logRun is audit.log.
func logRun(ctx context.Context, env Env, in LogIn) (LogOut, error) {
	if in.Limit < 0 {
		return LogOut{}, errs.New(errs.InvalidInput, "--limit must be 0 or more")
	}
	limit := in.Limit
	switch {
	case in.All:
		limit = 0
	case limit == 0:
		limit = DefaultLimit
	}
	recs, err := env.AuditLog(ctx, limit)
	if err != nil {
		return LogOut{}, err
	}
	if recs == nil {
		recs = []Entry{}
	}
	return LogOut{SchemaVersion: SchemaVersion, Kind: KindLog, Count: len(recs), Data: recs}, nil
}

// verify is audit.verify. A broken chain or a row changed outside lore is
// E_AUDIT_CHAIN_BROKEN (P3-AUDIT-1: one path, so the failure is the error
// envelope; v0.1.17 printed its report and then failed the same way).
func verify(ctx context.Context, env Env) (VerifyOut, error) {
	rep, err := env.AuditVerify(ctx)
	if err != nil {
		return VerifyOut{}, err
	}
	switch {
	case rep.BrokenAt != "":
		return VerifyOut{}, errs.New(errs.AuditChainBroken, "audit chain broken at "+rep.BrokenAt+" (an entry was edited or deleted)").
			WithHint("restore lore.db from a backup to investigate")
	case len(rep.ChangedOutside) > 0:
		names := make([]string, 0, len(rep.ChangedOutside))
		for _, c := range rep.ChangedOutside {
			names = append(names, c.Table+"/"+c.ID)
		}
		return VerifyOut{}, errs.New(errs.AuditChainBroken,
			fmt.Sprintf("hash mismatch: %d row(s) changed outside lore: %s", len(names), strings.Join(names, ", "))).
			WithHint("inspect them (`lore <kind> show <id>`); the next lore command records them as external writes")
	}
	res := VerifyResult{OK: true, Entries: rep.Entries, ExternalWrites: rep.ExternalWrites}
	return VerifyOut{SchemaVersion: SchemaVersion, Kind: KindVerify, Count: rep.Entries, Data: res}, nil
}
