package datalock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSecondAcquireIsRefusedUntilRelease(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire: err = %v, want ErrLocked", err)
	}
	if !strings.Contains(err.Error(), Path(dir)) {
		t.Errorf("error %q does not name the lock file %s", err, Path(dir))
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	_ = again.Release()
}

func TestReleaseIsSafeTwice(t *testing.T) {
	l, err := Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

// helperEnv makes the test binary act as a lock holder in a child process.
const helperEnv = "SUBGLANCE_DATALOCK_HOLDER_DIR"

// TestMain lets this test binary double as the child process: with helperEnv
// set it takes the lock, says so, and waits for its stdin to close.
func TestMain(m *testing.M) {
	if dir := os.Getenv(helperEnv); dir != "" {
		if _, err := Acquire(dir); err != nil {
			_, _ = os.Stdout.WriteString("error: " + err.Error() + "\n")
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString("locked\n")
		// Exit without Release, as a crashed server would: only the
		// operating system can give the lock back now.
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A crashed server must not leave the data directory locked: the lock is the
// kernel's, so it goes away with the process. Checked across a real process
// boundary, since that is the only boundary a crash crosses.
func TestLockHeldByAnotherProcessIsReleasedWhenItExits(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if line != "locked\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("holder process said %q, want it to hold the lock", line)
	}

	if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		_ = cmd.Process.Kill()
		t.Fatalf("Acquire while another process holds the lock: err = %v, want ErrLocked", err)
	}

	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("holder process: %v", err)
	}

	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after the holder exited: %v", err)
	}
	_ = l.Release()
}
