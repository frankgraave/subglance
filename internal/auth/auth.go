// Package auth handles password hashing, session tokens and API tokens.
//
// # Why argon2id
//
// Passwords are hashed with argon2id, the winner of the Password Hashing
// Competition and the algorithm OWASP recommends first. Unlike bcrypt it is
// memory-hard: an attacker with a GPU farm cannot trade silicon for speed as
// cheaply, because every guess must also allocate 64 MiB.
//
// # Why tokens are stored hashed
//
// Session and API tokens are stored as SHA-256 hashes, never in clear. A
// database file is a thing that ends up in backups, in support tickets and on
// laptops; if it leaks, it must not hand out working credentials. SHA-256 is
// the right choice here rather than argon2 precisely because these tokens are
// 256 bits of randomness — there is no dictionary to attack, so the slow-hash
// argument does not apply and the speed matters on every request.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Errors returned by this package.
var (
	ErrInvalidHash     = errors.New("auth: malformed password hash")
	ErrIncompatible    = errors.New("auth: incompatible hash version")
	ErrPasswordTooWeak = errors.New("auth: password does not meet the minimum requirements")
)

// MinPasswordLength is the shortest password accepted.
//
// Twelve, with no composition rules. Forcing a symbol and a digit produces
// "Password1!" and a sticky note; length is what actually costs an attacker
// anything. This matches current NIST guidance.
const MinPasswordLength = 12

// argon2 parameters. These follow the OWASP recommendation for argon2id
// (19 MiB, 2 iterations, 1 degree of parallelism) rounded up to 64 MiB, which
// a monitoring server can afford — logins are rare, checks are the hot path.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	saltLen             = 16
)

// TokenPrefix marks SubGlance API tokens.
//
// A recognisable prefix lets secret scanners (GitHub's included) spot a leaked
// token in a commit and lets a human tell at a glance what they are looking at.
const TokenPrefix = "sgp_"

// HashPassword returns an encoded argon2id hash of the password.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// Standard PHC string format, so the parameters travel with the hash and
	// can be raised later without invalidating existing passwords.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether the password matches the encoded hash.
//
// The comparison is constant-time: a timing difference would let an attacker
// learn the hash byte by byte.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, ErrIncompatible
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	if len(salt) == 0 || len(key) == 0 {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}

// ValidatePassword enforces the minimum policy.
func ValidatePassword(password string) error {
	if len([]rune(password)) < MinPasswordLength {
		return fmt.Errorf("%w: at least %d characters", ErrPasswordTooWeak, MinPasswordLength)
	}
	// A cap keeps a multi-megabyte "password" from turning every login into a
	// denial-of-service against our own argon2 memory budget.
	if len(password) > 1024 {
		return fmt.Errorf("%w: at most 1024 bytes", ErrPasswordTooWeak)
	}
	return nil
}

// GenerateSessionToken returns a new random session token.
//
// 32 bytes of crypto/rand, URL-safe encoded. Guessing one is not a threat model
// worth worrying about at that size.
func GenerateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateAPIToken returns a new API token and the prefix to display for it.
//
// The plaintext is returned exactly once; only its hash is ever stored.
func GenerateAPIToken() (token, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate api token: %w", err)
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)

	// Enough to distinguish tokens in a list, far too little to brute-force
	// the remaining 250-odd bits.
	prefix = token[:len(TokenPrefix)+6]
	return token, prefix, nil
}

// HashToken returns the storage form of a session or API token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
