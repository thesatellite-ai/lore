package main

// inbox_metakeys.go — the `lore inbox` review-queue line (AGENT_IDENTITY
// 8.3, METADATA 3.14, CLI_REFERENCE `lore inbox`):
//
//	attached stores ─► opssettings.UnreviewedKeys (the meta keys --unreviewed set)
//	               ─► who wrote each (the agent actor's "tomas via codex" label)
//	               ─► one "metadata" item:
//	   N keys await review (added by tomas via codex): a, b, c — lore meta keys --unreviewed
//
// The line is a queue, not news: it carries no time, so --ack and --since
// never hide it and it shows until someone reviews the keys (P3-AGENT-2).
// Anyone may review (agents too), so every caller sees it.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	lschema "github.com/thesatellite-ai/lore/schema"
)

// inboxMetadata is the kind of the review-queue item (8 wide, the inbox
// kind column).
const inboxMetadata = "metadata"

// metaReviewHint is the command that lists and explains the keys.
const metaReviewHint = "lore meta keys --unreviewed"

// metaReviewItems returns the review-queue item, or none when every key is
// reviewed or the core is not mounted. A read error hides the line rather
// than the whole inbox (like the comments of mentionItems).
func (cv *coordView) metaReviewItems(ctx context.Context) []inboxItem {
	if coreApp == nil {
		return nil
	}
	keys, err := opssettings.UnreviewedKeys(ctx, coreApp)
	if err != nil || len(keys) == 0 {
		return nil
	}
	return []inboxItem{{Kind: inboxMetadata, Text: metaReviewText(keys, func(id string) string { return cv.keyAuthor(ctx, id) })}}
}

// keyAuthor names the writer of a key row: an agent actor's own label
// ("tomas via codex", OQ8/OQ9), else the usual actor label.
func (cv *coordView) keyAuthor(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if d, ok, err := coreDoc(ctx, lschema.TableActors, id); err == nil && ok && d.Str(lschema.FieldActorKind) == lschema.ActorKindAgent {
		if l := d.Str(fieldLabel); l != "" {
			return l
		}
	}
	return cv.actorLabel(ctx, id)
}

// metaReviewText is the item text: the count, the distinct authors in
// first-seen order, and the key names (one per name, in the given order).
func metaReviewText(keys []opssettings.UnreviewedKey, author func(id string) string) string {
	var names, who []string
	for _, k := range keys {
		if !slices.Contains(names, k.Name) {
			names = append(names, k.Name)
		}
		if a := author(k.CreatedBy); a != "" && !slices.Contains(who, a) {
			who = append(who, a)
		}
	}
	noun := "keys await"
	if len(names) == 1 {
		noun = "key awaits"
	}
	by := ""
	if len(who) > 0 {
		by = " (added by " + strings.Join(who, ", ") + ")"
	}
	return fmt.Sprintf("%d %s review%s: %s — %s", len(names), noun, by, strings.Join(names, ", "), metaReviewHint)
}
