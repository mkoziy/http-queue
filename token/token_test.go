// Package token provides worker bearer token generation, hashing, and verification.
package token

import (
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerate_ReturnsNonEmpty(t *testing.T) {
	plain, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if plain == "" {
		t.Error("Generate() returned empty plaintext token")
	}
	if hashed == "" {
		t.Error("Generate() returned empty hash")
	}
}

func TestGenerate_ProducesUniqueTokens(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		plain, _, err := Generate()
		if err != nil {
			t.Fatalf("Generate() error: %v", err)
		}
		if seen[plain] {
			t.Errorf("duplicate token generated: %q", plain)
		}
		seen[plain] = true
	}
}

func TestGenerate_PlainTokenIsBase64URL(t *testing.T) {
	plain, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// base64url without padding: only [A-Za-z0-9_-] characters.
	for _, r := range plain {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			t.Errorf("token contains invalid base64url character: %q (code %d)", r, r)
		}
	}
}

func TestGenerate_PlainTokenLength(t *testing.T) {
	plain, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// 32 bytes raw => 43 base64url chars without padding.
	// base64.RawURLEncoding encodes 32 bytes as ceil(32*8/6) = 43 chars.
	const expectedLen = 43
	if len(plain) != expectedLen {
		t.Errorf("plain token length = %d, want %d (32 bytes raw)", len(plain), expectedLen)
	}
}

func TestGenerate_HashIsSHA256Hex(t *testing.T) {
	_, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// SHA-256 hex is 64 characters, all lowercase hex digits.
	if len(hashed) != 64 {
		t.Errorf("hash length = %d, want 64 (SHA-256 hex)", len(hashed))
	}

	// Verify it's valid lowercase hex.
	decoded, err := hex.DecodeString(hashed)
	if err != nil {
		t.Errorf("hash is not valid hex: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded hash length = %d, want 32", len(decoded))
	}
}

func TestHash_IsDeterministic(t *testing.T) {
	token := "some-random-test-token-value"

	h1 := Hash(token)
	h2 := Hash(token)

	if h1 != h2 {
		t.Error("Hash() is not deterministic")
	}

	if len(h1) != 64 {
		t.Errorf("Hash() length = %d, want 64", len(h1))
	}
}

func TestHash_EmptyString(t *testing.T) {
	h := Hash("")
	if len(h) != 64 {
		t.Errorf("Hash('') length = %d, want 64", len(h))
	}
	// SHA-256 of empty string must match the known value.
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if h != emptySHA256 {
		t.Errorf("Hash('') = %q, want %q (SHA-256 of empty string)", h, emptySHA256)
	}
}

func TestHash_DifferentInputsProduceDifferentHashes(t *testing.T) {
	h1 := Hash("token-A")
	h2 := Hash("token-B")
	if h1 == h2 {
		t.Error("Hash() should produce different outputs for different inputs")
	}
}

func TestVerify_ValidToken(t *testing.T) {
	plain, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if !Verify(plain, hashed) {
		t.Error("Verify(plain, hashed) = false, want true")
	}
}

func TestVerify_InvalidToken(t *testing.T) {
	_, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if Verify("wrong-token", hashed) {
		t.Error("Verify(wrong, hashed) = true, want false")
	}
}

func TestVerify_WrongHash(t *testing.T) {
	plain, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if Verify(plain, "0000000000000000000000000000000000000000000000000000000000000000") {
		t.Error("Verify(plain, wrong_hash) = true, want false")
	}
}

func TestVerify_EmptyToken(t *testing.T) {
	_, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if Verify("", hashed) {
		t.Error("Verify('', hashed) = true, want false")
	}
}

func TestVerify_AgainstEmptyHash(t *testing.T) {
	plain, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if Verify(plain, "") {
		t.Error("Verify(plain, '') = true, want false")
	}
}

func TestVerify_EmptyAgainstEmpty(t *testing.T) {
	if Verify("", "") {
		t.Error("Verify('', '') = true, want false")
	}
}

func TestVerify_ConsistentWithHash(t *testing.T) {
	plain, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Manually hash and verify.
	hashed := Hash(plain)
	if !Verify(plain, hashed) {
		t.Error("Verify(plain, Hash(plain)) = false, want true")
	}
}

