package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	const password = "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("the hash contains the plaintext password")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash %q is not in argon2id PHC format", hash)
	}

	ok, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("the correct password did not verify")
	}

	ok, err = VerifyPassword("wrong-horse-battery-staple", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("a wrong password verified")
	}
}

// Equal passwords must produce different hashes, or the salt is not doing its
// job and one rainbow table breaks every account at once.
func TestHashesAreSalted(t *testing.T) {
	const password = "correct-horse-battery-staple"

	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Error("the same password produced identical hashes; the salt is not random")
	}

	// Both must still verify.
	for _, h := range []string{first, second} {
		ok, err := VerifyPassword(password, h)
		if err != nil || !ok {
			t.Errorf("hash %q did not verify: ok=%v err=%v", h, ok, err)
		}
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	bad := []string{
		"",
		"not-a-hash",
		"$argon2id$",
		"$argon2i$v=19$m=65536,t=2,p=1$c2FsdA$aGFzaA",       // wrong variant
		"$argon2id$v=99$m=65536,t=2,p=1$c2FsdA$aGFzaA",      // wrong version
		"$argon2id$v=19$m=65536,t=2,p=1$!!!invalid!!!$aGFz", // bad base64
		"$argon2id$v=19$m=bad,t=2,p=1$c2FsdA$aGFzaA",        // bad parameters
	}
	for _, h := range bad {
		t.Run(h, func(t *testing.T) {
			if _, err := VerifyPassword("whatever", h); err == nil {
				t.Errorf("malformed hash %q was accepted", h)
			}
		})
	}
}

func TestPasswordPolicy(t *testing.T) {
	tooShort := strings.Repeat("a", MinPasswordLength-1)
	if err := ValidatePassword(tooShort); err == nil {
		t.Errorf("a %d-character password was accepted", len(tooShort))
	}

	justLong := strings.Repeat("a", MinPasswordLength)
	if err := ValidatePassword(justLong); err != nil {
		t.Errorf("a %d-character password was rejected: %v", len(justLong), err)
	}

	// A multi-megabyte password would turn every login attempt into a
	// denial-of-service against our own argon2 memory budget.
	if err := ValidatePassword(strings.Repeat("a", 2048)); err == nil {
		t.Error("an oversized password was accepted")
	}
}

func TestHashPasswordEnforcesPolicy(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("HashPassword accepted a password that violates the policy")
	}
}

func TestGenerateSessionTokenIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		token, err := GenerateSessionToken()
		if err != nil {
			t.Fatalf("GenerateSessionToken: %v", err)
		}
		if seen[token] {
			t.Fatal("a session token repeated within 1000 draws")
		}
		if len(token) < 40 {
			t.Fatalf("token %q is shorter than expected for 32 random bytes", token)
		}
		seen[token] = true
	}
}

func TestGenerateAPIToken(t *testing.T) {
	token, prefix, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}

	if !strings.HasPrefix(token, TokenPrefix) {
		t.Errorf("token %q lacks the %q prefix that secret scanners match on", token, TokenPrefix)
	}
	if !strings.HasPrefix(token, prefix) {
		t.Errorf("prefix %q is not a prefix of token %q", prefix, token)
	}
	// The displayed prefix must identify a token without being enough to guess
	// it: short enough to be useless, long enough to tell two tokens apart.
	if len(prefix) >= len(token)/2 {
		t.Errorf("prefix %q reveals too much of the token", prefix)
	}
}

func TestHashTokenIsStableAndOpaque(t *testing.T) {
	const token = "sgp_abcdefghijklmnop"

	first := HashToken(token)
	if first != HashToken(token) {
		t.Error("HashToken is not deterministic")
	}
	if strings.Contains(first, token) {
		t.Error("the hash contains the token")
	}
	if first == HashToken(token+"x") {
		t.Error("different tokens hashed to the same value")
	}
	if len(first) != 64 {
		t.Errorf("hash length = %d, want 64 hex characters for SHA-256", len(first))
	}
}
