//go:build userscen

package userscen

// render_targets_test.go — tool profiles and pointer files (logic/toolprofile,
// TOOL NEUTRAL, Q-R2-3):
//   - every built-in profile: its pointer file, its import style (@import,
//     read sentence, Cursor .mdc with frontmatter first), its budget; the
//     generic profile writes only LORE.md;
//   - hand-written text outside lore's fenced block survives, before and
//     after the block, a second render, an edit of the hand text, and an
//     edit inside the block (which render puts back);
//   - broken fences: a start marker with no end, an end marker with no
//     start, two blocks, a file with no trailing newline, CRLF + BOM;
//   - target selection: AGENTS.md fallback, a detected CLAUDE.md, the
//     context.targets row (and its refusals: unknown name, empty), a
//     user profile from ~/.lore/config.yaml and invalid user profiles;
//   - a context.targets row naming a profile this machine lacks: render
//     refuses and names the profile (fixed: the message had no cause).

import (
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// renderProfiles is every built-in profile: its pointer file, the exact
// file content a fresh render writes there, and its budget (toolprofile
// Builtins: claude_code and gemini_cli 32000, the others 16000).
var renderProfiles = []struct {
	name, file, content string
	budget              string
}{
	{"claude_code", "CLAUDE.md", renderPtrAt, "budget 32000 claude_code"},
	{"codex", "AGENTS.md", renderPtrRead, "budget 16000 codex"},
	{"cursor", ".cursor/rules/lore.mdc", renderMDCHead + renderPtrRead, "budget 16000 cursor"},
	{"gemini_cli", "GEMINI.md", renderPtrAt, "budget 32000 gemini_cli"},
	{"copilot", ".github/copilot-instructions.md", renderPtrRead, "budget 16000 copilot"},
	{"generic", "", "", "budget 16000 generic"},
}

// renderPointerFiles is every built-in pointer file.
var renderPointerFiles = []string{"CLAUDE.md", "AGENTS.md", ".cursor/rules/lore.mdc", "GEMINI.md", ".github/copilot-instructions.md"}

// renderFreshNoAgents is a solo clone whose AGENTS.md (written by init's
// fallback wiring) is removed, so a render starts from no pointer file.
func renderFreshNoAgents(t *testing.T) *th.Clone {
	t.Helper()
	c := renderSolo(t)
	c.Git("rm", "-q", "--cached", "--ignore-unmatch", "AGENTS.md")
	c.TryGit("clean", "-q", "-f", "AGENTS.md")
	return c
}

func TestRenderEveryProfilePointerFile(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, p := range renderProfiles {
		t.Run(p.name, func(t *testing.T) {
			c := renderFreshNoAgents(t)
			c.MustLore("rule", "add", "--body", "the profile rule", "--severity", "must")
			r := c.MustLore("render", "--target", p.name)
			r.Says(t, p.budget)
			if md := renderRead(t, c, renderLoreMD); !strings.Contains(md, "the profile rule") {
				t.Fatalf("LORE.md is written for every profile: %s", md)
			}
			for _, f := range renderPointerFiles {
				got, ok := c.ReadFile(f)
				switch {
				case f == p.file && got != p.content:
					t.Errorf("%s is exactly lore's pointer for %s:\n%q\nwant\n%q", f, p.name, got, p.content)
				case f != p.file && ok:
					t.Errorf("rendering for %s writes no %s: %q", p.name, f, got)
				}
			}
			// The same target named by its pointer file wires the same
			// profile, and a second render changes nothing.
			if p.file != "" {
				before := renderSnapshot(c, append([]string{renderLoreMD}, renderPointerFiles...)...)
				c.MustLore("render", "--target", p.file).Says(t, "· .lore/LORE.md (unchanged)", "(pointer present)")
				renderSameFiles(t, "render --target "+p.file, before, renderSnapshot(c, append([]string{renderLoreMD}, renderPointerFiles...)...))
			}
		})
	}
}

func TestRenderHandTextOutsideFenceSurvives(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, p := range renderProfiles {
		if p.file == "" {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			c := renderFreshNoAgents(t)
			hand := "# Team notes\n\nRun `make test` before you push.\n\n- ünïcode bullet ✓\n"
			c.WriteFile(p.file, hand)
			c.MustLore("render", "--target", p.name)
			got := renderRead(t, c, p.file)
			block := renderPtrRead
			if p.content == renderPtrAt {
				block = renderPtrAt
			}
			want := block + "\n" + hand // the block goes at the top, a blank line, then the person's text
			if got != want {
				t.Fatalf("%s: lore's block on top, the hand text kept byte for byte:\n%q\nwant\n%q", p.file, got, want)
			}
			// The person edits their text: the next render keeps the edit.
			edited := strings.Replace(got, "make test", "task check", 1) + "\nA line added later.\n"
			c.WriteFile(p.file, edited)
			c.MustLore("render", "--target", p.name).Says(t, "(pointer present)")
			if after := renderRead(t, c, p.file); after != edited {
				t.Fatalf("an edit outside the block survives the next render:\n%q\nwant\n%q", after, edited)
			}
			// Someone edits inside the block: render puts lore's block back
			// and leaves everything else.
			tampered := strings.Replace(edited, block, "<!-- lore:pointer:start -->\nhand-edited inside the fence\n<!-- lore:pointer:end -->\n", 1)
			c.WriteFile(p.file, tampered)
			c.MustLore("render", "--target", p.name)
			if after := renderRead(t, c, p.file); after != edited {
				t.Fatalf("the block is restored, the rest kept:\n%q\nwant\n%q", after, edited)
			}
		})
	}
}

