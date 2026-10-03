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

// HashParams is the cost of argon2id.
type HashParams struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// DefaultHashParams follow the OWASP recommendation for argon2id (19 MiB, 2 iterations) with some room.
var DefaultHashParams = HashParams{Time: 3, Memory: 32 * 1024, Threads: 2}

// FastHashParams are for tests only.
var FastHashParams = HashParams{Time: 1, Memory: 8, Threads: 1}

const keyLen = 32

// HashPassword returns the encoded argon2id hash (`$argon2id$v=19$m=..,t=..,p=..$salt$hash`).
func HashPassword(p string, par HashParams) (string, error) {
	if p == "" {
		return "", errors.New("auth: empty password")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(p), salt, par.Time, par.Memory, par.Threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, par.Memory, par.Time, par.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword verifies a password against an encoded hash in constant time.
func CheckPassword(p, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: unknown hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("auth: unknown argon2 version")
	}
	var par HashParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &par.Memory, &par.Time, &par.Threads); err != nil {
		return false, errors.New("auth: bad parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(p), salt, par.Time, par.Memory, par.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
