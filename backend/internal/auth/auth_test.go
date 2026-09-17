package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

const (
	secret    = "0123456789abcdef0123456789abcdef"
	ingestKey = "ingest-key-ingest-key-ingest-key-0"
)

var (
	now    = time.Date(2026, 9, 17, 10, 30, 15, 500, time.UTC)
	admin  = authz.Caller{Role: authz.Admin, UserID: 1}
	viewer = authz.Caller{Role: authz.Viewer, Tenant: "demoA", UserID: 2}
)

func newTokens(t *testing.T, at *time.Time) *auth.Tokens {
	t.Helper()
	tokens, err := auth.NewTokens(auth.TokenConfig{Secret: secret, Now: func() time.Time { return *at }})
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func TestTokenRoundTrip(t *testing.T) {
	at := now
	tokens := newTokens(t, &at)
	for _, c := range []authz.Caller{admin, viewer} {
		token, expires, err := tokens.Issue(c)
		if err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, 9, 17, 22, 30, 15, 0, time.UTC); !expires.Equal(want) {
			t.Errorf("expires %v, want %v", expires, want)
		}
		got, err := tokens.Parse(token)
		if err != nil || got != c {
			t.Errorf("Parse = %+v, %v; want %+v", got, err, c)
		}
	}
}

func TestTokenExpiry(t *testing.T) {
	at := now
	tokens := newTokens(t, &at)
	token, expires, err := tokens.Issue(viewer)
	if err != nil {
		t.Fatal(err)
	}

	at = expires.Add(-time.Second)
	if _, err := tokens.Parse(token); err != nil {
		t.Errorf("a second before expiry: %v", err)
	}
	at = expires
	if _, err := tokens.Parse(token); !isInvalidToken(err) {
		t.Errorf("at expiry: %v, want invalid_token", err)
	}
	// Issued in the future: a clock that went backwards, or a forged time.
	at = now.Add(-time.Minute)
	if _, err := tokens.Parse(token); !isInvalidToken(err) {
		t.Errorf("before issue: %v, want invalid_token", err)
	}
}

func TestIssueRefusesNonUsers(t *testing.T) {
	at := now
	tokens := newTokens(t, &at)
	for _, c := range []authz.Caller{
		{},
		{Role: authz.Collector},
		{Role: authz.Admin, Tenant: "demoA", UserID: 1},
		{Role: authz.Viewer, UserID: 2},
		{Role: authz.Viewer, Tenant: "demoA"},
	} {
		if token, _, err := tokens.Issue(c); err == nil {
			t.Errorf("Issue(%+v) = %s", c, token)
		}
	}
}

func TestParseRejects(t *testing.T) {
	at := now
	tokens := newTokens(t, &at)
	valid := jwt.MapClaims{
		"sub": "2", "role": "viewer", "tenant": "demoA",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	with := func(changes jwt.MapClaims) jwt.MapClaims {
		out := jwt.MapClaims{}
		for k, v := range valid {
			out[k] = v
		}
		for k, v := range changes {
			if v == nil {
				delete(out, k)
			} else {
				out[k] = v
			}
		}
		return out
	}
	sign := func(method jwt.SigningMethod, key any, cl jwt.MapClaims) string {
		s, err := jwt.NewWithClaims(method, cl).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	hs256 := func(cl jwt.MapClaims) string { return sign(jwt.SigningMethodHS256, []byte(secret), cl) }

	// The fixture is sound, so each case fails for its own reason.
	if _, err := tokens.Parse(hs256(valid)); err != nil {
		t.Fatalf("valid token: %v", err)
	}

	good := hs256(valid)
	parts := strings.Split(good, ".")

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"not a token", "abc"},
		{"alg none", sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, valid)},
		{"HS512 with the right secret", sign(jwt.SigningMethodHS512, []byte(secret), valid)},
		{"RS256 header on an HMAC signature", strings.Join([]string{
			"eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9", parts[1], parts[2],
		}, ".")},
		{"wrong secret", sign(jwt.SigningMethodHS256, []byte(strings.Repeat("x", 32)), valid)},
		{"payload changed", parts[0] + "." + strings.TrimRight(parts[1], "Q") + "R." + parts[2]},
		{"signature dropped", parts[0] + "." + parts[1] + "."},
		{"no expiry", hs256(with(jwt.MapClaims{"exp": nil}))},
		{"expired", hs256(with(jwt.MapClaims{"exp": now.Add(-time.Second).Unix()}))},
		{"role is not a string", hs256(with(jwt.MapClaims{"role": 1}))},
		{"collector role", hs256(with(jwt.MapClaims{"role": "collector", "tenant": nil}))},
		{"unknown role", hs256(with(jwt.MapClaims{"role": "root"}))},
		{"no role", hs256(with(jwt.MapClaims{"role": nil}))},
		{"admin with a tenant", hs256(with(jwt.MapClaims{"role": "admin"}))},
		{"viewer without a tenant", hs256(with(jwt.MapClaims{"tenant": nil}))},
		{"no subject", hs256(with(jwt.MapClaims{"sub": nil}))},
		{"subject is not an id", hs256(with(jwt.MapClaims{"sub": "alice"}))},
		{"subject is zero", hs256(with(jwt.MapClaims{"sub": "0"}))},
		{"subject is a number", hs256(with(jwt.MapClaims{"sub": 2}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if c, err := tokens.Parse(tc.token); !isInvalidToken(err) {
				t.Errorf("Parse = %+v, %v; want invalid_token", c, err)
			}
		})
	}
}