func TestRenderCursorKeepsOwnFrontmatterFirst(t *testing.T) {
	c := renderFreshNoAgents(t)
	own := "---\ndescription: my cursor rule\nglobs: \"*.go\"\n---\nprefer small functions\n"
	c.WriteFile(".cursor/rules/lore.mdc", own)
	c.MustLore("render", "--target", "cursor")
	want := "---\ndescription: my cursor rule\nglobs: \"*.go\"\n---\n" + renderPtrRead + "\nprefer small functions\n"
	if got := renderRead(t, c, ".cursor/rules/lore.mdc"); got != want {
		t.Fatalf("an existing .mdc keeps its own frontmatter first, lore's block right after:\n%q\nwant\n%q", got, want)
	}
}

func TestRenderBrokenFences(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	cases := []struct {
		name, in, want string
	}{
		{
			// No end marker: lore cannot tell where its block ends, so it
			// never deletes anything; it adds a whole block on top.
			name: "start without end",
			in:   "# Top\n<!-- lore:pointer:start -->\nold text with no end\n\nmore hand text\n",
			want: renderPtrAt + "\n# Top\n<!-- lore:pointer:start -->\nold text with no end\n\nmore hand text\n",
		},
		{
			name: "end without start",
			in:   "intro\n<!-- lore:pointer:end -->\nmiddle\n",
			want: renderPtrAt + "\nintro\n<!-- lore:pointer:end -->\nmiddle\n",
		},
		{
			name: "two complete blocks",
			in:   "A\n" + renderPtrAt + "B\n" + renderPtrAt + "C\n",
			want: "A\n" + renderPtrAt + "B\n" + renderPtrAt + "C\n",
		},
		{
			name: "no trailing newline",
			in:   "no trailing newline",
			want: renderPtrAt + "\nno trailing newline",
		},
		{
			name: "CRLF and a byte-order mark",
			in:   "\ufeffBOM file\r\nwindows line\r\n",
			want: renderPtrAt + "\n\ufeffBOM file\r\nwindows line\r\n",
		},
		{
			name: "only the block, stale target path",
			in:   "<!-- lore:pointer:start -->\n@old/place/LORE.md\n<!-- lore:pointer:end -->\n",
			want: renderPtrAt,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := renderFreshNoAgents(t)
			c.WriteFile("CLAUDE.md", tc.in)
			c.MustLore("render", "--target", "claude_code")
			got := renderRead(t, c, "CLAUDE.md")
			if got != tc.want {
				t.Fatalf("CLAUDE.md after render:\n%q\nwant\n%q", got, tc.want)
			}
			// Stable: a second render changes nothing.
			c.MustLore("render", "--target", "claude_code")
			if again := renderRead(t, c, "CLAUDE.md"); again != got {
				t.Fatalf("a second render of a broken fence changes nothing:\n%q\nthen\n%q", got, again)
			}
		})
	}
}

