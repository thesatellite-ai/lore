package rowref

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Row ids the leak cases point at (memories: a known table).
const (
	hiddenID  = "mem_01a1a5aca7f57659be7320c5f4b70011" // only in agency's root folder
	visibleID = "mem_01a1a5aca7f57659be7320c5f4b70022" // loaded, in the repo
	deletedID = "mem_01a1a5aca7f57659be7320c5f4b70033" // loaded tombstone
	missingID = "mem_01a1a5aca7f57659be7320c5f4b70044" // nowhere
)

// fakeEnv is a checkout whose Get sees only visibleID/deletedID (the repo
// store) and nothing else. It never writes: creates counts Create calls so
// a test can prove a refusal wrote nothing.
type fakeEnv struct {
	creates int
}

func (f *fakeEnv) Get(_ context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	switch ref.ID.String() {
	case visibleID:
		return store.SourceRepo, row.Row{}, nil
	case deletedID:
		return store.SourceRepo, row.Row{Deleted: true}, nil
	}
	return "", row.Row{}, errs.New(errs.NotFound, "no row")
}

func (f *fakeEnv) Create(context.Context, store.Source, row.Table, map[string]value.Value) (engine.Written, error) {
	f.creates++
	return engine.Written{}, nil
}

func (f *fakeEnv) List(context.Context, store.Source, row.Table) ([]row.Row, error) {
	return nil, nil
}

func (f *fakeEnv) Stores() []store.Source { return []store.Source{store.SourceRepo, "agency"} }

// locating adds the hidden-row lookup (wire.App.LocateHidden) and wire's
// StoreClass answer: the repo is a client repo, agency an internal org
// store whose root folder this checkout does not attach.
type locating struct {
	fakeEnv
	hit      engine.HiddenHit
	found    bool
	lookErr  error
	classErr error
	class    map[store.Source]direction.Class
}

func (l *locating) LocateHidden(_ context.Context, ref engine.Ref) (engine.HiddenHit, bool, error) {
	if l.lookErr != nil {
		return engine.HiddenHit{}, false, l.lookErr
	}
	if ref.ID.String() == hiddenID && l.found {
		return l.hit, true, nil
	}
	return engine.HiddenHit{}, false, nil
}

func (l *locating) StoreClass(_ context.Context, name store.Source) (direction.Class, error) {
	if l.classErr != nil {
		return "", l.classErr
	}
	c, ok := l.class[name]
	if !ok {
		return "", errors.New("unplaced store " + string(name))
	}
	return c, nil
}

func clientRepo() *locating {
	return &locating{
		hit:   engine.HiddenHit{Store: "agency", Folder: "org"},
		found: true,
		class: map[store.Source]direction.Class{
			store.SourceRepo: direction.Client,
			"":               direction.Client,
			"agency":         direction.Internal,
			"me":             direction.Personal,
		},
	}
}

// parseCheck runs the --ref helper the way a noun does before writing:
// Parse, then Check against the store the pointing row goes to.
func parseCheck(env Env, from store.Source, raw string) error {
	ctx := context.Background()
	refs, err := Parse(ctx, env, []string{raw})
	if err != nil {
		return err
	}
	return Check(ctx, env, from, refs)
}

