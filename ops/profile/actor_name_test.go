package profile

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestNameFlags: --name and --display-name set one field; the same value
// twice is fine, two different values are refused before anything opens.
func TestNameFlags(t *testing.T) {
	t.Parallel()
	got, err := nameFlags(ActorEditIn{Actor: "@mira", Name: "Mira Patel"})
	if err != nil || got.DisplayName != "Mira Patel" {
		t.Fatalf("--name alone: %+v %v", got, err)
	}
	if _, err = nameFlags(ActorEditIn{Name: "A", DisplayName: "A"}); err != nil {
		t.Fatalf("the same value twice: %v", err)
	}
	if _, err = nameFlags(ActorEditIn{Name: "A", DisplayName: "B"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("two names: %v, want E_INVALID_INPUT", err)
	}
}
