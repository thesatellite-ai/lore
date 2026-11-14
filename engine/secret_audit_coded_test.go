package engine

// secret_audit_coded_test.go — a secret audit record refused for a reason
// the person can fix (an unwritable ~/.lore: E_READ_ONLY_FS naming the
// folder) keeps that code and those words; before, every failure became
// E_INTERNAL "secret audit record failed" and named nothing.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestAuditSecretKeepsACodedRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		cause error
		code  errs.Code
		says  string
	}{
		{"read-only home", errs.New(errs.ReadOnlyFS, "cannot create /h/.lore/state: permission denied").WithHint("chmod u+rwx /h/.lore"), errs.ReadOnlyFS, "/h/.lore/state"},
		{"plain error", errors.New("disk on fire"), errs.Internal, "secret audit record failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sink := &flakySink{}
			f := setup(t, WithAudit(sink))
			sink.setFail(c.cause)
			err := f.eng.AuditSecret(context.Background(), ActionSecretRuleChanged, SecretAudit{})
			if got := errs.CodeOf(err); got != c.code {
				t.Fatalf("code = %s, want %s (%v)", got, c.code, err)
			}
			if env := errs.EnvelopeOf(err); !strings.Contains(env.Error.Message+" "+env.Error.Hint, c.says) {
				t.Fatalf("envelope %+v does not say %q", env, c.says)
			}
		})
	}
}
