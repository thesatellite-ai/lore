package attach

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestStoreErrText (STORE_SERVER_SPEC 9.4 delta, memparity "attach
// storeErr text"): a store that cannot be attached answers an error whose
// message names the store and the reason, keeps the cause's code and hint;
// the reason is the cause's own lore message, and a foreign cause (driver,
// git or os text that may hold a path or a url with a credential) is named
// only by its code, so nothing of its text reaches the person.
func TestStoreErrText(t *testing.T) {
	t.Parallel()
	tok := "ghp_" + strings.Repeat("C", 24)
	foreign := errors.New("dial https://mira:" + tok + "@git.example/acme: refused")
	cases := []struct {
		name     string
		cause    error
		code     errs.Code
		msg      string // exact Message
		hint     string
		mustMiss []string // text that must appear nowhere in Error()
	}{
		{name: "lore cause with hint", cause: errs.New(errs.StoreUnreachable, "no local copy and offline").WithHint("lore sync when online"),
			code: errs.StoreUnreachable, msg: "attach: store agency could not be opened: no local copy and offline", hint: "lore sync when online"},
		{name: "lore cause without hint", cause: errs.New(errs.NotAStore, "the folder has no lore marker"),
			code: errs.NotAStore, msg: "attach: store agency could not be opened: the folder has no lore marker"},
		{name: "lore cause wrapped by fmt", cause: fmt.Errorf("open: %w", errs.New(errs.StoreScheme, "unknown scheme s3")),
			code: errs.StoreScheme, msg: "attach: store agency could not be opened: unknown scheme s3"},
		{name: "foreign cause", cause: foreign,
			code: errs.Internal, msg: "attach: store agency could not be opened: " + string(errs.Internal), mustMiss: []string{tok, "mira", "git.example"}},
		{name: "foreign cause under a lore code", cause: errs.Wrap(errs.StoreUnreachable, "", foreign),
			code: errs.StoreUnreachable, msg: "attach: store agency could not be opened: " + string(errs.StoreUnreachable), mustMiss: []string{tok, "mira"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := storeErr("agency", c.cause)
			var le *errs.Error
			if !errors.As(err, &le) {
				t.Fatalf("not a lore error: %v", err)
			}
			if le.Code != c.code || le.Message != c.msg || le.Hint != c.hint {
				t.Fatalf("got code %s msg %q hint %q, want %s %q %q", le.Code, le.Message, le.Hint, c.code, c.msg, c.hint)
			}
			for _, m := range c.mustMiss {
				if strings.Contains(le.Message, m) || strings.Contains(le.Hint, m) {
					t.Fatalf("the person-facing text leaks %q: %q / %q", m, le.Message, le.Hint)
				}
			}
		})
	}
}

// TestOpenNamesTheFailingStore: Open's error for a backend that fails to
// open is storeErr's (the store named, a foreign cause only by its code).
func TestOpenNamesTheFailingStore(t *testing.T) {
	t.Parallel()
	tok := "ghp_" + strings.Repeat("D", 24)
	openers := store.NewOpeners()
	if err := openers.Register("bad", func(context.Context, *url.URL, store.OpenOptions) (store.Backend, error) {
		return nil, errors.New("open https://x:" + tok + "@h/r failed")
	}); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]byte("stores:\n  - name: broken\n    url: bad://r\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(context.Background(), c, openers, store.OpenOptions{})
	var le *errs.Error
	if !errors.As(err, &le) || !strings.Contains(le.Message, "store broken could not be opened") || strings.Contains(le.Message, tok) {
		t.Fatalf("err = %v", err)
	}
}
