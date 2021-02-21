package endtoend_test

import (
	"bufio"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kopia/kopia/internal/clock"
	"github.com/kopia/kopia/internal/testutil"
	"github.com/kopia/kopia/tests/testenv"
)

func TestSnapshotActionsBeforeSnapshotRoot(t *testing.T) {
	t.Parallel()

	th := os.Getenv("TESTING_ACTION_EXE")
	if th == "" {
		t.Skip("TESTING_ACTION_EXE verifyNoError be set")
	}

	e := testenv.NewCLITest(t)

	defer e.RunAndExpectSuccess(t, "repo", "disconnect")

	e.RunAndExpectSuccess(t, "repo", "create", "filesystem", "--path", e.RepoDir, "--override-hostname=foo", "--override-username=foo", "--enable-actions")
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir2)

	envFile1 := filepath.Join(e.LogsDir, "env1.txt")

	// set a action before-snapshot-root that fails and which saves the environment to a file.
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --exit-code=3 --save-env="+envFile1)

	// this prevents the snapshot from being created
	e.RunAndExpectFailure(t, "snapshot", "create", sharedTestDataDir1)

	envFile2 := filepath.Join(e.LogsDir, "env2.txt")

	// now set a action before-snapshot-root that succeeds and saves environment to a different file
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --save-env="+envFile2)

	// snapshot now succeeds.
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	env1 := mustReadEnvFile(t, envFile1)
	env2 := mustReadEnvFile(t, envFile2)

	// make sure snapshot IDs are different between two attempts
	if id1, id2 := env1["KOPIA_SNAPSHOT_ID"], env2["KOPIA_SNAPSHOT_ID"]; id1 == id2 {
		t.Errorf("KOPIA_SNAPSHOT_ID passed to action was not different between runs %v", id1)
	}

	// Now set up the action again, in optional mode,
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --exit-code=3",
		"--action-command-mode=optional")

	// this will not prevent snapshot creation.
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	// Now set up the action again, in async mode and pass --sleep so that the command takes some time.
	// because the action is async it will not wait for the command.
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --exit-code=3 --sleep=30s",
		"--action-command-mode=async")

	t0 := clock.Now()

	// at this point the data is all cached so this will be quick, definitely less than 30s,
	// async action failure will not prevent snapshot success.
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	if dur := clock.Since(t0); dur > 30*time.Second {
		t.Errorf("command did not execute asynchronously (took %v)", dur)
	}

	// Now set up essential action with a timeout of 3s and have the action sleep for 30s
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --sleep=30s",
		"--action-command-timeout=3s")

	t0 = clock.Now()

	// the action will be killed after 3s and cause a failure.
	e.RunAndExpectFailure(t, "snapshot", "create", sharedTestDataDir1)

	if dur := clock.Since(t0); dur > 30*time.Second {
		t.Errorf("command did not apply timeout (took %v)", dur)
	}

	// Now set up essential action that will cause redirection to an alternative folder which does not exist.
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --stdout-file="+tmpfileWithContents(t, "KOPIA_SNAPSHOT_PATH=/no/such/directory\n"))

	e.RunAndExpectFailure(t, "snapshot", "create", sharedTestDataDir1)

	// Now set up essential action that will cause redirection to an alternative folder which does exist.
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --stdout-file="+tmpfileWithContents(t, "KOPIA_SNAPSHOT_PATH="+sharedTestDataDir2+"\n"))

	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	// since we redirected to sharedTestDataDir2 the object ID of last snapshot of sharedTestDataDir1
	// will be the same as snapshots of sharedTestDataDir2
	snaps1 := e.ListSnapshotsAndExpectSuccess(t, sharedTestDataDir1)[0].Snapshots
	snaps2 := e.ListSnapshotsAndExpectSuccess(t, sharedTestDataDir2)[0].Snapshots

	if snaps1[0].ObjectID == snaps2[0].ObjectID {
		t.Fatal("failed sanity check - snapshots are the same")
	}

	if got, want := snaps1[len(snaps1)-1].ObjectID, snaps2[0].ObjectID; got != want {
		t.Fatalf("invalid snapshot ID after redirection %v, wanted %v", got, want)
	}

	// not setup the same redirection but in async mode - will be ignored because Kopia does not wait for asynchronous
	// actions at all or parse their output.
	e.RunAndExpectSuccess(t,
		"policy", "set", sharedTestDataDir1,
		"--before-snapshot-root-action",
		th+" --stdout-file="+tmpfileWithContents(t, "KOPIA_SNAPSHOT_PATH="+sharedTestDataDir2+"\n"),
		"--action-command-mode=async")
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	// verify redirection had no effect - last snapshot will be the same as the first one
	snaps1 = e.ListSnapshotsAndExpectSuccess(t, sharedTestDataDir1)[0].Snapshots
	if got, want := snaps1[len(snaps1)-1].ObjectID, snaps1[0].ObjectID; got != want {
		t.Fatalf("invalid snapshot ID after async action %v, wanted %v", got, want)
	}
}

