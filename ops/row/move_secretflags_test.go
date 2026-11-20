package row

import "testing"

// TestMoveOffersTheSecretAnswers (devco:D-762): row.move's bulk form runs
// a secret scan whose refusal prints the move again with
// --secret-unstored, --secret-ref and --not-a-secret; the surfaces offer
// those answers only on an op whose write path may ask (Confirm.MayAsk).
// Its spec said Always alone, so `lore move` refused each printed rerun as
// an unknown flag.
func TestMoveOffersTheSecretAnswers(t *testing.T) {
	t.Parallel()
	for _, e := range Ops(nil) {
		s := e.Spec()
		if s.Name != OpMove {
			continue
		}
		if !s.Confirm.MayAsk || s.Confirm.NoSecretFindings || !s.Confirm.Always {
			t.Fatalf("row.move confirm %+v: want Always and MayAsk (secret answers offered)", s.Confirm)
		}
		return
	}
	t.Fatalf("no %s op", OpMove)
}
