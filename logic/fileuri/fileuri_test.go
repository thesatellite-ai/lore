package fileuri

import "testing"

// TestURLPath: a drive path gains the slash that keeps "C:" out of the
// url's host; a unix path and a UNC slash path are unchanged.
func TestURLPath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"C:/Users/a/x.db":     "/C:/Users/a/x.db",
		"d:/x":                "/d:/x",
		"/home/a/x.db":        "/home/a/x.db",
		"//server/share/x.db": "//server/share/x.db",
		"/C:/x":               "/C:/x",
		"1:/x":                "1:/x",
		"C":                   "C",
		"":                    "",
	} {
		if got := URLPath(in); got != want {
			t.Errorf("URLPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestURL: the printed url never has a host for an absolute path, and
// names that look like url syntax are escaped.
func TestURL(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ p, q, want string }{
		{"C:/Users/a b/x.db", "mode=ro", "file:///C:/Users/a%20b/x.db?mode=ro"},
		{"/home/a#b?c/x.db", "", "file:///home/a%23b%3Fc/x.db"},
		{"/home/a/x.db", "mode=ro", "file:///home/a/x.db?mode=ro"},
	} {
		if got := URL(c.p, c.q); got != c.want {
			t.Errorf("URL(%q, %q) = %q, want %q", c.p, c.q, got, c.want)
		}
	}
}

// TestOSPathFor: the way back, per operating system, and the round trip
// of a Windows drive path.
func TestOSPathFor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ goos, in, want string }{
		{"windows", "/C:/Users/a/x.db", `C:\Users\a\x.db`},
		{"windows", "/c:/x", `c:\x`},
		{"windows", "//server/share/x", `\\server\share\x`},
		{"windows", "/1:/x", `\1:\x`},
		{"linux", "/C:/x", "/C:/x"},
		{"darwin", "/home/a/x.db", "/home/a/x.db"},
	} {
		if got := OSPathFor(c.goos, c.in); got != c.want {
			t.Errorf("OSPathFor(%s, %q) = %q, want %q", c.goos, c.in, got, c.want)
		}
	}
	if got := OSPathFor("windows", URLPath("C:/Users/a/x.db")); got != `C:\Users\a\x.db` {
		t.Errorf("round trip = %q", got)
	}
}
