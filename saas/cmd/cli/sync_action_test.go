package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowLoreVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ flag, own, want string }{
		{"", "0.1.10", "v0.1.10"},
		{"", "v0.2.0", "v0.2.0"},
		{"", "0.1.0-dev", loreVersionLatest},
		{"", "v0.1.9-4-ga85220b-dirty", loreVersionLatest},
		{"", "0.0.0-snapshot-abc1234", loreVersionLatest},
		{"latest", "0.1.10", loreVersionLatest},
		{"0.1.8", "0.1.10", "v0.1.8"},
		{"v0.1.8", "0.1.10", "v0.1.8"},
	} {
		got, err := workflowLoreVersion(tc.flag, tc.own)
		if err != nil || got != tc.want {
			t.Fatalf("flag %q own %q: %q %v, want %q", tc.flag, tc.own, got, err, tc.want)
		}
	}
	for _, bad := range []string{"1.2", "v1.2.3-rc1", "main", "v1.2.3; curl evil"} {
		if _, err := workflowLoreVersion(bad, "0.1.10"); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

func TestReleaseAssetURL(t *testing.T) {
	t.Parallel()
	if got := releaseAssetURL("v0.1.10"); got != "https://github.com/"+loreReleaseRepo+"/releases/download/v0.1.10" {
		t.Fatal(got)
	}
	if got := releaseAssetURL(loreVersionLatest); got != "https://github.com/"+loreReleaseRepo+"/releases/latest/download" {
		t.Fatal(got)
	}
}

// workflowDoc is the slice of a GitHub workflow the tests inspect.
type workflowDoc struct {
	On   map[string]yaml.Node `yaml:"on"`
	Jobs map[string]struct {
		Steps []struct {
			Name             string            `yaml:"name"`
			Uses             string            `yaml:"uses"`
			Run              string            `yaml:"run"`
			WorkingDirectory string            `yaml:"working-directory"`
			Env              map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// triggerFilter is a push / pull_request trigger's filters.
type triggerFilter struct {
	Branches []string `yaml:"branches"`
	Paths    []string `yaml:"paths"`
}

func parseWorkflow(t *testing.T, content string) workflowDoc {
	t.Helper()
	var doc workflowDoc
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("generated workflow is not valid YAML: %v\n%s", err, content)
	}
	return doc
}

func mergeStep(t *testing.T, doc workflowDoc) (run, workDir string) {
	t.Helper()
	for _, s := range doc.Jobs["merge"].Steps {
		if strings.Contains(s.Run, ciMergeCommand) {
			return s.Run, s.WorkingDirectory
		}
	}
	t.Fatal("no step runs lore sync ci-merge")
	return "", ""
}

func TestRenderSyncAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitInitRepo(t)
	auto, err := syncActionParamsFor(ctx, dir, dir, syncActionOptions{branches: []string{"main", "staging"}, loreVersion: "v0.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := renderSyncAction(auto)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(content, syncActionMarker) {
		t.Fatal("generated file must start with the marker")
	}
	doc := parseWorkflow(t, content)
	for _, trig := range []string{"pull_request", "push", "workflow_dispatch"} {
		if _, ok := doc.On[trig]; !ok {
			t.Fatalf("missing trigger %s", trig)
		}
	}
	var push triggerFilter
	pushNode := doc.On["push"]
	if err := pushNode.Decode(&push); err != nil {
		t.Fatal(err)
	}
	if strings.Join(push.Branches, ",") != "main,staging" || strings.Join(push.Paths, ",") != ".lore/data/**" {
		t.Fatalf("push filter = %+v", push)
	}
	run, workDir := mergeStep(t, doc)
	if workDir != repoRootRel {
		t.Fatalf("working-directory = %q", workDir)
	}
	// The job branches on the exact outcomes ci-merge prints.
	for _, outcome := range []ciMergeOutcome{ciMergeMerged, ciMergeNeedsHuman} {
		if !strings.Contains(run, string(outcome)+")") {
			t.Fatalf("merge step does not handle outcome %q", outcome)
		}
	}
	if !strings.Contains(content, releaseAssetURL("v0.1.10")+"/checksums.txt") || !strings.Contains(content, "sha256sum -c") {
		t.Fatal("download must be pinned and checksum-verified")
	}

	manual := auto
	manual.Manual = true
	content, err = renderSyncAction(manual)
	if err != nil {
		t.Fatal(err)
	}
	doc = parseWorkflow(t, content)
	if len(doc.On) != 1 {
		t.Fatalf("--manual must leave only workflow_dispatch: %v", doc.On)
	}
	if _, ok := doc.On["workflow_dispatch"]; !ok {
		t.Fatal("--manual lost workflow_dispatch")
	}
}

// A lore project in a subfolder: paths filter and working directory follow.
func TestRenderSyncActionSubdirProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitInitRepo(t)
	sub := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := syncActionParamsFor(ctx, dir, sub, syncActionOptions{branches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	content, err := renderSyncAction(p)
	if err != nil {
		t.Fatal(err)
	}
	doc := parseWorkflow(t, content)
	var pr triggerFilter
	prNode := doc.On["pull_request"]
	if err := prNode.Decode(&pr); err != nil {
		t.Fatal(err)
	}
	if strings.Join(pr.Paths, ",") != "services/api/.lore/data/**" {
		t.Fatalf("paths = %v", pr.Paths)
	}
	if _, wd := mergeStep(t, doc); wd != "services/api" {
		t.Fatalf("working-directory = %q", wd)
	}
}

func TestSyncActionRefusesUnsafeBranch(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	for _, b := range []string{"main\"]\non: x", "a b", "$(id)"} {
		if _, err := syncActionParamsFor(context.Background(), dir, dir, syncActionOptions{branches: []string{b}}); err == nil {
			t.Fatalf("branch %q must be refused", b)
		}
	}
}

// Every install/uninstall state, in one repository.
func TestInstallUninstallSyncAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitInitRepo(t)
	file := filepath.Join(dir, filepath.FromSlash(syncActionFileRel("")))
	o := syncActionOptions{branches: []string{"main"}, loreVersion: "v0.1.10"}

	if res, err := uninstallSyncAction(ctx, dir, false); err != nil || res.Status != syncActionAbsent {
		t.Fatalf("uninstall before install: %+v %v", res, err)
	}
	dry := o
	dry.dryRun = true
	if res, err := installSyncAction(ctx, dir, dry); err != nil || res.Status != syncActionDryRun || !strings.HasPrefix(res.Content, syncActionMarker) {
		t.Fatalf("dry run: %+v %v", res.Status, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("--dry-run wrote the file")
	}
	for _, want := range []syncActionStatus{syncActionCreated, syncActionUnchanged} {
		if res, err := installSyncAction(ctx, dir, o); err != nil || res.Status != want {
			t.Fatalf("want %s: %+v %v", want, res, err)
		}
	}
	upgraded := o
	upgraded.loreVersion = "v0.1.11"
	if res, err := installSyncAction(ctx, dir, upgraded); err != nil || res.Status != syncActionUpdated {
		t.Fatalf("re-install after upgrade: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(file); !strings.Contains(string(got), "v0.1.11") {
		t.Fatal("update did not rewrite the version")
	}

	// A workflow someone else wrote at that path is never overwritten or
	// deleted without --force.
	if err := os.WriteFile(file, []byte("name: theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installSyncAction(ctx, dir, o); err == nil {
		t.Fatal("overwrote a foreign file")
	}
	if _, err := uninstallSyncAction(ctx, dir, false); err == nil {
		t.Fatal("deleted a foreign file")
	}
	if got, _ := os.ReadFile(file); string(got) != "name: theirs\n" {
		t.Fatal("foreign file changed")
	}
	forced := o
	forced.force = true
	if res, err := installSyncAction(ctx, dir, forced); err != nil || res.Status != syncActionUpdated {
		t.Fatalf("--force: %+v %v", res, err)
	}
	if res, err := uninstallSyncAction(ctx, dir, false); err != nil || res.Status != syncActionRemoved {
		t.Fatalf("uninstall: %+v %v", res, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}
}

func TestSyncActionNeedsGit(t *testing.T) {
	t.Parallel()
	if _, err := installSyncAction(context.Background(), t.TempDir(), syncActionOptions{}); err == nil {
		t.Fatal("install-action outside a git repo must fail")
	}
}

func TestIsLoreManagedPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		prefix, path string
		want         bool
	}{
		{"", ".lore/data/memories/mem_1.json", true},
		{"", ".lore/data/_meta.json", true},
		{"", ".lore/LORE.md", true},
		{"", ".lore/lore.db", false},
		{"", "README.md", false},
		{"", ".lore/data", false},
		{"svc/", "svc/.lore/data/rules/r.json", true},
		{"svc/", ".lore/data/rules/r.json", false},
		{"svc/", "other/.lore/data/rules/r.json", false},
	} {
		if got := isLoreManagedPath(tc.prefix, tc.path); got != tc.want {
			t.Fatalf("%q %q = %v", tc.prefix, tc.path, got)
		}
	}
}

// gitInitRepo makes an empty git repository and returns its resolved path.
func gitInitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}
