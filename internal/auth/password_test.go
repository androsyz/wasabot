package auth

import (
	"errors"
	"strings"
	"testing"
)

// cheap settings keep the tests fast; the algorithm is the same
var testParams = Params{Memory: 8, Time: 1, Threads: 1}

func TestHashAndVerify(t *testing.T) {
	h := NewHasher(testParams)

	encoded, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if ok, err := Verify("correct horse battery staple", encoded); err != nil || !ok {
		t.Errorf("the right password must verify: %v, %v", ok, err)
	}
	for _, wrong := range []string{"", "correct horse battery stapl", "Correct horse battery staple", "correct horse battery staple "} {
		if ok, err := Verify(wrong, encoded); err != nil || ok {
			t.Errorf("%q must not verify: %v, %v", wrong, ok, err)
		}
	}
}

func TestHash_Format(t *testing.T) {
	encoded, err := NewHasher(Params{Memory: 16, Time: 2, Threads: 1}).Hash("pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=16,t=2,p=1$") || strings.Contains(encoded, "pw") {
		t.Fatalf("unexpected encoding %q", encoded)
	}
}

func TestHash_UsesAFreshSaltEachTime(t *testing.T) {
	h := NewHasher(testParams)

	a, _ := h.Hash("same password")
	b, _ := h.Hash("same password")

	if a == b {
		t.Fatal("two hashes of the same password must differ")
	}
}

func TestHash_HandlesUnicode(t *testing.T) {
	encoded, _ := NewHasher(testParams).Hash("pässwörd-日本語-🔑")

	if ok, _ := Verify("pässwörd-日本語-🔑", encoded); !ok {
		t.Fatal("non-ASCII passwords must verify")
	}
	if ok, _ := Verify("passwörd-日本語-🔑", encoded); ok {
		t.Fatal("a different password must not")
	}
}

func TestVerify_OldHashesKeepWorkingAfterTheParametersChange(t *testing.T) {
	encoded, _ := NewHasher(Params{Memory: 8, Time: 1, Threads: 1}).Hash("password-123456")

	newer := NewHasher(Params{Memory: 16, Time: 2, Threads: 1})

	if ok, err := Verify("password-123456", encoded); err != nil || !ok {
		t.Fatalf("the parameters are read from the hash: %v, %v", ok, err)
	}
	if !newer.NeedsRehash(encoded) {
		t.Fatal("a hash made with other parameters needs rehashing")
	}
	if fresh, _ := newer.Hash("x"); newer.NeedsRehash(fresh) {
		t.Fatal("a hash made with the current parameters does not")
	}
	if !newer.NeedsRehash("garbage") {
		t.Fatal("an unreadable hash is replaced too")
	}
}

func TestVerify_RejectsMalformedHashes(t *testing.T) {
	good, _ := NewHasher(testParams).Hash("pw")
	parts := strings.Split(good, "$")
	swap := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, "$")
	}

	tests := map[string]string{
		"empty":                 "",
		"plain text":            "hunter2",
		"wrong algorithm":       swap(1, "argon2i"),
		"wrong version":         swap(2, "v=16"),
		"missing parameters":    swap(3, "m=8"),
		"zero memory":           swap(3, "m=0,t=1,p=1"),
		"absurd memory":         swap(3, "m=4294967295,t=1,p=1"),
		"absurd time":           swap(3, "m=8,t=1000000,p=1"),
		"zero threads":          swap(3, "m=8,t=1,p=0"),
		"bad salt":              swap(4, "!!!"),
		"empty salt":            swap(4, ""),
		"bad key":               swap(5, "!!!"),
		"empty key":             swap(5, ""),
		"too few parts":         "$argon2id$v=19$m=8,t=1,p=1$c2FsdA",
		"does not start with $": strings.TrimPrefix(good, "$"),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := Verify("pw", encoded)

			if ok || err == nil {
				t.Fatalf("got %v, %v; want an error", ok, err)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want error
	}{
		{"empty", "", ErrPasswordTooShort},
		{"eleven characters", "12345678901", ErrPasswordTooShort},
		{"twelve characters", "123456789012", nil},
		{"characters are counted, not bytes", "日本語日本語日本語日本語", nil},
		{"eleven multibyte characters", "日本語日本語日本語日本", ErrPasswordTooShort},
		{"spaces count", "            ", nil},
		{"at the length limit", strings.Repeat("a", maxPasswordBytes), nil},
		{"over the length limit", strings.Repeat("a", maxPasswordBytes+1), ErrPasswordTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidatePassword(tt.pw); !errors.Is(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
