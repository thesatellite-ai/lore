package wire

import (
	"strings"
	"testing"
)

// TestTeamVersionsNameTheWriter (CLI_REFERENCE 3.7): on cmd/lore the Team
// versions notes name the writer by its actors row (handle, else display
// name), never the raw act_ id, and the save by its date; a save with no
// recorded writer is named as such, not left blank.
func TestTeamVersionsNameTheWriter(t *testing.T) {
	repo := gitRepo(t)
	home := t.TempDir()
	a := openWith(t, repo, Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home}})
	withProject(t, a)
	histAddMemory(t, a, "", "stamped")
	got, err := a.DoctorTeamVersions(background())
	if err != nil || len(got) < 2 {
		t.Fatalf("team versions: %+v %v", got, err)
	}
	for _, f := range got[1:] {
		line := f.Message
		if strings.Contains(line, "act_") {
			t.Errorf("raw actor id in %q", line)
		}
		if strings.HasPrefix(line, "   on ") {
			t.Errorf("blank writer in %q", line)
		}
		_, date, ok := strings.Cut(line, "(last save ")
		if !ok || len(strings.TrimSuffix(date, ")")) != len("2006-01-02") {
			t.Errorf("save time is not a date in %q", line)
		}
	}
	if saveDate("2026-11-01T23:59:59.123456789Z") != "2026-11-01" || saveDate("not a time") != "not a time" {
		t.Error("saveDate")
	}
}
