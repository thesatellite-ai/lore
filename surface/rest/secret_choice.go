package rest

import (
	"net/http"
	"strconv"
	"unicode"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// The secret answer headers (SECRETS 4 over REST; DECISION P2-CTX-5): the
// same choice the CLI flags --not-a-secret --reason, --secret-ref and
// --secret-unstored give, carried in headers so they never clash with an
// operation's own input fields (an op with its own "reason" field).
const (
	// HeaderNotASecret keeps detected text as a false positive; its value
	// is the typed reason (required, D12).
	HeaderNotASecret = "X-Lore-Not-A-Secret"
	// HeaderSecretRef redacts findings with this reference; repeat the
	// header once per finding, in finding order.
	HeaderSecretRef = "X-Lore-Secret-Ref"
	// HeaderSecretUnstored ("true") redacts every finding as
	// ${secret:unstored} and adds the store-then-rotate task.
	HeaderSecretUnstored = "X-Lore-Secret-Unstored"
	// maxSecretHeaderLen caps each header value.
	maxSecretHeaderLen = 1024
)

// secretChoiceOf reads the secret answer headers. The engine validates
// the choice itself (one kind only, a reference per finding).
func secretChoiceOf(r *http.Request) (op.SecretChoice, error) {
	var ch op.SecretChoice
	check := func(name, v string) error {
		if len(v) > maxSecretHeaderLen {
			return errs.New(errs.InvalidInput, name+" is longer than "+strconv.Itoa(maxSecretHeaderLen)+" bytes")
		}
		for _, c := range v {
			if !unicode.IsPrint(c) {
				return errs.New(errs.InvalidInput, name+" holds a control character")
			}
		}
		return nil
	}
	if vs, ok := r.Header[http.CanonicalHeaderKey(HeaderNotASecret)]; ok {
		for _, v := range vs {
			if err := check(HeaderNotASecret, v); err != nil {
				return ch, err
			}
		}
		ch.NotASecret = true
		if len(vs) > 0 {
			ch.Reason = vs[len(vs)-1]
		}
	}
	for _, v := range r.Header.Values(HeaderSecretRef) {
		if err := check(HeaderSecretRef, v); err != nil {
			return ch, err
		}
		ch.Refs = append(ch.Refs, v)
	}
	if v := r.Header.Get(HeaderSecretUnstored); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return ch, errs.New(errs.InvalidInput, HeaderSecretUnstored+" must be true or false")
		}
		ch.Unstored = b
	}
	return ch, nil
}