func TestSnapshotActionsBeforeAfterFolder(t *testing.T) {
	t.Parallel()

	th := os.Getenv("TESTING_ACTION_EXE")
	if th == "" {
		t.Skip("TESTING_ACTION_EXE verifyNoError be set")
	}

	e := testenv.NewCLITest(t)

	e.RunAndExpectSuccess(t, "repo", "create", "filesystem", "--path", e.RepoDir, "--enable-actions")
	defer e.RunAndExpectSuccess(t, "repo", "disconnect")

	// create directory structure
	rootDir := testutil.TempDirectory(t)
	sd1 := filepath.Join(rootDir, "subdir1")
	sd2 := filepath.Join(rootDir, "subdir2")
	sd11 := filepath.Join(rootDir, "subdir1", "subdir1")
	sd12 := filepath.Join(rootDir, "subdir1", "subdir2")

	verifyNoError(t, os.Mkdir(sd1, 0700))
	verifyNoError(t, os.Mkdir(sd2, 0700))
	verifyNoError(t, os.Mkdir(sd11, 0700))
	verifyNoError(t, os.Mkdir(sd12, 0700))

	actionRanDir := testutil.TempDirectory(t)

	actionRanFileBeforeRoot := filepath.Join(actionRanDir, "before-root")
	actionRanFileAfterRoot := filepath.Join(actionRanDir, "before-root")
	actionRanFileBeforeSD1 := filepath.Join(actionRanDir, "before-sd1")
	actionRanFileAfterSD1 := filepath.Join(actionRanDir, "before-sd1")
	actionRanFileBeforeSD11 := filepath.Join(actionRanDir, "before-sd11")
	actionRanFileAfterSD11 := filepath.Join(actionRanDir, "before-sd11")
	actionRanFileBeforeSD2 := filepath.Join(actionRanDir, "before-sd2")
	actionRanFileAfterSD2 := filepath.Join(actionRanDir, "before-sd2")

	// setup actions that will write a marker file when the action is executed.
	//
	// We are not setting a policy on 'sd12' to ensure it's not inherited
	// from sd1. If it was inherited, the action would fail since it refuses to create the
	// file if one already exists.
	e.RunAndExpectSuccess(t, "policy", "set", rootDir,
		"--before-folder-action", th+" --create-file="+actionRanFileBeforeRoot)
	e.RunAndExpectSuccess(t, "policy", "set", rootDir,
		"--after-folder-action", th+" --create-file="+actionRanFileAfterRoot)
	e.RunAndExpectSuccess(t, "policy", "set", sd1,
		"--before-folder-action", th+" --create-file="+actionRanFileBeforeSD1)
	e.RunAndExpectSuccess(t, "policy", "set", sd1,
		"--after-folder-action", th+" --create-file="+actionRanFileAfterSD1)
	e.RunAndExpectSuccess(t, "policy", "set", sd2,
		"--before-folder-action", th+" --create-file="+actionRanFileBeforeSD2)
	e.RunAndExpectSuccess(t, "policy", "set", sd2,
		"--after-folder-action", th+" --create-file="+actionRanFileAfterSD2)
	e.RunAndExpectSuccess(t, "policy", "set", sd11,
		"--before-folder-action", th+" --create-file="+actionRanFileBeforeSD11)
	e.RunAndExpectSuccess(t, "policy", "set", sd11,
		"--after-folder-action", th+" --create-file="+actionRanFileAfterSD11)

	e.RunAndExpectSuccess(t, "snapshot", "create", rootDir)

	verifyFileExists(t, actionRanFileBeforeRoot)
	verifyFileExists(t, actionRanFileAfterRoot)
	verifyFileExists(t, actionRanFileBeforeSD1)
	verifyFileExists(t, actionRanFileBeforeSD11)
	verifyFileExists(t, actionRanFileAfterSD11)
	verifyFileExists(t, actionRanFileAfterSD1)
	verifyFileExists(t, actionRanFileBeforeSD2)
	verifyFileExists(t, actionRanFileAfterSD2)

	// the action will fail to run the next time since all 'actionRan*' files already exist.
	e.RunAndExpectFailure(t, "snapshot", "create", rootDir)
}

