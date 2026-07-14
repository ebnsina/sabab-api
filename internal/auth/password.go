package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrBadCredentials is returned for both an unknown email and a wrong password.
// One error for both, deliberately: distinguishing them turns the login form
// into an oracle for which email addresses have accounts.
var ErrBadCredentials = errors.New("invalid email or password")

// Argon2id parameters. These are cost knobs, and the cost is the point: they
// make an offline attack against a stolen hash expensive.
//
// 64 MiB and 3 passes is the OWASP-recommended baseline. Lowering the memory is
// the single most damaging change you could make here, because memory-hardness
// is precisely what denies an attacker the GPU advantage.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashPassword returns an encoded Argon2id hash, salt included.
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// The standard PHC string format: the parameters travel with the hash, so
	// they can be raised later without invalidating every existing password.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against an encoded hash.
func VerifyPassword(password, encoded string) error {
	salt, want, memory, time, threads, err := decodeHash(encoded)
	if err != nil {
		return ErrBadCredentials
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))

	// Constant-time: a byte-by-byte comparison leaks, through timing, how much
	// of the hash a guess got right.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrBadCredentials
	}
	return nil
}

func decodeHash(encoded string) (salt, key []byte, memory, time uint32, threads uint8, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, errors.New("not an argon2id hash")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, 0, 0, 0, err
	}
	if version != argon2.Version {
		return nil, nil, 0, 0, 0, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return nil, nil, 0, 0, 0, err
	}

	if salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil {
		return nil, nil, 0, 0, 0, err
	}
	if key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil {
		return nil, nil, 0, 0, 0, err
	}
	return salt, key, memory, time, threads, nil
}
