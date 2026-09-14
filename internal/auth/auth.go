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
	"sync/atomic"

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

	key := idKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// Standard PHC string format, so the parameters travel with the hash and
	// can be raised later without invalidating existing passwords.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// maxConcurrentHashes bounds how many argon2 operations run at once.
//
// Each one allocates argonMemory — 64 MiB — and those parameters are correct:
// memory hardness is exactly what makes a stolen hash expensive to attack.
// What was missing is a ceiling on how many are in flight. /auth/login is
// public, and its rate limiter is keyed per email and per address, so a caller
// varying both never shares a bucket with itself. Two dozen simultaneous
// attempts were enough to take a heap to 1.5 GiB; a few dozen more is an
// out-of-memory kill on the 2 GB box this is typically self-hosted on, and a
// monitoring tool that dies is a monitoring tool reporting silence as health.
//
// Hashing is bounded alongside verification because /api/v1/setup is public on
// a fresh instance and hashes a password before it knows whether an account
// already exists. The two share one budget rather than having one each, since
// what has to be capped is the total resident at any moment.
//
// Four. Logins are rare and checks are the hot path, so queueing behind three
// others costs a human a fraction of a second on a screen they visit once a
// week, and caps this package's footprint at 256 MiB no matter what arrives.
const maxConcurrentHashes = 4

// hashSem admits maxConcurrentHashes argon2 calls at a time. Callers queue
// rather than fail: a legitimate login delayed is correct, a legitimate login
// refused because someone else is flooding is the attacker winning.
var hashSem = make(chan struct{}, maxConcurrentHashes)

// inFlightHashes is what the concurrency test observes. It is maintained only
// so the ceiling can be asserted rather than assumed.
var inFlightHashes atomic.Int64

// idKey runs argon2id with the concurrency ceiling applied.
func idKey(password, salt []byte, t, memory uint32, threads uint8, keyLen uint32) []byte {
	hashSem <- struct{}{}
	inFlightHashes.Add(1)
	defer func() {
		inFlightHashes.Add(-1)
		<-hashSem
	}()
	return argon2.IDKey(password, salt, t, memory, threads, keyLen)
}

// VerifyPassword reports whether the password matches the encoded hash.
//
// The comparison is constant-time: a timing difference would let an attacker
// learn the hash byte by byte.
//
// Concurrency is bounded; see maxConcurrentHashes.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		// Decoding allocates nothing worth queueing for, so a malformed hash
		// is answered without occupying a slot.
		return false, err
	}

	got := idKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
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

// PushTokenPrefix marks the token in a push URL.
//
// Distinct from TokenPrefix because the two are not interchangeable and must
// never be confused at a glance: an API token authenticates a user and can do
// anything that user can, while a push token authenticates one job reporting
// on one monitor and can do nothing else. A leaked `sgp_` is an incident; a
// leaked `sgu_` lets someone lie about one backup.
const PushTokenPrefix = "sgu_"

// GeneratePushToken returns a new push-URL token and the prefix to display.
//
// Same 32 bytes of crypto/rand as an API token. The endpoint it opens is
// unauthenticated by necessity — a cron line cannot hold a session — so the
// secrecy of this string is the only thing standing between an outsider and
// the ability to report someone else's job as healthy. That is precisely the
// wrong place to economise on entropy.
func GeneratePushToken() (token, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate push token: %w", err)
	}
	token = PushTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	prefix = token[:len(PushTokenPrefix)+6]
	return token, prefix, nil
}

// HashToken returns the storage form of a session, API or push token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
