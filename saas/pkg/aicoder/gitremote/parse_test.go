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
