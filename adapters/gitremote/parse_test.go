package gitremote

import "testing"

// Samples captured from git 2.53 (`git push --porcelain`, LC_ALL=C).
func TestParsePorcelainSamples(t *testing.T) {
	const out = "To ../o.git\n" +
		"*\t1dea5f564eb3623c15f8369c75fa3b334a0bea54:refs/lore/new\t[new reference]\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/heads/main\t[rejected] (stale info)\n" +
		"!\t4bfa3fdd00517c5b9c437243d0ac23faf7700cf3:refs/lore/live\t[rejected] (non-fast-forward)\n" +
		"!\t4bfa3fdd00517c5b9c437243d0ac23faf7700cf3:refs/lore/fetchfirst\t[rejected] (fetch first)\n" +
		"!\t1dea5f564eb3623c15f8369c75fa3b334a0bea54:refs/lore/other\t[rejected] (atomic push failed)\n" +
		"+\t4bfa3fdd00517c5b9c437243d0ac23faf7700cf3:refs/lore/forced\tf140cf3...4bfa3fd (forced update)\n" +
		"-\t:refs/lore/gone\t[deleted]\n" +
		"=\t4bfa3fdd00517c5b9c437243d0ac23faf7700cf3:refs/lore/same\t[up to date]\n" +
		" \t4bfa3fdd00517c5b9c437243d0ac23faf7700cf3:refs/lore/ff\tf140cf3..4bfa3fd\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/heads/prot\t[remote rejected] (pre-receive hook declined)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/heads/gl\t[remote rejected] (protected branch hook declined)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/heads/hook\t[remote rejected] (pre-receive hook declined)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/raceL\t[remote rejected] (incorrect old value provided)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/raceF\t[remote rejected] (cannot lock ref 'refs/lore/raceF': is at a but expected b)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/createL\t[remote rejected] (reference already exists)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/createF\t[remote rejected] (reference already exists)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/txL\t[remote rejected] (atomic transaction failed)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/txF\t[remote rejected] (atomic transaction failed)\n" +
		"!\tf140cf35e19d9e8cd2509da245b65c9b8c7f380e:refs/lore/sibling\t[remote rejected] (atomic push failure)\n" +
		"Done\n"
	const stderr = "remote: error: GH006: Protected branch update failed for refs/heads/prot.        \n" +
		"error: failed to push some refs to '../o.git'"
	modes := map[string]UpdateMode{"refs/lore/raceL": ModeLease, "refs/lore/raceF": ModeFastForward, "refs/lore/createL": ModeLease, "refs/lore/createF": ModeFastForward, "refs/lore/txL": ModeLease, "refs/lore/txF": ModeFastForward}
	want := map[string]Status{
		"refs/lore/new":        StatusOK,
		"refs/heads/main":      StatusStale,
		"refs/lore/live":       StatusNonFastForward,
		"refs/lore/fetchfirst": StatusNonFastForward,
		"refs/lore/other":      StatusAtomicFailed,
		"refs/lore/forced":     StatusOK,
		"refs/lore/gone":       StatusOK,
		"refs/lore/same":       StatusUpToDate,
		"refs/lore/ff":         StatusOK,
		"refs/heads/prot":      StatusProtected,
		"refs/heads/gl":        StatusProtected,
		"refs/heads/hook":      StatusRemoteRejected,
		"refs/lore/raceL":      StatusStale,
		"refs/lore/raceF":      StatusNonFastForward,
		// two pushes creating the same absent ref: the loser lost a race (seen by signals' 3-clone create race)
		"refs/lore/createL": StatusStale,
		"refs/lore/createF": StatusNonFastForward,
		// an atomic push whose server-side ref transaction lost the locks to another push (signals' 4-agent race)
		"refs/lore/txL": StatusStale,
		"refs/lore/txF": StatusNonFastForward,
		// receive-pack's reason for a fine ref when another ref of the atomic push was refused: nothing applied
		"refs/lore/sibling": StatusAtomicFailed,
	}
	got := parsePorcelain(out, stderr, modes)
	if len(got) != len(want) {
		t.Fatalf("parsed %d refs, want %d: %v", len(got), len(want), got)
	}
	for dst, s := range want {
		if got[dst].Status != s {
			t.Errorf("%s = %v want %v (%q)", dst, got[dst].Status, s, got[dst].Message)
		}
	}
	if m := got["refs/heads/prot"].Message; m != "[remote rejected] (pre-receive hook declined); remote: error: GH006: Protected branch update failed for refs/heads/prot." {
		t.Errorf("protection message = %q", m)
	}
}