func TestSnapshotActionsEmbeddedScript(t *testing.T) {
	t.Parallel()

	e := testenv.NewCLITest(t)

	e.RunAndExpectSuccess(t, "repo", "create", "filesystem", "--path", e.RepoDir, "--enable-actions")
	defer e.RunAndExpectSuccess(t, "repo", "disconnect")

	var (
		successScript      = tmpfileWithContents(t, "echo Hello world!")
		successScript2     string
		failingScript      string
		goodRedirectScript = tmpfileWithContents(t, "echo KOPIA_SNAPSHOT_PATH="+sharedTestDataDir2)
		badRedirectScript  = tmpfileWithContents(t, "echo KOPIA_SNAPSHOT_PATH=/no/such/directory")
	)

	if runtime.GOOS == "windows" {
		failingScript = tmpfileWithContents(t, "exit /b 1")
		successScript2 = tmpfileWithContents(t, "echo Hello world!")
	} else {
		failingScript = tmpfileWithContents(t, "#!/bin/sh\nexit 1")
		successScript2 = tmpfileWithContents(t, "#!/bin/sh\necho Hello world!")
	}

	e.RunAndExpectSuccess(t, "policy", "set", sharedTestDataDir1, "--before-folder-action", successScript, "--persist-action-script")
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	e.RunAndExpectSuccess(t, "policy", "set", sharedTestDataDir1, "--before-folder-action", goodRedirectScript, "--persist-action-script")
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	e.RunAndExpectSuccess(t, "policy", "set", sharedTestDataDir1, "--before-folder-action", successScript2, "--persist-action-script")
	e.RunAndExpectSuccess(t, "snapshot", "create", sharedTestDataDir1)

	snaps1 := e.ListSnapshotsAndExpectSuccess(t, sharedTestDataDir1)[0].Snapshots
	if snaps1[0].ObjectID == snaps1[1].ObjectID {
		t.Fatalf("redirection did not happen!")
	}

	e.RunAndExpectSuccess(t, "policy", "set", sharedTestDataDir1, "--before-folder-action", badRedirectScript, "--persist-action-script")
	e.RunAndExpectFailure(t, "snapshot", "create", sharedTestDataDir1)

	e.RunAndExpectSuccess(t, "policy", "set", sharedTestDataDir1, "--before-folder-action", failingScript, "--persist-action-script")
	e.RunAndExpectFailure(t, "snapshot", "create", sharedTestDataDir1)
}

func TestSnapshotActionsEnable(t *testing.T) {
	t.Parallel()

	th := os.Getenv("TESTING_ACTION_EXE")
	if th == "" {
		t.Skip("TESTING_ACTION_EXE verifyNoError be set")
	}

	cases := []struct {
		desc          string
		connectFlags  []string
		snapshotFlags []string
		wantRun       bool
	}{
		{desc: "defaults", connectFlags: nil, snapshotFlags: nil, wantRun: false},
		{desc: "override-connect-disable", connectFlags: []string{"--enable-actions"}, snapshotFlags: nil, wantRun: true},
		{desc: "override-connect-disable", connectFlags: []string{"--no-enable-actions"}, snapshotFlags: nil, wantRun: false},
		{desc: "override-snapshot-enable", connectFlags: nil, snapshotFlags: []string{"--force-enable-actions"}, wantRun: true},
		{desc: "override-snapshot-disable", connectFlags: nil, snapshotFlags: []string{"--force-disable-actions"}, wantRun: false},
		{desc: "snapshot-takes-precedence-enable", connectFlags: []string{"--no-enable-actions"}, snapshotFlags: []string{"--force-enable-actions"}, wantRun: true},
		{desc: "snapshot-takes-precedence-disable", connectFlags: []string{"--enable-actions"}, snapshotFlags: []string{"--force-disable-actions"}, wantRun: false},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			e := testenv.NewCLITest(t)

			defer e.RunAndExpectSuccess(t, "repo", "disconnect")

			e.RunAndExpectSuccess(t, append([]string{"repo", "create", "filesystem", "--path", e.RepoDir}, tc.connectFlags...)...)

			envFile := filepath.Join(e.LogsDir, "env1.txt")

			// set an action before-snapshot-root that fails and which saves the environment to a file.
			e.RunAndExpectSuccess(t,
				"policy", "set",
				sharedTestDataDir1,
				"--before-snapshot-root-action",
				th+" --save-env="+envFile)

			e.RunAndExpectSuccess(t, append([]string{"snapshot", "create", sharedTestDataDir1}, tc.snapshotFlags...)...)

			_, err := os.Stat(envFile)
			didRun := err == nil
			if didRun != tc.wantRun {
				t.Errorf("unexpected behavior. did run: %v want run: %v", didRun, tc.wantRun)
			}
		})
	}
}

func tmpfileWithContents(t *testing.T, contents string) string {
	t.Helper()

	f, err := ioutil.TempFile("", "kopia-test")
	verifyNoError(t, err)

	f.WriteString(contents)
	f.Close()

	t.Cleanup(func() { os.Remove(f.Name()) })

	return f.Name()
}

func verifyFileExists(t *testing.T, fname string) {
	t.Helper()

	_, err := os.Stat(fname)
	if err != nil {
		t.Fatal(err)
	}
}

func verifyNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func mustReadEnvFile(t *testing.T, fname string) map[string]string {
	t.Helper()

	f, err := os.Open(fname)

	verifyNoError(t, err)

	defer f.Close()
	s := bufio.NewScanner(f)

	m := map[string]string{}

	for s.Scan() {
		parts := strings.SplitN(s.Text(), "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}

	verifyNoError(t, s.Err())

	return m
}
