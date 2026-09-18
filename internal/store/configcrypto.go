package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Encryption at rest for notification channel configuration.
//
// # What this protects against, and what it does not
//
// notif_channels.config_json holds webhook URLs, bot tokens and SMTP
// passwords. The threat this addresses is narrow and worth stating exactly:
// someone who obtains the database file — a backup on a laptop, a `docker cp`
// of the data volume, a stolen disk — and who does not also have the key.
//
// It does nothing against an attacker who can read the running process: the
// key is in memory, and the decrypted config is handed to the notifier on
// every delivery. It does nothing against someone who can read the key file
// or the process environment. Anyone with write access to the database can
// still delete rows or replace a channel wholesale; authentication of the
// ciphertext (below) only guarantees that a *modified* ciphertext is refused
// rather than decrypted into something a notifier would act on.
//
// # Why the key is never derived from the database's own neighbourhood
//
// The rejected alternative was a key file that appears automatically beside
// subglance.db on first start, so that encryption would be on by default with
// no operator action. It was rejected because such a key travels with every
// backup and every copy of the data volume: it encrypts nothing against the
// attacker who takes the data, while creating the impression of protection.
// A key that is always where the data is, is not a key. Being explicit — and
// therefore plaintext by default — is more honest than being automatic and
// hollow. See SECURITY.md for the operator-facing form of this argument.

// configCipherPrefix marks an encrypted config_json value.
//
// The stored value is self-describing rather than sniffed: during migration a
// database legitimately holds both plaintext and encrypted rows, and deciding
// between them by "does it parse as JSON" would mean that any ciphertext that
// happened to start with '{' took the wrong branch, and that a genuinely
// corrupt plaintext row was silently treated as ciphertext. The version digit
// is there so a future construction can be introduced without having to guess
// what old rows are.
//
//	sgc1.<base64url(nonce || ciphertext||tag)>   AES-256-GCM, 12-byte nonce
const configCipherPrefix = "sgc1."

// errWrongKey is what every failed authentication collapses to.
//
// GCM does not distinguish "wrong key" from "tampered ciphertext" — both are
// simply a tag that does not verify — so pretending to tell them apart would
// be a lie. Callers turn this into a startup refusal.
var errWrongKey = errors.New("store: channel config could not be decrypted with this secret key")

// configCipher encrypts and decrypts channel config values.
//
// A nil *configCipher is a valid, working value meaning "no encryption": that
// is the default configuration, and making it the zero value keeps the no-key
// path free of branches that could drift from the encrypted one.
type configCipher struct {
	aead cipher.AEAD
	// key is retained only to compare two ciphers for equality during
	// rotation. It is never logged or returned.
	key []byte
}

// SecretKeyLength is the exact amount of key material AES-256-GCM needs.
const SecretKeyLength = 32

// newConfigCipher builds a cipher from exactly SecretKeyLength bytes.
func newConfigCipher(key []byte) (*configCipher, error) {
	if len(key) != SecretKeyLength {
		return nil, fmt.Errorf("store: secret key must be %d bytes, got %d", SecretKeyLength, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("store: build cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: build GCM: %w", err)
	}
	return &configCipher{aead: aead, key: append([]byte(nil), key...)}, nil
}

// sameKeyAs reports whether two ciphers hold identical key material, so that
// `--secret-key-previous` naming the key already in use is a no-op instead of
// a rewrap of every row.
func (c *configCipher) sameKeyAs(other *configCipher) bool {
	if c == nil || other == nil {
		return c == other
	}
	return subtle.ConstantTimeCompare(c.key, other.key) == 1
}

// seal encrypts plaintext into the stored string form.
//
// The nonce is fresh random bytes per call and stored in front of the
// ciphertext. Per-row and per-write is the point: GCM loses confidentiality
// *and* integrity if a nonce is ever reused under the same key, and a counter
// would have to be persisted and would repeat after a restore from backup.
// Twelve random bytes per write needs no coordination and no state.
//
// There is no additional authenticated data. Binding a row to its own id was
// considered and does not fit: CreateChannel does not know the id until the
// INSERT has returned, so the value would have to be written twice, and the
// attack it would prevent — swapping two rows' ciphertexts inside the
// database — is available to anyone who can write to the database at all,
// which is strictly more access than this design claims to defend against.
func (c *configCipher) seal(plaintext string) (string, error) {
	// One buffer holds the nonce and then the sealed output, so the stored
	// value is nonce||ciphertext||tag with no second allocation. Seal appends
	// to the slice it is given, which is why the nonce is read into the first
	// NonceSize bytes and the sealed output is appended after it.
	n := c.aead.NonceSize()
	buf := make([]byte, n, n+len(plaintext)+c.aead.Overhead())
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("store: read nonce: %w", err)
	}
	buf = c.aead.Seal(buf, buf[:n], []byte(plaintext), nil)
	return configCipherPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// open decrypts a stored value produced by seal.
func (c *configCipher) open(stored string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, configCipherPrefix))
	if err != nil {
		return "", fmt.Errorf("%w: stored value is not valid base64", errWrongKey)
	}
	n := c.aead.NonceSize()
	if len(raw) < n+c.aead.Overhead() {
		return "", fmt.Errorf("%w: stored value is too short to hold a nonce and a tag", errWrongKey)
	}
	plaintext, err := c.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		// Deliberately not wrapping err: GCM's own message ("message
		// authentication failed") reads like a network problem, and the
		// distinction it hides — wrong key versus tampering — does not exist.
		return "", errWrongKey
	}
	return string(plaintext), nil
}