func isInvalidToken(err error) bool {
	return errors.KindOf(err) == errors.KindUnauthenticated && errors.CodeOf(err) == "invalid_token"
}

func TestSecretsMustBeLong(t *testing.T) {
	short := secret[:auth.MinSecretBytes-1]
	if _, err := auth.NewTokens(auth.TokenConfig{Secret: short}); err == nil {
		t.Error("NewTokens took a short secret")
	}
	tokens, err := auth.NewTokens(auth.TokenConfig{Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewAuthenticator(tokens, short); err == nil {
		t.Error("NewAuthenticator took a short ingest key")
	}
}

func newAuthenticator(t *testing.T) (*auth.Authenticator, *auth.Tokens) {
	t.Helper()
	tokens, err := auth.NewTokens(auth.TokenConfig{Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.NewAuthenticator(tokens, ingestKey)
	if err != nil {
		t.Fatal(err)
	}
	return a, tokens
}

func TestAuthenticate(t *testing.T) {
	a, tokens := newAuthenticator(t)
	token, _, err := tokens.Issue(viewer)
	if err != nil {
		t.Fatal(err)
	}

	anonymous := authz.Caller{Role: authz.Anonymous}
	collector := authz.Caller{Role: authz.Collector}
	for _, tc := range []struct {
		name    string
		headers []string
		want    authz.Caller
		invalid bool
	}{
		{name: "no credential", want: anonymous},
		{name: "ingest key", headers: []string{"Bearer " + ingestKey}, want: collector},
		{name: "scheme in any case, extra space", headers: []string{"  bearer   " + ingestKey + " "}, want: collector},
		{name: "user token", headers: []string{"Bearer " + token}, want: viewer},
		{name: "ingest key with a byte more", headers: []string{"Bearer " + ingestKey + "x"}, invalid: true},
		{name: "ingest key cut short", headers: []string{"Bearer " + ingestKey[:len(ingestKey)-1]}, invalid: true},
		{name: "not a bearer", headers: []string{"Basic " + ingestKey}, invalid: true},
		{name: "scheme only", headers: []string{"Bearer"}, invalid: true},
		{name: "scheme and space", headers: []string{"Bearer "}, invalid: true},
		{name: "credential only", headers: []string{ingestKey}, invalid: true},
		{name: "empty header", headers: []string{""}, invalid: true},
		{name: "two credentials", headers: []string{"Bearer " + ingestKey, "Bearer " + token}, invalid: true},
		{name: "garbage token", headers: []string{"Bearer abc.def.ghi"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			for _, h := range tc.headers {
				r.Header.Add("Authorization", h)
			}
			got, err := a.Authenticate(r)
			if tc.invalid {
				if !isInvalidToken(err) {
					t.Errorf("Authenticate = %+v, %v; want invalid_token", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("Authenticate = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	a, tokens := newAuthenticator(t)
	token, _, err := tokens.Issue(admin)
	if err != nil {
		t.Fatal(err)
	}

	var seen *authz.Caller
	var failed error
	h := a.Middleware(func(w http.ResponseWriter, r *http.Request, err error) {
		failed = err
		w.WriteHeader(http.StatusUnauthorized)
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := auth.CallerFrom(r.Context())
		seen = &c
	}))

	for _, tc := range []struct {
		header string
		want   *authz.Caller
	}{
		{"", &authz.Caller{Role: authz.Anonymous}},
		{"Bearer " + token, &admin},
		{"Bearer " + token + "x", nil},
	} {
		seen, failed = nil, nil
		r := httptest.NewRequest("GET", "/", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		switch {
		case tc.want == nil && (seen != nil || !isInvalidToken(failed)):
			t.Errorf("%q: handler saw %+v, fail got %v; want only invalid_token", tc.header, seen, failed)
		case tc.want != nil && (seen == nil || *seen != *tc.want || failed != nil):
			t.Errorf("%q: handler saw %+v, fail got %v; want %+v", tc.header, seen, failed, *tc.want)
		}
	}

	if c := auth.CallerFrom(t.Context()); c.Authenticated() {
		t.Errorf("a context without a caller has %+v", c)
	}
}

func TestCheckPassword(t *testing.T) {
	long := strings.Repeat("p", 72)
	hash, err := auth.HashPassword(long)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		hash     string
		password string
		want     bool
	}{
		{"right password", hash, long, true},
		{"wrong password", hash, long[:71] + "q", false},
		{"no account", "", long, false},
		{"bytes past 72", hash, long + "anything", false},
		{"NUL past 72", hash, long + "\x00", false},
		{"unusable hash", "not a hash", long, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.CheckPassword(tc.hash, tc.password); got != tc.want {
				t.Errorf("CheckPassword = %v, want %v", got, tc.want)
			}
		})
	}
}

// An unknown email is checked against a fixed hash, which must cost what a
// real one does or the response time gives the difference away.
func TestUnknownUserHashCostsTheSame(t *testing.T) {
	hash, err := auth.HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	real, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := bcrypt.Cost([]byte(auth.UnknownUserHash))
	if err != nil {
		t.Fatal(err)
	}
	if fixed != real {
		t.Errorf("the fixed hash costs %d, a real one %d", fixed, real)
	}
}
