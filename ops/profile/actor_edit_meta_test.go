package profile

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestActorPatchMetaOnly pins owner org-setup item 3 (devco D-862): an
// actor edit whose only flags are --meta is a real change (the empty
// patch is the write its metadata rides on, named "meta" in the answer),
// while an edit with no flag at all is still "nothing to change".
func TestActorPatchMetaOnly(t *testing.T) {
	t.Parallel()
	patch, changed, err := actorPatch(ActorEditIn{Actor: "@mira", RowMetaFlags: engine.RowMetaFlags{Meta: []string{"desk=4F"}}})
	if err != nil || len(patch) != 0 || !slices.Equal(changed, []string{fieldMeta}) {
		t.Fatalf("--meta only: patch %v changed %v err %v; want an empty patch and changed [meta]", patch, changed, err)
	}
	_, changed, err = actorPatch(ActorEditIn{Actor: "@mira", Title: "Lead", RowMetaFlags: engine.RowMetaFlags{MetaJSON: []string{`desk={"floor":4}`}}})
	if err != nil || !slices.Equal(changed, []string{"title", fieldMeta}) {
		t.Fatalf("--title with --meta-json: changed %v err %v; want [title meta]", changed, err)
	}
	if _, _, err = actorPatch(ActorEditIn{Actor: "@mira"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("no flag: %v, want E_INVALID_INPUT nothing to change", err)
	}
}
