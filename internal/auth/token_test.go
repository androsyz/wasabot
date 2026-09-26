package auth

import (
	"regexp"
	"testing"
)

func TestNewToken(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		token, hash, err := NewToken()
		if err != nil {
			t.Fatalf("new token: %v", err)
		}

		if len(token) != 43 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(token) {
			t.Fatalf("token %q should be 43 URL-safe characters (256 bits)", token)
		}
		if hash != HashToken(token) || hash == token || len(hash) != 64 {
			t.Fatalf("hash %q does not match the token", hash)
		}
		if seen[token] {
			t.Fatal("tokens must not repeat")
		}
		seen[token] = true
	}
}

func TestHashToken(t *testing.T) {
	first, second := HashToken("abc"), HashToken("abc")
	if first != second {
		t.Fatal("hashing is deterministic")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Fatal("different tokens hash differently")
	}
	// SHA-256 of "abc"
	if got := HashToken("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("got %s", got)
	}
}

func TestNewCSRFToken(t *testing.T) {
	a, err := NewCSRFToken()
	b, _ := NewCSRFToken()

	if err != nil || len(a) != 43 || a == b {
		t.Fatalf("got %q and %q, %v", a, b, err)
	}
}
