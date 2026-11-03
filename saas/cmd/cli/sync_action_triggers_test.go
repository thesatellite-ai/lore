package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// syncActionTriggerNames lists the workflow's triggers in sorted order, so
// a test can compare them as one string.
func syncActionTriggerNames(t *testing.T, file string) string {
	t.Helper()
	doc := parseWorkflow(t, readFile(t, file))
	var names []string
	for k := range doc.On {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// Trigger names as syncActionTriggerNames prints them.
const (
	triggersManualOnly = "workflow_dispatch"
	triggersAutomatic  = "pull_request,push,workflow_dispatch"
)

// What a new install writes for each flag combination. Automatic runs spend
// Actions minutes, so only --auto may add them.
func TestInstallSyncActionTriggerFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		o          syncActionOptions
		want       string
		wantManual bool
	}{
		{"default is manual only", syncActionOptions{}, triggersManualOnly, true},
		{"--auto adds pull_request and push", syncActionOptions{auto: true}, triggersAutomatic, false},
		{"--manual still accepted", syncActionOptions{manualSet: true}, triggersManualOnly, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := gitInitRepo(t)
			o := tc.o
			o.loreVersion, o.branches = "v0.1.11", []string{"main"}
			res, err := installSyncAction(context.Background(), dir, o)
			if err != nil {
				t.Fatal(err)
			}
			if got := syncActionTriggerNames(t, res.Path); got != tc.want {
				t.Fatalf("triggers = %s, want %s", got, tc.want)
			}
			if res.Manual != tc.wantManual || res.Auto == tc.wantManual {
				t.Fatalf("result manual=%v auto=%v, want manual=%v", res.Manual, res.Auto, tc.wantManual)
			}
			b, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["manual"] != tc.wantManual || fields["auto"] != !tc.wantManual {
				t.Fatalf("JSON manual/auto = %v/%v", fields["manual"], fields["auto"])
			}
		})
	}
}

func TestInstallSyncActionRefusesAutoAndManual(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	if _, err := installSyncAction(context.Background(), dir, syncActionOptions{auto: true, manualSet: true, loreVersion: "v0.1.11"}); err == nil {
		t.Fatal("--auto with --manual must be refused")
	}
}

// A plain re-run (no --auto / --manual / --branch) keeps an installed
// workflow's triggers and branches, so changing the default never stops an
// automatic workflow or starts a manual one.
func TestInstallSyncActionRerunKeepsTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	autoDir := gitInitRepo(t)
	file := installAt(t, autoDir, syncActionOptions{auto: true, branches: []string{"main", "staging"}})
	res, err := installSyncAction(ctx, autoDir, syncActionOptions{loreVersion: "v0.1.12"})
	if err != nil || res.Status != syncActionUpdated || res.Manual {
		t.Fatalf("re-run over an automatic workflow: %+v %v", res, err)
	}
	if got := syncActionTriggerNames(t, file); got != triggersAutomatic {
		t.Fatalf("plain re-run changed the triggers to %s", got)
	}
	if s, err := parseSyncAction(readFile(t, file)); err != nil || strings.Join(s.branches, ",") != "main,staging" {
		t.Fatalf("plain re-run lost the branches: %+v %v", s, err)
	}

	manualDir := gitInitRepo(t)
	file = installAt(t, manualDir, syncActionOptions{})
	if res, err := installSyncAction(ctx, manualDir, syncActionOptions{loreVersion: "v0.1.12"}); err != nil || !res.Manual {
		t.Fatalf("re-run over a manual workflow: %+v %v", res, err)
	}
	if got := syncActionTriggerNames(t, file); got != triggersManualOnly {
		t.Fatalf("plain re-run changed the triggers to %s", got)
	}

	// Explicit flags do switch an installed workflow, both ways.
	if _, err := installSyncAction(ctx, manualDir, syncActionOptions{auto: true, loreVersion: "v0.1.12"}); err != nil {
		t.Fatal(err)
	}
	if got := syncActionTriggerNames(t, file); got != triggersAutomatic {
		t.Fatalf("--auto left triggers %s", got)
	}
	if _, err := installSyncAction(ctx, manualDir, syncActionOptions{manualSet: true, loreVersion: "v0.1.12"}); err != nil {
		t.Fatal(err)
	}
	if got := syncActionTriggerNames(t, file); got != triggersManualOnly {
		t.Fatalf("--manual left triggers %s", got)
	}
}

// The pin refresh rewrites the file for a newer lore but keeps whatever
// triggers are installed: an automatic workflow stays automatic, a manual
// one stays manual, whatever install-action's default is.
func TestRefreshKeepsInstalledTriggers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		o    syncActionOptions
		want string
	}{
		{"automatic", syncActionOptions{auto: true}, triggersAutomatic},
		{"manual by default", syncActionOptions{}, triggersManualOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := gitInitRepo(t)
			file := installAt(t, dir, tc.o)
			if r := refresh(t, dir, "0.1.12"); r.To != "v0.1.12" {
				t.Fatalf("refresh = %+v", r)
			}
			if got := syncActionTriggerNames(t, file); got != tc.want {
				t.Fatalf("refresh changed the triggers to %s, want %s", got, tc.want)
			}
		})
	}
}

// A manual workflow's header tells how to turn the automatic triggers on,
// and the trigger lines it offers to paste are exactly what --auto writes.
func TestSyncActionManualHeaderOffersAutoTriggers(t *testing.T) {
	t.Parallel()
	dir := gitInitRepo(t)
	content := readFile(t, installAt(t, dir, syncActionOptions{branches: []string{"main", "staging"}}))
	if !strings.Contains(content, "lore sync install-action --auto") {
		t.Fatal("manual workflow does not mention --auto")
	}
	// Uncomment the offered lines and paste them under "on:".
	var pasted []string
	inBlock := false
	for _, l := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(l, "#   pull_request:"):
			inBlock = true
		case inBlock && !strings.HasPrefix(l, "#   "):
			inBlock = false
		}
		if inBlock {
			pasted = append(pasted, strings.TrimPrefix(l, "# "))
		}
	}
	if len(pasted) == 0 {
		t.Fatal("manual workflow offers no trigger lines to paste")
	}
	edited := strings.Replace(content, "\non:\n", "\non:\n"+strings.Join(pasted, "\n")+"\n", 1)
	doc := parseWorkflow(t, edited)
	var push triggerFilter
	node := doc.On["push"]
	if err := node.Decode(&push); err != nil {
		t.Fatal(err)
	}
	if strings.Join(push.Branches, ",") != "main,staging" || strings.Join(push.Paths, ",") != ".lore/data/**" {
		t.Fatalf("pasted push filter = %+v", push)
	}
	if _, ok := doc.On["pull_request"]; !ok {
		t.Fatal("pasted lines have no pull_request trigger")
	}
}
