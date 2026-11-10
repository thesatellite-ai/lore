package reveal

import (
	"context"
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakePort resolves targets from a map and records every Open.
type fakePort struct {
	places map[Target]Place
	opened [][]string
}

func (f *fakePort) MachinePath(_ context.Context, t Target) (Place, error) { return f.places[t], nil }

func (f *fakePort) Open(_ context.Context, argv []string) error {
	f.opened = append(f.opened, argv)
	return nil
}

func noDisplay(string) string { return "" }

func display(k string) string {
	if k == envDisplay {
		return ":0"
	}
	return ""
}

// TestArgv: every platform × file/folder, and Linux without a display.
func TestArgv(t *testing.T) {
	t.Parallel()
	file := Place{Path: "/h/.lore/config.yaml", Exists: true}
	dir := Place{Path: "/h/.lore", Exists: true, IsDir: true}
	cases := []struct {
		name   string
		goos   string
		p      Place
		getenv func(string) string
		want   []string
		ok     bool
	}{
		{"darwin file is revealed", "darwin", file, noDisplay, []string{"open", "-R", "/h/.lore/config.yaml"}, true},
		{"darwin folder is opened", "darwin", dir, noDisplay, []string{"open", "/h/.lore"}, true},
		{"windows file is selected", "windows", file, noDisplay, []string{"explorer", "/select,/h/.lore/config.yaml"}, true},
		{"windows folder", "windows", dir, noDisplay, []string{"explorer", "/h/.lore"}, true},
		{"linux file opens its folder", "linux", file, display, []string{"xdg-open", "/h/.lore"}, true},
		{"linux folder", "linux", dir, display, []string{"xdg-open", "/h/.lore"}, true},
		{"linux without a display", "linux", dir, noDisplay, nil, false},
		{"freebsd with wayland", "freebsd", dir, func(k string) string {
			if k == envWayland {
				return "wayland-0"
			}
			return ""
		}, []string{"xdg-open", "/h/.lore"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Argv(c.goos, c.p, c.getenv)
			if ok != c.ok || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Argv = %v %v, want %v %v", got, ok, c.want, c.ok)
			}
		})
	}
}

// invoke runs the op named name with JSON-ish args over e.
func invoke(t *testing.T, ctx context.Context, e engine, name string, args ...string) (op.Result, error) {
	t.Helper()
	for _, c := range e.ops() {
		if c.Spec().Name == name {
			in, err := c.Decode(op.ArgsDecoder{Args: args})
			if err != nil {
				return op.Result{}, err
			}
			return c.Invoke(ctx, in)
		}
	}
	t.Fatalf("no op %s", name)
	return op.Result{}, nil
}

// TestReveal: each target resolves to its own path and only that path is
// opened; unknown, missing, placeless and lore-api calls are refused
// without opening anything.
func TestReveal(t *testing.T) {
	t.Parallel()
	places := map[Target]Place{
		TargetLoreHome:   {Path: "/h/.lore", Exists: true, IsDir: true},
		TargetConfigFile: {Path: "/h/.lore/config.yaml", Exists: true},
		TargetRepoLore:   {Path: "/src/app/.lore", Exists: false, IsDir: true},
	}
	bg := context.Background()
	cases := []struct {
		name   string
		ctx    context.Context
		target string
		places map[Target]Place
		want   []string
		code   errs.Code
	}{
		{"lore home", bg, "lore_home", places, []string{"open", "/h/.lore"}, ""},
		{"config file", bg, "config_file", places, []string{"open", "-R", "/h/.lore/config.yaml"}, ""},
		{"setup page over REST", op.WithPage(op.WithRemote(bg)), "config_file", places, []string{"open", "-R", "/h/.lore/config.yaml"}, ""},
		{"lore api is refused", op.WithRemote(bg), "config_file", places, nil, errs.Unsupported},
		{"unknown target", bg, "/etc/passwd", places, nil, errs.InvalidInput},
		{"missing .lore", bg, "repo_lore", places, nil, errs.NotFound},
		{"not in a repo", bg, "repo_lore", map[Target]Place{}, nil, errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := &fakePort{places: c.places}
			e := engine{p: p, goos: goosDarwin, getenv: noDisplay}
			_, err := invoke(t, c.ctx, e, OpReveal, c.target)
			if c.code != "" {
				if errs.CodeOf(err) != c.code || len(p.opened) != 0 {
					t.Fatalf("err %v opened %v, want %s and nothing opened", err, p.opened, c.code)
				}
				return
			}
			if err != nil || len(p.opened) != 1 || !reflect.DeepEqual(p.opened[0], c.want) {
				t.Fatalf("err %v opened %v, want %v", err, p.opened, c.want)
			}
		})
	}
}

// TestPathList lists every target in order, "" for one with no place.
func TestPathList(t *testing.T) {
	t.Parallel()
	p := &fakePort{places: map[Target]Place{TargetLoreHome: {Path: "/h/.lore", Exists: true, IsDir: true}}}
	res, err := invoke(t, context.Background(), engine{p: p, goos: goosDarwin, getenv: noDisplay}, OpPathList)
	if err != nil {
		t.Fatal(err)
	}
	b, err := res.JSON()
	want := `{"paths":[{"target":"lore_home","path":"/h/.lore","exists":true},{"target":"config_file","path":"","exists":false},{"target":"repo_lore","path":"","exists":false}]}`
	if err != nil || string(b) != want {
		t.Fatalf("json %s %v", b, err)
	}
}
