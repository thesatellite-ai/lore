// Package auditchain is the hash rule of lore's audit chain (R16 #4, R27
// #9, R37 Block 5; layer L1, stdlib only): every entry carries the hash of
// the one before it, so editing or deleting a past entry breaks the link of
// the entry after it and `lore audit verify` names where.
//
//	Hash(r)            ─▶ sha256 of the fixed v0.1.17 payload (+ session / confirmed only when set)
//	Verify(records)    ─▶ the first record whose PrevLogHash is not Hash(previous), "" = intact
//
// The payload format is byte for byte saas/pkg/aicoder/audit's hashRecord,
// so a chain copied from a v0.1.17 lore.db verifies unchanged here
// (P3-LOCAL-7). Changing it invalidates every existing chain.
package auditchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Record is one chain entry (the v0.1.17 audit_logs columns).
type Record struct {
	ID              string `json:"id"`
	CreatedAt       string `json:"created_at"`
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
	// SessionID is lore's ses_ id (never the tool's own id); "" outside a
	// session. Hashed only when set (OQ10).
	SessionID string `json:"session_id,omitempty"`
	// Confirmed marks a write that followed --yes; hashed only when true.
	Confirmed   bool   `json:"confirmed,omitempty"`
	PrevLogHash string `json:"prev_log_hash,omitempty"`
}

// Hash is the chain-link hash of r: what the next entry's PrevLogHash must
// hold. CreatedAt and PrevLogHash are not part of it (as in v0.1.17).
func Hash(r Record) string {
	payload := fmt.Sprintf(
		"id=%s|actor=%s|action=%s|acting=%s|target=%s|table=%s|tid=%s|override=%t|before=%s|after=%s|reason=%s",
		r.ID, r.ActorID, r.Action, r.ActingProjectID, r.TargetProjectID,
		r.TargetTable, r.TargetID, r.Override, r.BeforeHash, r.AfterHash, r.Reason)
	if r.SessionID != "" {
		payload += "|session=" + r.SessionID
	}
	if r.Confirmed {
		payload += "|confirmed=true"
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// Verify walks records in chain order (oldest first) and returns the id of
// the first one whose PrevLogHash does not match its predecessor ("" =
// intact) and how many entries verified before it.
func Verify(records []Record) (brokenAt string, verified int) {
	prev := ""
	for _, r := range records {
		if r.PrevLogHash != prev {
			return r.ID, verified
		}
		prev = Hash(r)
		verified++
	}
	return "", verified
}