func TestRenderTargetSelection(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	t.Run("no tool file: AGENTS.md, never CLAUDE.md", func(t *testing.T) {
		c := renderFreshNoAgents(t)
		c.MustLore("render").Says(t, "budget 16000 codex")
		if _, ok := c.ReadFile("CLAUDE.md"); ok {
			t.Error("CLAUDE.md is never a default")
		}
		if got := renderRead(t, c, "AGENTS.md"); got != renderPtrRead {
			t.Errorf("AGENTS.md is wired: %q", got)
		}
	})
	t.Run("a CLAUDE.md the repo already has is detected", func(t *testing.T) {
		w := newWorld(t)
		p := w.Human("person", "person@example.com")
		p.WriteConfig(renderSoloConfig())
		c := p.Clone(w.Remote(renderSlugOwn), "tool")
		c.WriteFile("CLAUDE.md", "# existing\n")
		c.CommitAll("claude")
		c.MustLore("init", "--non-interactive", "--name=tool").Says(t, "claude_code")
		c.MustLore("render").Says(t, "budget 32000 claude_code")
		if got := renderRead(t, c, "CLAUDE.md"); got != renderPtrAt+"\n# existing\n" {
			t.Errorf("the detected CLAUDE.md is wired, its text kept: %q", got)
		}
		if _, ok := c.ReadFile("AGENTS.md"); ok {
			t.Error("no AGENTS.md when a tool file was detected")
		}
	})
	t.Run("the context.targets row decides", func(t *testing.T) {
		c := renderFreshNoAgents(t)
		c.MustLore("policy", "set", "context.targets", "gemini_cli,copilot", "--yes")
		c.MustLore("render").Says(t, "budget 16000 copilot", "GEMINI.md", "copilot-instructions.md")
		if _, ok := c.ReadFile("AGENTS.md"); ok {
			t.Error("a target the row does not name is not wired")
		}
		// A hand-written tool file the row does not name: named, never wired.
		c.WriteFile("CLAUDE.md", "# mine\n")
		c.MustLore("render").Says(t, "CLAUDE.md found but not wired to lore; add it with: lore policy set --key context.targets --value \"<profiles incl. claude_code>\" --yes")
		if got := renderRead(t, c, "CLAUDE.md"); got != "# mine\n" {
			t.Errorf("an unlisted tool file is left alone: %q", got)
		}
	})
	t.Run("refusals of the row", func(t *testing.T) {
		c := renderFreshNoAgents(t)
		c.Lore("policy", "set", "context.targets", "nosuch", "--yes").
			Fails(t, 20, "[E_INVALID_INPUT]", `unknown tool profile "nosuch"`, "known: claude_code, codex, cursor, gemini_cli, copilot, generic", "context.profiles")
		c.Lore("policy", "set", "context.targets", "claude_code,Bad-Name", "--yes").
			Fails(t, 20, "[E_INVALID_INPUT]", `"Bad-Name"`)
		// Nothing was stored: the render still uses the fallback.
		c.MustLore("render").Says(t, "budget 16000 codex")
	})
}

// renderProfileConfig is the solo config plus a context.profiles block
// (raw YAML: the harness Config has no context key).
func renderProfileConfig(profiles string) string {
	return "identity:\n  key: human:person@example.com\n  name: Person Example\n  handle: person\n" +
		"personal:\n  store: local\nlive:\n  fetch_ttl: 0s\ncontext:\n  profiles:\n" + profiles
}

