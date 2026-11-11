package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DefaultIterations is the PBKDF2-HMAC-SHA256 work factor for new password
// hashes (OWASP 2023 recommendation).
const DefaultIterations = 600_000

const (
	hashScheme = "pbkdf2-sha256"
	saltLen    = 16
	keyLen     = 32
	// MinPasswordLen is the shortest password accepted.
	MinPasswordLen = 8
	// maxPasswordLen bounds the work an attacker can force per guess.
	maxPasswordLen = 1024
)

var errMalformedHash = errors.New("malformed password hash")

// HashPassword returns password hashed with PBKDF2-HMAC-SHA256 in the form
// "$pbkdf2-sha256$i=<iterations>$<salt>$<key>" (unpadded standard base64).
func HashPassword(password string, iterations int) (string, error) {
	if iterations < 1 {
		return "", fmt.Errorf("iterations must be positive, got %d", iterations)
	}
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt) // never fails
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$%s$i=%d$%s$%s", hashScheme, iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// CheckPassword reports whether password matches an encoded hash from
// HashPassword. It runs in time independent of where the keys differ.
func CheckPassword(encoded, password string) (bool, error) {
	// "", scheme, "i=N", salt, key
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "" || parts[1] != hashScheme {
		return false, errMalformedHash
	}
	iterStr, ok := strings.CutPrefix(parts[2], "i=")
	if !ok {
		return false, errMalformedHash
	}
	iterations, err := strconv.Atoi(iterStr)
	if err != nil || iterations < 1 {
		return false, errMalformedHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[3])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := enc.DecodeString(parts[4])
	if err != nil || len(want) == 0 {
		return false, errMalformedHash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func validatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d bytes", maxPasswordLen)
	}
	return nil
}
