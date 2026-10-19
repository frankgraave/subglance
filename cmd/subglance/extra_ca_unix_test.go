//go:build unix

package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/config"
)

// A CA file mounted owner-only from the host is refused with its owner and
// the fix, not with a bare "permission denied".
func TestExtraCAFileThatCannotBeReadIsNamed(t *testing.T) {
	skipAsRoot(t)
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("not read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chmod(t, path, 0o200)
	_, err := extraRoots(config.Config{ExtraCAFile: path}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("an unreadable CA file was accepted")
	}
	for _, want := range []string{"--extra-ca-file", path, "belongs to", "SubGlance runs as user", "chmod u+r " + path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q:\n%v", want, err)
		}
	}
}
