package auth

import (
	"golang.org/x/crypto/bcrypt"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

// maxPasswordBytes is as much of a password as bcrypt reads.
const maxPasswordBytes = 72

// HashPassword returns the bcrypt hash stored in users.password_hash.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if errors.Is(err, bcrypt.ErrPasswordTooLong) {
		return "", errors.Invalid("password_too_long", "password must be at most 72 bytes").Wrapping(err)
	}
	if err != nil {
		return "", errors.Internalf(err, "cannot hash password")
	}
	return string(hash), nil
}

// unknownUserHash stands in for the hash of an account that doesn't exist. It
// hashes a random password nobody kept, at the cost HashPassword uses, so
// checking it takes as long as checking a real one.
const unknownUserHash = "$2a$10$KfxAu1zE2xpTdKx1kXTs8ONLzCJp2C0nnGKXJj56TL11udkRMCWz2"

// CheckPassword reports whether password is the one hash was made from. An
// empty hash means there is no such account: the password is then checked
// against a fixed hash, so the answer takes as long either way.
func CheckPassword(hash, password string) bool {
	known := hash != ""
	if !known {
		hash = unknownUserHash
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	// bcrypt ignores what follows the first 72 bytes, and HashPassword never
	// stores a longer password, so a longer one never matches.
	return known && len(password) <= maxPasswordBytes && err == nil
}