func TestStatusNames(t *testing.T) {
	for s := StatusOK; s <= StatusAtomicFailed; s++ {
		if n := s.String(); n == "" || n[0] == 's' && n[:6] == "status" {
			t.Errorf("status %d has no name", int(s))
		}
	}
	if Status(99).String() != "status(99)" {
		t.Error("unknown status name")
	}
}

// TestClassifyRejectionWordings pins every "[remote rejected]" reason git
// versions print for a ref-lock loss (a lost race: retry) against the reasons
// that are genuine refusals (never retried), in both update modes. The
// "failed to update ref" row with its stderr is verbatim from Apple Git 2.50.1
// with 12 clones pushing one bare remote at once.
func TestClassifyRejectionWordings(t *testing.T) {
	const dst = "refs/heads/main"
	const lockStderr = "remote: error: cannot lock ref 'refs/heads/main': is at 1111111111111111111111111111111111111111 but expected 2222222222222222222222222222222222222222\n" +
		"error: failed to push some refs to '../o.git'"
	const gh006Stderr = "remote: error: GH006: Protected branch update failed for refs/heads/main.\n" +
		"error: failed to push some refs to 'github.com:o/r.git'"
	cases := []struct {
		name, reason, stderr string
		lease, ff            Status
	}{
		// lost races
		{"apple git lock loss", "failed to update ref", lockStderr, StatusStale, StatusNonFastForward},
		{"cannot lock ref", "cannot lock ref 'refs/heads/main': is at a but expected b", "", StatusStale, StatusNonFastForward},
		{"unable to update local ref", "unable to update local ref", "", StatusStale, StatusNonFastForward},
		{"incorrect old value", "incorrect old value provided", "", StatusStale, StatusNonFastForward},
		{"failed to lock", "failed to lock", "", StatusStale, StatusNonFastForward},
		{"create race", "reference already exists", "", StatusStale, StatusNonFastForward},
		{"atomic tx lock loss", "atomic transaction failed", "", StatusStale, StatusNonFastForward},
		// genuine refusals
		{"pre-receive hook", "pre-receive hook declined", "", StatusRemoteRejected, StatusRemoteRejected},
		{"update hook", "hook declined", "", StatusRemoteRejected, StatusRemoteRejected},
		{"permission denied", "permission denied", "", StatusRemoteRejected, StatusRemoteRejected},
		{"deny current branch", "branch is currently checked out", "", StatusRemoteRejected, StatusRemoteRejected},
		{"gh006 protected", "protected branch hook declined", gh006Stderr, StatusProtected, StatusProtected},
		{"gh006 generic reason", "pre-receive hook declined", gh006Stderr, StatusProtected, StatusProtected},
		{"atomic sibling", "atomic push failure", "", StatusAtomicFailed, StatusAtomicFailed},
	}
	for _, c := range cases {
		summary := summaryRemoteRej + " (" + c.reason + ")"
		for mode, want := range map[UpdateMode]Status{ModeLease: c.lease, ModeFastForward: c.ff} {
			if got := classifyRejection(summary, c.stderr, dst, mode); got != want {
				t.Errorf("%s (mode %v): got %v, want %v", c.name, mode, got, want)
			}
		}
	}
}
