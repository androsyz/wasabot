package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordLength = 12
	maxPasswordBytes  = 1024
	saltLength        = 16
	keyLength         = 32

	// limits on parameters read back from a stored hash, so a corrupted row cannot ask for gigabytes
	maxMemoryKiB = 1 << 20
	maxTime      = 20
	maxThreads   = 32
)

var (
	ErrPasswordTooShort = errors.New("password too short")
	ErrPasswordTooLong  = errors.New("password too long")
	errBadHash          = errors.New("malformed password hash")
)

// Params are the argon2id cost settings. Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}

// DefaultParams follow the OWASP guidance for argon2id and cost roughly 100ms on a small server.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2}

type Hasher struct {
	params Params
}

func NewHasher(p Params) *Hasher {
	return &Hasher{params: p}
}

func ValidatePassword(pw string) error {
	if len(pw) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}

// Hash returns a self-describing string ($argon2id$v=19$m=...,t=...,p=...$salt$key), so the
// parameters can change later while old hashes keep verifying.
func (h *Hasher) Hash(pw string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, h.params.Time, h.params.Memory, h.params.Threads, keyLength)

	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.Memory, h.params.Time, h.params.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// NeedsRehash reports whether encoded was made with different settings than this hasher uses.
func (h *Hasher) NeedsRehash(encoded string) bool {
	p, _, _, err := parseHash(encoded)
	return err != nil || p != h.params
}

// Verify checks pw against encoded in constant time, using the parameters stored in encoded.
func Verify(pw, encoded string) (bool, error) {
	p, salt, key, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

func parseHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, errBadHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, errBadHash
	}

	var p Params
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); err != nil {
		return Params{}, nil, nil, errBadHash
	}
	if p.Memory == 0 || p.Memory > maxMemoryKiB || p.Time == 0 || p.Time > maxTime || threads == 0 || threads > maxThreads {
		return Params{}, nil, nil, errBadHash
	}
	p.Threads = uint8(threads)

	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, errBadHash
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Params{}, nil, nil, errBadHash
	}
	return p, salt, key, nil
}
