package store

import (
	"context"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Opener builds a Backend from a store url (.lore/stores.yaml).
type Opener func(ctx context.Context, u *url.URL, opts OpenOptions) (Backend, error)

// OpenOptions carries what every backend may need, nothing backend-specific.
type OpenOptions struct {
	Home     string // the lore home (~/.lore)
	ReadOnly bool   // read_only: true in stores.yaml
	// AllowNetworkFS lets a store live inside a cloud-sync folder or on a
	// network file system, which is otherwise refused with E_NETWORK_FS
	// (locks are not honoured and renames are not atomic there). It is
	// the user's explicit override (v0.1.17's LORE_ALLOW_NETWORK_FS=1);
	// false, the zero value, keeps the refusal.
	AllowNetworkFS bool
	// LockTimeout is how long a write waits for the store lock before
	// E_STORE_LOCKED (`store.lock_timeout` in ~/.lore/config.yaml, F63,
	// FA-AGENT-8); 0 keeps the backend's default (10 s).
	LockTimeout time.Duration
}

// Openers maps url schemes to Openers. It is a value built by the binary's
// main (NewOpeners, then Register per backend), never a package global, so
// tests and embedders choose their own backends and nothing registers
// itself behind the caller's back.
type Openers struct {
	mu      sync.RWMutex
	schemes map[string]Opener
}

// NewOpeners returns an empty set.
func NewOpeners() *Openers { return &Openers{schemes: map[string]Opener{}} }

// Register adds the Opener for scheme ("git", "ssh", "https", "file"). A
// second Opener for one scheme is an error, so two backends can never fight
// over one url.
func (o *Openers) Register(scheme string, open Opener) error {
	if scheme == "" || open == nil {
		return errs.New(errs.InvalidInput, "store: register needs a scheme and an opener")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, dup := o.schemes[scheme]; dup {
		return errs.New(errs.InvalidInput, "store: scheme "+scheme+" registered twice")
	}
	o.schemes[scheme] = open
	return nil
}

// Schemes lists the registered schemes, sorted.
func (o *Openers) Schemes() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]string, 0, len(o.schemes))
	for s := range o.schemes {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// Open parses rawURL and opens it with the Opener of its scheme; an unknown
// scheme is ErrScheme (E_STORE_SCHEME). The url never appears in the error:
// it may carry a token.
func (o *Openers) Open(ctx context.Context, rawURL string, opts OpenOptions) (Backend, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return nil, errs.New(errs.StoreScheme, "store: the url has no scheme")
	}
	o.mu.RLock()
	open, ok := o.schemes[u.Scheme]
	o.mu.RUnlock()
	if !ok {
		return nil, errs.Wrap(errs.StoreScheme, "store: no backend for scheme "+u.Scheme, ErrScheme)
	}
	return open(ctx, u, opts)
}
