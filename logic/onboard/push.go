package onboard

// push.go — the push mode question of the personal store step (4.4) and
// the Start a team step (4.6): when a store's writes reach its remote
// (logic/storepush: now | batch | manual). The setup offers the mode only;
// batch keeps the default interval (storepush.DefaultPushEvery), and
// push_every and compaction stay with `lore store set`.
//
//	"" or manual ─▶ no setting (manual is the default, C5: nothing reaches
//	                a remote until the person runs lore sync; nothing written)
//	batch        ─▶ push=batch
//	now          ─▶ push=now (an opt-in the person chooses, never the default)
//	other        ─▶ error naming the value and the three modes

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/storepush"
)

// pushChoices are the push question's options, the default first.
func pushChoices() ChoiceSource {
	every := storepush.FormatAge(storepush.DefaultPushEvery)
	return ChoiceSource{Static: []Choice{
		{Value: string(storepush.ModeManual), Label: "Wait for lore sync", Detail: "writes are committed locally and sent only when you run lore sync (default)"},
		{Value: string(storepush.ModeBatch), Label: "Batch", Detail: "writes wait locally; lore pushes them by itself every " + every + " and on lore sync"},
		{Value: string(storepush.ModeNow), Label: "Now", Detail: "lore pushes each write at once, without asking"},
	}}
}

// PushSettings is the store settings a push answer asks for: none for an
// empty answer or the default (manual needs no key, so choosing it writes
// nothing), push=<mode> for batch and now. Any other value is refused
// naming it.
func PushSettings(answer string) ([]storepush.Assignment, error) {
	v := strings.ToLower(strings.TrimSpace(answer))
	if v == "" || v == string(storepush.Default().Push) {
		return nil, nil
	}
	m, err := storepush.ParseMode(v)
	if err != nil {
		return nil, err
	}
	return []storepush.Assignment{{Key: storepush.KeyPush, Value: string(m)}}, nil
}