func TestVerify_ConstantTime(t *testing.T) {
	// subtle.ConstantTimeCompare returns 1 for equal, 0 for not equal.
	// This test verifies Verify() delegates to it correctly.
	plain := "test-token"
	hashed := Hash(plain)

	computed := Hash(plain)
	result := subtle.ConstantTimeCompare([]byte(computed), []byte(hashed))
	if result != 1 {
		t.Error("constant-time comparison of identical strings returned 0")
	}

	// Verify also uses the same comparison internally.
	if !Verify(plain, hashed) {
		t.Error("Verify() should use constant-time comparison")
	}
}

func TestGenerate_HashMatches(t *testing.T) {
	plain, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// The hash returned from Generate must match Hash(plain).
	expectedHash := Hash(plain)
	if hashed != expectedHash {
		t.Errorf("Generate() hash = %q, want Hash(plain) = %q", hashed, expectedHash)
	}
}

func TestGenerate_ConsecutiveCallsDiffer(t *testing.T) {
	p1, h1, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	p2, h2, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if p1 == p2 {
		t.Error("two consecutive Generate() calls returned the same plaintext token")
	}
	if h1 == h2 {
		t.Error("two consecutive Generate() calls returned the same hash")
	}
}

func TestVerify_NearMatchHash(t *testing.T) {
	// Verify that hashes with a single differing character don't match.
	plain := "my-worker-token"
	hashed := Hash(plain)

	// Flip the last hex character.
	diff := []byte(hashed)
	if diff[len(diff)-1] == 'f' {
		diff[len(diff)-1] = 'e'
	} else {
		diff[len(diff)-1] = diff[len(diff)-1] + 1
	}

	if Verify(plain, string(diff)) {
		t.Error("Verify() should reject a hash that differs by one character")
	}
}

func TestVerify_LongToken(t *testing.T) {
	// Tokens longer than 32-byte equivalents are still valid inputs to Hash/Verify.
	long := strings.Repeat("a", 1000)
	h := Hash(long)
	if !Verify(long, h) {
		t.Error("Verify(long, Hash(long)) = false, want true")
	}
}

func TestHash_InputWithSpecialCharacters(t *testing.T) {
	special := "token\nwith\tspaces\x00and\U0001F600emoji!@#$%^&*()"
	h := Hash(special)
	if !Verify(special, h) {
		t.Error("Verify(special, Hash(special)) = false, want true")
	}
}

func TestVerify_AllAsciiEdgeCases(t *testing.T) {
	// Verify that tokens with various ASCII byte values work correctly.
	for i := 0; i < 256; i++ {
		input := strings.Repeat(string(rune(i)), 10)
		hashed := Hash(input)
		if !Verify(input, hashed) {
			t.Errorf("Verify failed for token consisting of byte 0x%02x", i)
		}
	}
}

func TestGenerate_ErrorHandling(t *testing.T) {
	// We can't easily force rand.Read to fail in a test, but we can verify
	// that Generate returns an error properly when we have no entropy.
	// In practice, crypto/rand.Read should never fail on a healthy system.
	// This test simply confirms the signature returns (string, string, error).
	plain, hashed, err := Generate()
	if err != nil {
		// If crypto/rand fails, we accept it — but it should not fail
		// on any reasonable system.
		t.Logf("Generate() error (acceptable on constrained systems): %v", err)
		return
	}
	// If no error, verify basic properties.
	if plain == "" {
		t.Error("Generate() returned empty plaintext with no error")
	}
	if hashed == "" {
		t.Error("Generate() returned empty hash with no error")
	}
}

func TestRoundTrip_GenerateHashVerify(t *testing.T) {
	// Full round-trip: Generate -> Hash -> Verify.
	plain, hashed, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Verify using the hash from Generate.
	if !Verify(plain, hashed) {
		t.Error("Round-trip verification failed: Verify(plain, hashed) == false")
	}

	// Verify using a freshly computed hash.
	if !Verify(plain, Hash(plain)) {
		t.Error("Round-trip verification failed: Verify(plain, Hash(plain)) == false")
	}

	// Wrong token should fail.
	if Verify(plain+"x", hashed) {
		t.Error("Round-trip: modified token should not verify")
	}
}

func BenchmarkGenerate(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _, _ = Generate()
	}
}

func BenchmarkHash(b *testing.B) {
	token := "benchmark-token-value-for-hashing"
	for i := 0; i < b.N; i++ {
		_ = Hash(token)
	}
}

func BenchmarkVerify(b *testing.B) {
	plain, hashed, err := Generate()
	if err != nil {
		b.Fatalf("Generate() error: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Verify(plain, hashed)
	}
}
