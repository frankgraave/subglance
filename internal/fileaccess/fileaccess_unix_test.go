//go:build unix

package fileaccess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const setting = "--secret-key (SUBGLANCE_SECRET_KEY)"

// skipAsRoot skips a test that takes a permission away: root is refused
// nothing a mode bit says, so the refusal under test cannot happen. CI runs
// these as an ordinary user.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes; run as an ordinary user")
	}
}

func writeFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subglance.key")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	return path
}

// A file the process owns but may not read is refused with the setting, the
// path, its owner and mode, the user SubGlance runs as, and the chmod that
// fixes it: a chown to the same user would change nothing.
func TestRefusesAnUnreadableFileItOwns(t *testing.T) {
	skipAsRoot(t)
	path := writeFile(t, 0o200)
	err := RequireReadable(setting, path)
	if err == nil {
		t.Fatal("an unreadable file was accepted")
	}
	msg := err.Error()
	for _, want := range []string{setting, path, "belongs to", "--w-------", "SubGlance runs as user",
		"chmod u+r " + path} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "chown") {
		t.Errorf("refusal of the process's own file suggests a chown, which changes nothing:\n%s", msg)
	}
}

// A file that belongs to another user, the mounted secret in a container, is
// given to the user SubGlance runs as.
func TestFixForAnotherUsersFileIsChown(t *testing.T) {
	skipAsRoot(t)
	got := readFix("/")
	want := "chown " + ProcessOwner() + " /"
	if !strings.Contains(got, want) {
		t.Errorf("fix for a root-owned path = %q, want it to contain %q", got, want)
	}
}

// Everything that is not a file this process may not read passes, and is
// left to the read that follows.
func TestLeavesEverythingElseToTheRead(t *testing.T) {
	readable := writeFile(t, 0o600)
	dangling := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"empty":         "",
		"readable file": readable,
		"no such path":  filepath.Join(t.TempDir(), "missing.key"),
		"dangling link": dangling,
	} {
		if err := RequireReadable(setting, path); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

// A --secret-key value that names no file is key material, and is never
// repeated in an error: this check says nothing about it at all.
func TestKeyMaterialIsNeverRepeated(t *testing.T) {
	const key = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	if err := RequireReadable(setting, key); err != nil {
		t.Errorf("key material was refused, and the refusal repeats it: %v", err)
	}
}