// isEncryptedConfig reports whether a stored config_json value is ciphertext.
func isEncryptedConfig(stored string) bool {
	return strings.HasPrefix(stored, configCipherPrefix)
}

// ParseSecretKey turns the value of --secret-key into key material.
//
// The option accepts either the key itself or the path to a file holding it,
// and an existing file always wins. The two forms are not quite disjoint:
// base64's alphabet contains '/', so a 43-character path such as
// /tmp/AAAA...  is also valid raw base64 for 32 bytes, and deciding by shape
// first would quietly use a key derived from the path instead of the key in
// the file it names. On an existing database that is a start-up failure with a
// misleading cause; worse, the derived key is reconstructible by anyone who can
// see the command line, which the file form exists to avoid.
//
// Statting first costs nothing in message quality, because a value that is
// neither a readable file nor key material still falls through to the combined
// error below.
//
// Only raw key material of exactly SecretKeyLength bytes is accepted; a
// passphrase is not stretched into a key. Accepting one would mean choosing
// KDF parameters, storing a salt beside the data, and keeping both stable
// across versions forever, and it would let "hunter2" look like encryption.
// The cost of refusing is one command in the documentation.
func ParseSecretKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	// Only a value that demonstrably names nothing may be read as key
	// material. Any other stat failure — a component that is not a directory,
	// a directory this process may not traverse — means something is there
	// that could not be looked at, and falling back to the shape of the path
	// would silently run on a key anyone who can read the command line can
	// reconstruct. Failing closed here costs one confusing start; guessing
	// costs the confidentiality the key was for.
	//
	// Lstat, not Stat, because Stat follows symlinks: a key file mounted as a
	// link whose target is missing — the ordinary shape of a secret that
	// failed to mount — reports ErrNotExist for a path that plainly names
	// something. An operator who meant a key file must never end up
	// encrypting with a key derived from its path, and the error they get
	// instead names the file, which is the problem they actually have.
	if _, statErr := os.Lstat(value); errors.Is(statErr, fs.ErrNotExist) {
		if key, ok := decodeSecretKey(value); ok {
			return key, nil
		}
	}

	// The path is the operator's own --secret-key value, read by the process
	// they started; there is no untrusted input anywhere near it.
	contents, err := os.ReadFile(value) //nolint:gosec // operator-supplied key file path
	if err != nil {
		// The message has to cover both readings, because at this point either
		// could have been meant — and it must not repeat the value. A mistyped
		// key is still key material, and os.ReadFile's *fs.PathError carries
		// the path it was given, so wrapping it with %w would print the
		// supplied key to stderr on the one failure where the operator most
		// likely typed a key by hand. Only the reason survives.
		// The reason is still wrapped rather than flattened to text, so a
		// caller can errors.Is it against fs.ErrNotExist or fs.ErrPermission;
		// PathError.Err is the half that carries no path.
		reason := err
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			reason = pathErr.Err
		}
		return nil, fmt.Errorf("secret key is neither %d bytes of key material "+
			"(64 hex or base64 characters) nor a readable file path (%w)", SecretKeyLength, reason)
	}
	key, ok := decodeSecretKey(string(contents))
	if !ok {
		return nil, fmt.Errorf("key file %s does not contain %d bytes of key material; "+
			"expected 64 hex characters or base64, such as the output of "+
			"`openssl rand -hex 32`", value, SecretKeyLength)
	}
	return key, nil
}

// decodeSecretKey accepts the two encodings the documented generator commands
// produce — `openssl rand -hex 32` and `openssl rand -base64 32` — and
// nothing else. A trailing newline from a key file is expected, not an error.
func decodeSecretKey(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if len(s) == hex.EncodedLen(SecretKeyLength) {
		if b, err := hex.DecodeString(s); err == nil {
			return b, true
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == SecretKeyLength {
			return b, true
		}
	}
	return nil, false
}