func TestRenderUserProfileFromConfig(t *testing.T) {
	c := renderFreshNoAgents(t)
	c.A.WriteHomeFile(".lore/config.yaml", renderProfileConfig(
		"    - name: mytool\n      pointer_file: docs/MYTOOL.md\n      import: read\n      budget: 20000\n"+
			"    - name: attool\n      pointer_file: AT.md\n      import: at\n"))
	c.MustLore("rule", "add", "--body", "the user profile rule", "--severity", "must")
	c.MustLore("render", "--target", "mytool").Says(t, "budget 20000 mytool")
	if got := renderRead(t, c, "docs/MYTOOL.md"); got != renderPtrRead {
		t.Errorf("a read-style user profile writes the read sentence naming the root path: %q", got)
	}
	c.MustLore("render", "--target", "attool").Says(t, "budget 16000 attool") // no budget: the default
	if got := renderRead(t, c, "AT.md"); got != renderPtrAt {
		t.Errorf("an at-style user profile at the root imports .lore/LORE.md: %q", got)
	}
	// The row may name a user profile; render wires it with the built-ins.
	c.MustLore("policy", "set", "context.targets", "mytool,codex", "--yes")
	c.MustLore("render").Says(t, "budget 16000 codex", filepath.FromSlash("docs/MYTOOL.md")+" (pointer present)", "AGENTS.md")

	// The profile disappears from this machine's config (a teammate's
	// row, a cleaned config): render refuses and names the profile.
	c.A.WriteHomeFile(".lore/config.yaml", renderProfileConfig("    []\n"))
	before := renderSnapshot(c, renderLoreMD, "AGENTS.md", "docs/MYTOOL.md")
	c.Lore("render").Fails(t, 20, "[E_INVALID_INPUT]", `the repo's context.targets setting: unknown tool profile "mytool"`, "lore policy set --key context.targets --value <profiles> --yes")
	renderSameFiles(t, "a refused render", before, renderSnapshot(c, renderLoreMD, "AGENTS.md", "docs/MYTOOL.md"))
	j := c.Lore("render", "--json")
	if !strings.Contains(j.Stdout, `"code": "E_INVALID_INPUT"`) || !strings.Contains(j.Stdout, `unknown tool profile \"mytool\"`) {
		t.Errorf("--json names the unknown profile too: %s", j)
	}
}

func TestRenderInvalidUserProfilesRefused(t *testing.T) {
	cases := []struct {
		name, yaml, say string
	}{
		{"built-in name", "    - name: codex\n      pointer_file: X.md\n      import: at\n", `tool profile "codex": the name is already taken`},
		{"inside .lore", "    - name: inlore\n      pointer_file: .lore/X.md\n      import: at\n", `pointer_file ".lore/X.md" must not be inside .lore/`},
		{"outside the repo", "    - name: outside\n      pointer_file: ../X.md\n      import: at\n", `pointer_file "../X.md" must be a slash path inside the repo`},
		{"absolute path", "    - name: abs\n      pointer_file: /tmp/X.md\n      import: at\n", `must be a slash path inside the repo`},
		{"bad name", "    - name: Bad-Name\n      pointer_file: X.md\n      import: at\n", "name must be lowercase letters, digits and _"},
		{"unknown import", "    - name: weird\n      pointer_file: X.md\n      import: magic\n", `import "magic" is not one of at, read, mdc, none`},
		{"no pointer file", "    - name: nofile\n      import: at\n", "pointer_file is required unless import is none"},
		{"pointer file taken", "    - name: dup\n      pointer_file: CLAUDE.md\n      import: at\n", `already belongs to profile "claude_code"`},
		{"negative budget", "    - name: neg\n      pointer_file: N.md\n      import: at\n      budget: -1\n", "budget must not be negative"},
	}
	c := renderFreshNoAgents(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c.A.WriteHomeFile(".lore/config.yaml", renderProfileConfig(tc.yaml))
			c.Lore("render").Fails(t, 20, "[E_CONFIG_INVALID]", tc.say, "lore config check")
			for _, f := range []string{"X.md", ".lore/X.md", "N.md", "CLAUDE.md"} {
				if _, ok := c.ReadFile(f); ok {
					t.Errorf("a refused config writes no %s", f)
				}
			}
		})
	}
}
