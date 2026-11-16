package task

// claimmark.go — devco:D-464: an explicit status change during a claim is
// work, and the release keeps it.
//
//	task start / edit --status / done / cancel / reopen ─► setStatus
//	   ─► markClaimStatusSet: the open claim chain's claim_log entry gets
//	      before.status_set = true, in the same row write
//	task release (adapters/signals/coord Release)
//	   ─► before.status_set? keep the row's status + started_at, write the span
//	      else                put back the pre-claim status (devco:D-427)
//
// Why: a claim sets in_progress and started_at itself, so a `task start`
// on a claimed task changed nothing the release could see, and the release
// put the started task back to todo. The marker is durable (a row field,
// synced like the claim), so the release never guesses from timestamps.

import (
	"encoding/json"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// The claim_log keys the marker reads and writes. They are the JSON names
// of coord.ClaimLogEntry / coord.ClaimBefore (adapters/signals/coord/row.go);
// the ops layer does not import the coordination adapter, so the entry is
// handled as raw JSON and every other key is kept byte for byte.
const (
	claimKeyEvent     = "event"
	claimKeyBefore    = "before"
	claimKeyStatus    = "status"
	claimKeyStatusSet = "status_set"
	claimEventClaim   = "claim"
	claimEventReclaim = "reclaim"
	claimEventRelease = "release"
)

// markClaimStatusSet adds the D-464 marker to patch when cur is in an open
// claim chain: the entry coord's claimBefore would read (the newest claim
// or reclaim carrying before, never past a release) gets
// before.status_set = true; a chain whose entries carry no before (claimed
// by an older binary) gets one on its newest claim. A row never claimed,
// or whose last claim was released, is left alone. An unreadable log is
// left alone too: the release then falls back as it did before.
func markClaimStatusSet(cur row.Row, patch map[string]value.Value) error {
	v := cur.Fields[fieldClaimLog]
	if v == nil || v.Kind() == value.KindNull {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil //nolint:nilerr // unreadable log: nothing to mark (see doc)
	}
	var log []map[string]json.RawMessage
	if json.Unmarshal(raw, &log) != nil {
		return nil
	}
	i := openClaimEntry(log)
	if i < 0 {
		return nil
	}
	before := map[string]json.RawMessage{}
	if b, ok := log[i][claimKeyBefore]; ok && string(b) != "null" {
		if json.Unmarshal(b, &before) != nil {
			return nil
		}
	}
	if string(before[claimKeyStatusSet]) == "true" {
		return nil // already marked: no write of its own
	}
	before[claimKeyStatusSet] = json.RawMessage("true")
	if _, ok := before[claimKeyStatus]; !ok {
		before[claimKeyStatus] = json.RawMessage(`""`) // ClaimBefore.Status has no omitempty
	}
	enc, err := json.Marshal(before)
	if err != nil {
		return errs.Wrap(errs.Internal, "claim_log before", err)
	}
	log[i][claimKeyBefore] = enc
	out, err := json.Marshal(log)
	if err != nil {
		return errs.Wrap(errs.Internal, "claim_log", err)
	}
	val, err := value.JSONOf(out)
	if err != nil {
		return errs.Wrap(errs.Internal, "claim_log", err)
	}
	patch[fieldClaimLog] = val
	return nil
}

// openClaimEntry is the index of the claim_log entry the marker goes on,
// or -1 when no claim chain is open. It walks the same way as coord's
// claimBefore (newest first, stop at a release, first claim/reclaim with
// before); with none carrying before it takes the newest claim/reclaim.
func openClaimEntry(log []map[string]json.RawMessage) int {
	newest := -1
	for i := len(log) - 1; i >= 0; i-- {
		var ev string
		if json.Unmarshal(log[i][claimKeyEvent], &ev) != nil {
			continue
		}
		switch ev {
		case claimEventRelease:
			return newest
		case claimEventClaim, claimEventReclaim:
			if b, ok := log[i][claimKeyBefore]; ok && string(b) != "null" {
				return i
			}
			if newest < 0 {
				newest = i
			}
		}
	}
	return newest
}