// TestHiddenRefLeak is the F15 leak lookup (p2-oldsync, final smoke): a
// client repo's --ref at a row that sits only in a folder of an attached
// org store this checkout does not attach is E_LEAK_BLOCKED (guard exit),
// naming where the row is; the same ref written --to the org store is
// allowed; an id no store holds stays E_NOT_FOUND; any lookup or class
// error refuses (fail closed); nothing is ever written by Parse/Check.
func TestHiddenRefLeak(t *testing.T) {
	lookErr := errs.New(errs.StoreUnreachable, "agency: fetch failed")
	cases := []struct {
		name string
		env  func() *locating
		from store.Source
		ref  string
		code errs.Code // "" = allowed
		msg  []string  // substrings the refusal names
	}{
		{"client repo to hidden internal row", clientRepo, store.SourceRepo, hiddenID, errs.LeakBlocked, []string{hiddenID, "store agency", "folder org", "not attached here"}},
		{"default target counts as the repo", clientRepo, "", hiddenID, errs.LeakBlocked, []string{"store agency"}},
		{"--to the org store is allowed", clientRepo, "agency", hiddenID, "", nil},
		{"personal row to hidden internal row allowed", clientRepo, "me", hiddenID, "", nil},
		{"hidden row in a personal store refused from internal", func() *locating {
			l := clientRepo()
			l.hit = engine.HiddenHit{Store: "me", Folder: "notes"}
			return l
		}, "agency", hiddenID, errs.LeakBlocked, []string{"store me", "folder notes"}},
		{"hidden row in a client-class store is fine from the repo", func() *locating {
			l := clientRepo()
			l.class["agency"] = direction.Client
			return l
		}, store.SourceRepo, hiddenID, "", nil},
		{"unknown id stays not found", clientRepo, store.SourceRepo, missingID, errs.NotFound, []string{missingID}},
		{"hidden lookup misses → not found", func() *locating {
			l := clientRepo()
			l.found = false
			return l
		}, store.SourceRepo, hiddenID, errs.NotFound, nil},
		{"lookup error refuses with its own code", func() *locating {
			l := clientRepo()
			l.lookErr = lookErr
			return l
		}, store.SourceRepo, hiddenID, errs.StoreUnreachable, nil},
		{"class error refuses", func() *locating {
			l := clientRepo()
			l.classErr = errs.New(errs.LinkUnknown, "links unreadable")
			return l
		}, store.SourceRepo, hiddenID, errs.LinkUnknown, nil},
		{"unplaced hidden store refuses", func() *locating {
			l := clientRepo()
			l.hit = engine.HiddenHit{Store: "ghost", Folder: "x"}
			return l
		}, store.SourceRepo, hiddenID, errs.Internal, nil},
		{"loaded row is not a hidden lookup", clientRepo, store.SourceRepo, visibleID, "", nil},
		{"deleted loaded row refused as not found", clientRepo, store.SourceRepo, deletedID, errs.NotFound, []string{"deleted"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := c.env()
			err := parseCheck(env, c.from, c.ref)
			if c.code == "" {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if c.code == errs.Internal {
				// a plain error from StoreClass carries no code: refusing is
				// what matters (fail closed).
				if err == nil {
					t.Fatal("want a refusal, got nil")
				}
			} else if got := errs.CodeOf(err); got != c.code {
				t.Fatalf("code %q (%v), want %q", got, err, c.code)
			}
			for _, m := range c.msg {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("error %q does not name %q", err, m)
				}
			}
			if env.creates != 0 {
				t.Errorf("refusal wrote %d rows", env.creates)
			}
		})
	}
}

// TestHiddenRefLeakExit pins E_LEAK_BLOCKED's class and exit: a guard
// refusal (19), HTTP 403 — what the CLI and REST print for the refusal.
func TestHiddenRefLeakExit(t *testing.T) {
	err := parseCheck(clientRepo(), store.SourceRepo, hiddenID)
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("want *errs.Error, got %v", err)
	}
	d := e.Def()
	if d.Class != errs.ClassGuard || d.Exit != 19 || d.HTTP != 403 {
		t.Errorf("E_LEAK_BLOCKED def = %+v, want guard/19/403", d)
	}
}

// TestHiddenRefNoLocator: an Env without the hidden lookup (a test env,
// an old binary) answers E_NOT_FOUND for a row it cannot see, as before.
func TestHiddenRefNoLocator(t *testing.T) {
	env := &fakeEnv{}
	err := parseCheck(env, store.SourceRepo, hiddenID)
	if errs.CodeOf(err) != errs.NotFound {
		t.Fatalf("want E_NOT_FOUND, got %v", err)
	}
	if !strings.Contains(err.Error(), "no row "+hiddenID) {
		t.Errorf("message %q", err)
	}
}

// TestHiddenRefQualifiedSkipsLookup: a <prj_…>:<id> pointer may name a row
// outside this checkout (v0.1.17) and is not looked up at all.
func TestHiddenRefQualifiedSkipsLookup(t *testing.T) {
	env := clientRepo()
	env.lookErr = errors.New("must not be called")
	err := parseCheck(env, store.SourceRepo, "prj_01a1a5aca7f57659be7320c5f4b70055:"+missingID)
	if err != nil {
		t.Fatalf("qualified ref: %v", err)
	}
}
