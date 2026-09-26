package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	WebPasswordHashScheme     = "pbkdf2-sha256"
	webPasswordHashIterations = 210000
	webPasswordHashSaltBytes  = 16
	webPasswordHashKeyBytes   = 32
)

// HashWebPassword derives a storable hash for a Web administrator password.
// The format is pbkdf2-sha256$<iterations>$<salt>$<key> with base64 (no
// padding) values. Use it for the WebPasswordHash setting and remove
// WebPassword afterwards.
func HashWebPassword(password string) (string, error) {
	salt := make([]byte, webPasswordHashSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, webPasswordHashIterations, webPasswordHashKeyBytes)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", WebPasswordHashScheme, webPasswordHashIterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyWebPassword compares input against the configured hash when present and
// falls back to a constant-time comparison of the plaintext value otherwise.
func VerifyWebPassword(plain, hash, input string) bool {
	if hash != "" {
		return verifyWebPasswordHash(hash, input)
	}
	return subtle.ConstantTimeCompare([]byte(plain), []byte(input)) == 1
}

func verifyWebPasswordHash(hash, input string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != WebPasswordHashScheme {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 || iterations > 10_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual, err := pbkdf2.Key(sha256.New, input, salt, iterations, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
