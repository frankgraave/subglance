package mutelink

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSigner(t *testing.T, fill byte) *Signer {
	t.Helper()
	s, err := New(bytes.Repeat([]byte{fill}, keyLength))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var testNow = time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC)

func TestTokenRoundTrip(t *testing.T) {
	s := testSigner(t, 1)
	expires := testNow.Add(Lifetime)
	token := s.Token(312, 4, expires)

	claim, err := s.Verify(token, testNow)
	if err != nil {
		t.Fatalf("Verify(%q): %v", token, err)
	}
	if claim.IncidentID != 312 || claim.ChannelID != 4 || !claim.Expires.Equal(expires) {
		t.Errorf("claim = %+v", claim)
	}
	if strings.ContainsAny(token, "/?#%+= ") {
		t.Errorf("token %q needs escaping in a URL path", token)
	}
}

func TestExpiredTokenIsGenuineButRefused(t *testing.T) {
	s := testSigner(t, 1)
	token := s.Token(312, 4, testNow.Add(Lifetime))

	if _, err := s.Verify(token, testNow.Add(Lifetime-time.Second)); err != nil {
		t.Errorf("a second before expiry: %v", err)
	}
	claim, err := s.Verify(token, testNow.Add(Lifetime))
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry: err = %v, want ErrExpired", err)
	}
	if claim.IncidentID != 312 {
		t.Errorf("an expired claim lost its incident: %+v", claim)
	}
}

// TestChangedTokenIsRefused: every part of the token is covered by the
// signature, so pointing a link at another incident or channel, or giving
// it a longer life, makes it a link that does not work.
func TestChangedTokenIsRefused(t *testing.T) {
	s := testSigner(t, 1)
	token := s.Token(312, 4, testNow.Add(Lifetime))
	parts := strings.Split(token, ".")
	tag := parts[3]
	flipped := []byte(tag)
	if flipped[0] == 'A' {
		flipped[0] = 'B'
	} else {
		flipped[0] = 'A'
	}

	cases := map[string]string{
		"other incident":   "313." + parts[1] + "." + parts[2] + "." + tag,
		"other channel":    parts[0] + ".5." + parts[2] + "." + tag,
		"longer life":      parts[0] + "." + parts[1] + ".9999999999." + tag,
		"changed tag":      strings.Join(parts[:3], ".") + "." + string(flipped),
		"cut short":        token[:len(token)-3],
		"no tag":           strings.Join(parts[:3], "."),
		"extra part":       token + ".1",
		"leading zero":     "0" + token,
		"signed":           "+" + token,
		"negative":         "-312." + parts[1] + "." + parts[2] + "." + tag,
		"zero incident":    "0." + parts[1] + "." + parts[2] + "." + tag,
		"padded tag":       token + "==",
		"empty":            "",
		"very long":        token + strings.Repeat("A", maxTokenLength),
		"other characters": parts[0] + "." + parts[1] + "." + parts[2] + "." + strings.ReplaceAll(tag, tag[:1], "/"),
	}
	for name, bad := range cases {
		if _, err := s.Verify(bad, testNow); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s (%q): err = %v, want ErrInvalid", name, bad, err)
		}
	}

	// Another instance's key: as good as a forgery, and never "expired".
	other := testSigner(t, 2)
	if _, err := other.Verify(token, testNow); !errors.Is(err, ErrInvalid) {
		t.Errorf("another key: err = %v, want ErrInvalid", err)
	}
	if _, err := other.Verify(token, testNow.Add(48*time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Errorf("another key, past expiry: err = %v, want ErrInvalid, not ErrExpired", err)
	}
}

func TestLoadDerivesFromTheSecretKey(t *testing.T) {
	dir := t.TempDir()
	secret := bytes.Repeat([]byte{7}, 32)
	a, err := Load(dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(t.TempDir(), secret)
	if err != nil {
		t.Fatal(err)
	}
	token := a.Token(1, 2, testNow.Add(time.Hour))
	if _, err := b.Verify(token, testNow); err != nil {
		t.Errorf("the same secret key on another data directory refuses the link: %v", err)
	}
	if bytes.Equal(a.key, secret) {
		t.Error("the signing key is the secret key itself; it must be derived")
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !os.IsNotExist(err) {
		t.Errorf("a key file was written although --secret-key is set: %v", err)
	}
	c, err := Load(dir, bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(token, testNow); !errors.Is(err, ErrInvalid) {
		t.Errorf("a changed secret key still accepts old links: %v", err)
	}
}

func TestLoadCreatesAndKeepsTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	a, err := Load(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, KeyFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no key file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key file mode = %o, want 600", mode)
	}

	// A restart reads the same key, so links sent before it still work.
	b, err := Load(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := a.Token(1, 2, testNow.Add(time.Hour))
	if _, err := b.Verify(token, testNow); err != nil {
		t.Errorf("after a restart the link does not work: %v", err)
	}

	// Another data directory, another key.
	c, err := Load(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(token, testNow); !errors.Is(err, ErrInvalid) {
		t.Errorf("a fresh key file accepts another instance's link: %v", err)
	}
}

func TestLoadRefusesABrokenKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFile)
	if err := os.WriteFile(path, []byte("not-a-key-s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir, nil)
	if err == nil {
		t.Fatal("a key file that holds no key is accepted")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the error quotes the file's contents: %v", err)
	}
	if !strings.Contains(err.Error(), KeyFile) {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestNewRefusesAShortKey(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Error("a 16-byte key is accepted")
	}
}
