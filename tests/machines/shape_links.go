//go:build machines && !windows

package machines

// shape_links.go — machines whose files lore reads and writes are symbolic
// links or read-only, as real homes have them: a shared-skills tool moved
// ~/.claude/skills/lore into ~/.agents and linked it back, a dotfiles repo
// owns ~/.lore/config.yaml and ~/.claude/CLAUDE.md, a team keeps one
// CLAUDE.md outside the repos and links it in, ~/code is a link to another
// disk, and a person made files read-only so nothing rewrites them.
//
// Every link target outside lore's own folders lives under Machine.Dir's
// "foreign" folder, so a test can prove lore wrote nothing there.

import (
	"os"
	"path/filepath"
	"testing"
)

// Link shape names.
const (
	ShapeLinksSkillChain    = "links_skill_chain"
	ShapeLinksSkillDangling = "links_skill_dangling"
	ShapeLinksFiles         = "links_files"
	ShapeLinksDirHome       = "links_dir_home"
	ShapeLinksReadonly      = "links_readonly"
)

// LinksShapes lists the link shapes, in run order.
func LinksShapes() []string {
	return []string{ShapeLinksSkillChain, ShapeLinksSkillDangling, ShapeLinksFiles, ShapeLinksDirHome, ShapeLinksReadonly}
}

// Paths the link shapes make, relative to Home.
const (
	// LinkSkillClaude is the Claude Code skill folder (a link in the
	// skill shapes).
	LinkSkillClaude = ".claude/skills/lore"
	// LinkSkillAgents is the shared-skills tool's folder: a link to the
	// real bundle under ForeignDir.
	LinkSkillAgents = ".agents/skills/lore"
	// LinkGlobalClaude is the user's global agent rules file.
	LinkGlobalClaude = ".claude/CLAUDE.md"
	// LinkConfig is lore's machine config.
	LinkConfig = ".lore/config.yaml"
)

// Folders under Machine.Dir (outside Home) the link targets live in.
const (
	// ForeignDir holds every link target that is not lore's: lore must
	// leave it byte-identical.
	ForeignDir = "foreign"
	// DotfilesDir is the dotfiles repo (under ForeignDir) that owns
	// config.yaml and ~/.claude/CLAUDE.md in links_files.
	DotfilesDir = "foreign/dotfiles"
	// SharedRulesDir holds the team's CLAUDE.md / AGENTS.md linked into
	// repos in links_files.
	SharedRulesDir = "foreign/shared-rules"
	// SkillStoreDir is the shared-skills tool's real folder of skills.
	SkillStoreDir = "foreign/skill-store"
	// ElsewhereCode is the other disk ~/code links to in links_dir_home.
	ElsewhereCode = "elsewhere/code"
)

// Bodies of the foreign files (placeholders).
const (
	SharedClaudeBody = "# Shared team rules\n\nReview every change.\n"
	SharedAgentsBody = "# Shared agent rules\n\nWrite tests first.\n"
	GlobalClaudeBody = "# My global rules\n\nKeep replies short.\n"
)

// LinkSetup is the setup the link shapes' earlier `lore setup` made.
const LinkSetup = "personal"

// LinkSetupFolder is LinkSetup's folder glob.
const LinkSetupFolder = "~/code/**"

// readOnlyFile / readOnlyDir are the modes of links_readonly's files.
const (
	readOnlyFile os.FileMode = 0o444
	readOnlyDir  os.FileMode = 0o555
)

// RepoReadonly is links_readonly's read-only repo folder (under ~/code).
const RepoReadonly = "locked"

func init() {
	RegisterShape(ExtraShape{Name: ShapeLinksSkillChain, Build: buildLinksSkillChain})
	RegisterShape(ExtraShape{Name: ShapeLinksSkillDangling, Build: buildLinksSkillDangling})
	RegisterShape(ExtraShape{Name: ShapeLinksFiles, Build: buildLinksFiles})
	RegisterShape(ExtraShape{Name: ShapeLinksDirHome, Build: buildLinksDirHome})
	RegisterShape(ExtraShape{Name: ShapeLinksReadonly, Build: buildLinksReadonly})
}

// linkBase is ssh, gh and the solo setup `lore setup` made earlier, with
// the Claude Code skill installed (a real folder at this point).
func (b *builder) linkBase() {
	b.ssh(false)
	b.gh([]string{LoginAlpha}, LoginAlpha)
	b.lore(b.m.Home, "setup", "apply", LinkSetup, "--step", "kind", "--answers", `{"kind":"solo","name":"`+LinkSetup+`","default":true}`)
	b.lore(b.m.Home, "setup", "apply", LinkSetup, "--step", "identity", "--answers", `{"name":"`+PersonName+`","email":"`+PersonEmail+`"}`)
	b.lore(b.m.Home, "setup", "apply", LinkSetup, "--step", "tools", "--answers", `{"tools":["claude_code"]}`)
	b.lore(b.m.Home, "setup", "apply", LinkSetup, "--step", "folders", "--answers", `{"folders":["`+LinkSetupFolder+`"],"default":true}`)
	b.lore(b.m.Home, "skill", "install", "--tool", "claude_code")
}

