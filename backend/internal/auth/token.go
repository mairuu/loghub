// Package auth establishes who is calling (ADR 0009): passwords, the signed
// tokens people sign in for, and the ingest key. What a caller may then do is
// decided in internal/authz.
package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

const (
	// MinSecretBytes is the shortest signing secret or ingest key accepted.
	MinSecretBytes = 32
	// TokenTTL is how long a token is accepted. There is no refresh.
	TokenTTL = 12 * time.Hour
)

var signingMethod = jwt.SigningMethodHS256

// claims is a token's payload. A struct rather than jwt.MapClaims, so a
// claim of the wrong type fails when the token is decoded.
type claims struct {
	Role string `json:"role"`
	// Tenant is empty for an admin.
	Tenant string `json:"tenant,omitempty"`
	jwt.RegisteredClaims
}

type TokenConfig struct {
	// Secret signs and verifies tokens: AUTH_SECRET.
	Secret string
	// Now nil means time.Now.
	Now func() time.Time
}

// Tokens issues and verifies the HS256 tokens people sign in for.
type Tokens struct {
	secret []byte
	now    func() time.Time
	parser *jwt.Parser
}

func NewTokens(cfg TokenConfig) (*Tokens, error) {
	if len(cfg.Secret) < MinSecretBytes {
		return nil, fmt.Errorf("the token secret must be at least %d bytes, got %d", MinSecretBytes, len(cfg.Secret))
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Tokens{
		secret: []byte(cfg.Secret),
		now:    now,
		parser: jwt.NewParser(
			// The token names its algorithm, so only this one is accepted,
			// and never none.
			jwt.WithValidMethods([]string{signingMethod.Alg()}),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithStrictDecoding(),
			jwt.WithTimeFunc(now),
		),
	}, nil
}

// Issue returns a token for a signed-in user and the time it expires.
func (t *Tokens) Issue(c authz.Caller) (string, time.Time, error) {
	if err := checkUser(c); err != nil {
		return "", time.Time{}, errors.Internalf(err, "cannot issue a token")
	}
	// Token times are whole seconds.
	now := t.now().Truncate(time.Second)
	expires := now.Add(TokenTTL)
	token, err := jwt.NewWithClaims(signingMethod, claims{
		Role:   c.Role.String(),
		Tenant: c.Tenant,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(c.UserID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, errors.Internalf(err, "cannot sign a token")
	}
	return token, expires, nil
}

// Parse verifies a token and returns the user it was issued to. Every
// failure is an invalid_token error.
func (t *Tokens) Parse(token string) (authz.Caller, error) {
	var cl claims
	_, err := t.parser.ParseWithClaims(token, &cl, func(*jwt.Token) (any, error) { return t.secret, nil })
	if err != nil {
		return authz.Caller{}, invalidToken(err)
	}

	role, ok := authz.UserRole(cl.Role)
	if !ok {
		return authz.Caller{}, invalidToken(fmt.Errorf("role %q is not a user role", cl.Role))
	}
	id, err := strconv.ParseInt(cl.Subject, 10, 64)
	if err != nil {
		return authz.Caller{}, invalidToken(fmt.Errorf("subject %q is not a user id", cl.Subject))
	}
	c := authz.Caller{Role: role, Tenant: cl.Tenant, UserID: id}
	if err := checkUser(c); err != nil {
		return authz.Caller{}, invalidToken(err)
	}
	return c, nil
}

// checkUser holds a signed-in user to what the users table allows: an admin
// has no tenant, and a viewer has one.
func checkUser(c authz.Caller) error {
	switch {
	case c.Role != authz.Admin && c.Role != authz.Viewer:
		return fmt.Errorf("%s is not a user role", c.Role)
	case c.Role == authz.Admin && c.Tenant != "":
		return fmt.Errorf("an admin has no tenant, got %q", c.Tenant)
	case c.Role == authz.Viewer && c.Tenant == "":
		return fmt.Errorf("a viewer needs a tenant")
	case c.UserID <= 0:
		return fmt.Errorf("user id %d is not positive", c.UserID)
	}
	return nil
}

func invalidToken(err error) error {
	return errors.Unauthenticated("invalid_token", "the token is invalid or has expired; sign in again").Wrapping(err)
}
