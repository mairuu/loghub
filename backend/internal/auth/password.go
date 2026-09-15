package auth

import (
	"golang.org/x/crypto/bcrypt"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

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