// symlink makes link (under Home, or absolute) point at target, making
// link's folder.
func (b *builder) symlink(target, link string) {
	b.t.Helper()
	if !filepath.IsAbs(link) {
		link = filepath.Join(b.m.Home, link)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		b.t.Fatal(err)
	}
}

// foreign is the absolute path of rel under Machine.Dir.
func (m *Machine) foreign(rel string) string { return filepath.Join(m.Dir, rel) }

// moveAway moves Home-relative src to the absolute dst (a shared-skills
// tool or a dotfiles manager taking a file over).
func (b *builder) moveAway(src, dst string) {
	b.t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(b.m.Home, src), dst); err != nil {
		b.t.Fatal(err)
	}
}

// buildLinksSkillChain: the shared-skills tool moved the installed skill to
// its own store and linked it back in two hops:
// ~/.claude/skills/lore → ~/.agents/skills/lore → foreign/skill-store/lore.
func buildLinksSkillChain(b *builder) {
	b.linkBase()
	real := b.m.foreign(filepath.Join(SkillStoreDir, "lore"))
	b.moveAway(LinkSkillClaude, real)
	b.symlink(real, LinkSkillAgents)
	b.symlink(filepath.Join(b.m.Home, LinkSkillAgents), LinkSkillClaude)
}

// buildLinksSkillDangling: the shared-skills store was deleted, so
// ~/.claude/skills/lore points nowhere; the dotfiles checkout that held
// ~/.claude/CLAUDE.md and a repo's CLAUDE.md is gone too.
func buildLinksSkillDangling(b *builder) {
	b.linkBase()
	if err := os.RemoveAll(filepath.Join(b.m.Home, LinkSkillClaude)); err != nil {
		b.t.Fatal(err)
	}
	b.symlink(b.m.foreign(filepath.Join(SkillStoreDir, "lore")), LinkSkillClaude)
	b.symlink(b.m.foreign(filepath.Join(DotfilesDir, "claude", "CLAUDE.md")), LinkGlobalClaude)
	b.symlink(b.m.foreign(filepath.Join(SharedRulesDir, "CLAUDE.md")), filepath.Join(b.m.Repos[RepoPlain], "CLAUDE.md"))
	b.dir(b.m.foreign(ForeignDir), 0o755)
}

// buildLinksFiles: a dotfiles repo owns ~/.lore/config.yaml and
// ~/.claude/CLAUDE.md; has-claude's CLAUDE.md and has-agents' AGENTS.md
// are links to one shared file outside every repo.
func buildLinksFiles(b *builder) {
	b.linkBase()
	cfg := b.m.foreign(filepath.Join(DotfilesDir, "lore", "config.yaml"))
	b.moveAway(LinkConfig, cfg)
	b.symlink(cfg, LinkConfig)
	b.file(b.m.foreign(filepath.Join(DotfilesDir, "claude", "CLAUDE.md")), GlobalClaudeBody, 0o644)
	b.symlink(b.m.foreign(filepath.Join(DotfilesDir, "claude", "CLAUDE.md")), LinkGlobalClaude)
	b.file(b.m.foreign(filepath.Join(SharedRulesDir, "CLAUDE.md")), SharedClaudeBody, 0o644)
	b.file(b.m.foreign(filepath.Join(SharedRulesDir, "AGENTS.md")), SharedAgentsBody, 0o644)
	for repo, f := range map[string]string{RepoClaude: "CLAUDE.md", RepoAgents: "AGENTS.md"} {
		p := filepath.Join(b.m.Repos[repo], f)
		if err := os.Remove(p); err != nil {
			b.t.Fatal(err)
		}
		b.symlink(b.m.foreign(filepath.Join(SharedRulesDir, f)), p)
	}
}

// buildLinksDirHome: ~/code lives on another disk and is reached through a
// link (~/code → <dir>/elsewhere/code), so a repo's physical path is not
// under the home folder.
func buildLinksDirHome(b *builder) {
	away := b.m.foreign(ElsewhereCode)
	b.moveAway("code", away)
	b.symlink(away, "code")
	b.linkBase()
}

// buildLinksReadonly: has-claude's CLAUDE.md and ~/.lore/config.yaml are
// 0444, and ~/code/locked is a read-only git repo.
func buildLinksReadonly(b *builder) {
	b.linkBase()
	locked := filepath.Join(b.m.Home, "code", RepoReadonly)
	b.dir(locked, 0o755)
	b.run(locked, "/usr/bin/git", "init", "-q")
	b.m.Repos[RepoReadonly] = locked
	chmod(b.t, locked, readOnlyDir)
	chmod(b.t, filepath.Join(b.m.Repos[RepoClaude], "CLAUDE.md"), readOnlyFile)
	chmod(b.t, filepath.Join(b.m.Home, LinkConfig), readOnlyFile)
	// The temp dir must stay removable after the test.
	b.t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
}

func chmod(t testing.TB, p string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}
