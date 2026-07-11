package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor for password hashing. 12 is a modern default:
// noticeably stronger than bcrypt's own DefaultCost (10) while staying well under
// a human-perceptible login delay.
const bcryptCost = 12

// HashPassword returns a bcrypt hash of the plaintext password. An empty password
// is rejected so an SSO-only account can never be provisioned with a guessable
// empty-string credential.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("auth: password must not be empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hashing password: %w", err)
	}
	return string(h), nil
}

// ComparePassword reports whether password matches the stored bcrypt hash. An
// empty hash (SSO-only account) never matches, so such accounts cannot be logged
// into with a password.
func ComparePassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
