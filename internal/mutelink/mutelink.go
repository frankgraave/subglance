// Package mutelink signs and checks the link in an alert that mutes the
// incident's repeat alerts without a session.
//
// Whoever reads an alert on a phone at 03:00 has usually decided what to do
// before they finish reading it, and "stop reminding me" should not need a
// sign-in. The link carries its own authority: an HMAC over what it may do
// (mute), the incident, the channel the alert went to and when it stops
// working, under a key only this instance holds. Nothing is stored per link,
// so a link is checked by recomputing its signature, and following one twice
// does nothing the second time because muting an incident that is already
// muted changes nothing.
package mutelink

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Lifetime is how long a link works after its alert is sent. The incident
// resolving ends it sooner; that is checked where the link is followed,
// because only the database knows.
//
// A day covers the night the alert arrived in and the morning after. A
// longer life would leave a working credential in chat histories and mail
// archives for an outage that nobody remembers.
const Lifetime = 24 * time.Hour

// PathPrefix is where a link points, below the instance's address: the
// token follows it. The page is served by internal/api.
const PathPrefix = "/mute/"

// KeyFile is the name of the generated key in the data directory, used when
// no --secret-key is set.
const KeyFile = "mute-link.key"

// keyLength is the length of the signing key in bytes.
const keyLength = 32

// tagLength is how many bytes of the HMAC-SHA256 a link carries. 128 bits
// is the usual floor for a truncated MAC and keeps the link short enough to
// read in a plain-text message.
const tagLength = 16

// maxTokenLength bounds the work spent on a token before it is refused: four
// decimal numbers and a 22-character tag fit with room to spare.
const maxTokenLength = 128

// purpose is signed into every token, so a signature made for this link can
// never be read as one made for anything else the key is later used for.
const purpose = "mute"

// derivationLabel separates the key derived from --secret-key from the key
// that encrypts channel settings: the same secret, two unrelated keys.
const derivationLabel = "subglance mute-link key v1"

var (
	// ErrInvalid is a token that this instance did not sign as it is: cut
	// short, changed, or signed with another key.
	ErrInvalid = errors.New("mute link is not valid")
	// ErrExpired is a genuine token whose lifetime is over.
	ErrExpired = errors.New("mute link has expired")
)

// Claim is what a valid token says.
type Claim struct {
	IncidentID int64
	ChannelID  int64
	Expires    time.Time
}

// Signer makes and checks tokens with one key.
type Signer struct {
	key []byte
}

// New returns a Signer for key, which must be keyLength bytes.
func New(key []byte) (*Signer, error) {
	if len(key) != keyLength {
		return nil, fmt.Errorf("mute link key is %d bytes, want %d", len(key), keyLength)
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

// Load returns the Signer for an instance. With a --secret-key the key is
// derived from it, so nothing is written; without one it is read from
// KeyFile in dataDir, which is created on first use and readable by the
// owner only.
func Load(dataDir string, secretKey []byte) (*Signer, error) {
	if len(secretKey) > 0 {
		mac := hmac.New(sha256.New, secretKey)
		mac.Write([]byte(derivationLabel))
		return New(mac.Sum(nil))
	}
	key, err := loadKeyFile(filepath.Join(dataDir, KeyFile))
	if err != nil {
		return nil, err
	}
	return New(key)
}

// loadKeyFile reads the key at path, creating it when there is none.
func loadKeyFile(path string) ([]byte, error) {
	key, err := readKeyFile(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	key = make([]byte, keyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate mute link key: %w", err)
	}
	// O_EXCL: two processes starting on one data directory must not each
	// write a key and leave the other signing with one that is gone. The
	// loser reads what the winner wrote.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path built from the data dir
	if errors.Is(err, fs.ErrExist) {
		return readKeyFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return key, nil
}

func readKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path built from the data dir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != keyLength {
		// The contents are a key, or meant to be one: never quote them.
		return nil, fmt.Errorf("%s does not hold a %d-byte hex key; delete it to have a new one made, "+
			"which stops mute links already sent from working", path, keyLength)
	}
	return key, nil
}

// Token returns the signed token for muting incidentID, sent to channelID,
// that stops working at expires.
//
// The shape is "<incident>.<channel>.<expires>.<tag>": decimal numbers, the
// expiry in Unix seconds, and the tag in unpadded base64url, so the whole
// token is safe in a URL path without escaping.
func (s *Signer) Token(incidentID, channelID int64, expires time.Time) string {
	body := fmt.Sprintf("%d.%d.%d", incidentID, channelID, expires.Unix())
	return body + "." + base64.RawURLEncoding.EncodeToString(s.tag(body))
}

// Verify checks token and returns what it says. A token whose signature
// does not match is ErrInvalid whatever its expiry, so a forged link is
// never reported as merely old; a genuine one past its expiry at now is
// ErrExpired, with the claim, so the caller can still point to the incident.
func (s *Signer) Verify(token string, now time.Time) (Claim, error) {
	if len(token) > maxTokenLength {
		return Claim{}, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return Claim{}, ErrInvalid
	}
	var nums [3]int64
	for i, p := range parts[:3] {
		n, err := strconv.ParseInt(p, 10, 64)
		// Only the form Token writes: no sign, no leading zero, no
		// second spelling of the same number for a signature to cover.
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != p {
			return Claim{}, ErrInvalid
		}
		nums[i] = n
	}
	got, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	body := strings.Join(parts[:3], ".")
	if err != nil || !hmac.Equal(got, s.tag(body)) {
		return Claim{}, ErrInvalid
	}
	claim := Claim{IncidentID: nums[0], ChannelID: nums[1], Expires: time.Unix(nums[2], 0).UTC()}
	if !now.Before(claim.Expires) {
		return claim, ErrExpired
	}
	return claim, nil
}

func (s *Signer) tag(body string) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(purpose + "." + body))
	return mac.Sum(nil)[:tagLength]
}
