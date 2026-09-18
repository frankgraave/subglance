package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHexKey = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

// The default has to stay off, and that has to be a test rather than a
// convention: the whole honesty argument for this feature rests on the default
// being plain text and documented, and on nobody later making it implicit.
func TestSecretKeyDefaultsToOffMeaningPlaintext(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SecretKey != "" {
		t.Errorf("SecretKey = %q by default; encryption must be opt-in", cfg.SecretKey)
	}
	key, err := cfg.ResolveSecretKey()
	if err != nil {
		t.Fatalf("ResolveSecretKey: %v", err)
	}
	if key != nil {
		t.Errorf("ResolveSecretKey = %d bytes with no option set, want nil", len(key))
	}
}

func TestSecretKeyAcceptsFlagAndEnvironmentWithFlagWinning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subglance.key")
	if err := os.WriteFile(path, []byte(testHexKey+"\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	t.Setenv("SUBGLANCE_SECRET_KEY", testHexKey)
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load from environment: %v", err)
	}
	if cfg.SecretKey != testHexKey {
		t.Errorf("SecretKey = %q, want the environment value", cfg.SecretKey)
	}

	cfg, err = Load([]string{"--secret-key", path})
	if err != nil {
		t.Fatalf("Load with flag: %v", err)
	}
	if cfg.SecretKey != path {
		t.Errorf("SecretKey = %q, want the flag to beat the environment", cfg.SecretKey)
	}
	key, err := cfg.ResolveSecretKey()
	if err != nil {
		t.Fatalf("ResolveSecretKey: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("resolved key is %d bytes, want 32", len(key))
	}
}

// A mistyped path or a passphrase must fail at config time, where every other
// configuration mistake is reported, rather than deeper in as a database error.
func TestSecretKeyIsValidatedAtLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{
			name:  "missing file",
			args:  []string{"--secret-key", "/nonexistent/subglance.key"},
			wants: []string{"invalid secret-key", "readable file path"},
		},
		{
			name:  "passphrase",
			args:  []string{"--secret-key", "hunter2"},
			wants: []string{"invalid secret-key", "32 bytes"},
		},
		{
			name:  "previous key is validated too",
			args:  []string{"--secret-key", testHexKey, "--secret-key-previous", "hunter2"},
			wants: []string{"invalid secret-key-previous"},
		},
		{
			name:  "previous key equal to the current one",
			args:  []string{"--secret-key", testHexKey, "--secret-key-previous", testHexKey},
			wants: []string{"same value"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.args)
			if err == nil {
				t.Fatal("Load accepted it")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// Turning encryption off is --secret-key-previous alone, so that combination
// must load cleanly; it is a supported state, not a half-configured one.
func TestPreviousKeyAloneIsValid(t *testing.T) {
	cfg, err := Load([]string{"--secret-key-previous", testHexKey})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SecretKey != "" {
		t.Errorf("SecretKey = %q, want empty", cfg.SecretKey)
	}
	key, err := cfg.ResolvePreviousSecretKey()
	if err != nil || len(key) != 32 {
		t.Fatalf("ResolvePreviousSecretKey = %d bytes, %v", len(key), err)
	}
}
