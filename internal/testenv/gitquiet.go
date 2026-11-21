package testenv

// GitConfigQuiet is the git config block every test's global gitconfig
// starts with: it turns off git's automatic maintenance.
//
// Why (devco:D-875): since git 2.47 every commit, fetch, merge and push
// ends with `git maintenance run --auto --detach`, and the detached child
// is started even when there is nothing to do. It outlives the command and
// takes .git/objects/maintenance.lock in the test's repositories; on a
// loaded machine it ran after the test had returned, while t.TempDir's
// cleanup was removing the folder, and the test failed with "TempDir
// RemoveAll cleanup: … directory not empty" (wire
// TestDoctorAllReadsRosterAndPeriod in a land gate). A test repository
// never needs maintenance, so no git a test starts leaves a child behind.
const GitConfigQuiet = "[maintenance]\n\tauto = false\n"
