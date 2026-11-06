package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"company-community/backend/internal/config"
	"company-community/backend/internal/domain"
	"company-community/backend/internal/localauth"
	"github.com/coreos/go-oidc/v3/oidc"
)

var ErrUnauthorized = errors.New("authentication required")

type Principal struct{ Issuer, Subject string }
type Verifier interface {
	Verify(context.Context, string) (Principal, error)
}
type Users interface {
	FindIdentity(context.Context, string, string) (domain.User, error)
}
type Authenticator struct {
	Verifier Verifier
	Users    Users
}

type LocalCredentials interface {
	LocalCredential(context.Context, string) (domain.LocalCredential, error)
}

type LocalPasswordLogin struct{ Credentials LocalCredentials }

func (l LocalPasswordLogin) Authenticate(ctx context.Context, username, password string) (domain.User, error) {
	minimumPasswordLength := 12
	if strings.EqualFold(strings.TrimSpace(username), "platform-admin@commonroom.local") {
		minimumPasswordLength = 8
	}
	if len(username) < 3 || len(username) > 100 || len(password) < minimumPasswordLength || len(password) > 128 {
		return domain.User{}, ErrUnauthorized
	}
	credential, err := l.Credentials.LocalCredential(ctx, strings.TrimSpace(username))
	if err != nil || !localauth.Verify(password, credential.Salt, credential.Hash, credential.Iterations) {
		return domain.User{}, ErrUnauthorized
	}
	return credential.User, nil
}

func (a Authenticator) Authenticate(ctx context.Context, token string) (domain.User, error) {
	principal, err := a.Verifier.Verify(ctx, token)
	if err != nil {
		return domain.User{}, ErrUnauthorized
	}
	u, err := a.Users.FindIdentity(ctx, principal.Issuer, principal.Subject)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, ErrUnauthorized
	}
	if err != nil {
		return domain.User{}, err
	}
	if !u.Active {
		return domain.User{}, ErrUnauthorized
	}
	return u, nil
}

type DevelopmentVerifier struct {
	token  string
	mu     sync.RWMutex
	tokens map[string]Principal
}

// PasswordVerifier signs short-lived bearer tokens for accounts provisioned by
// an organization administrator. Account status and role are still reloaded
// from the database on each request by Authenticator.
type PasswordVerifier struct{ secret []byte }

type passwordClaims struct {
	Subject string `json:"sub"`
	Expires int64  `json:"exp"`
}

func NewPasswordVerifier(secret string) *PasswordVerifier {
	return &PasswordVerifier{secret: []byte(secret)}
}

func (v *PasswordVerifier) Issue(u domain.User) (string, error) {
	if u.Subject == "" {
		return "", ErrUnauthorized
	}
	claims, err := json.Marshal(passwordClaims{Subject: u.Subject, Expires: time.Now().Add(12 * time.Hour).Unix()})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (v *PasswordVerifier) Verify(_ context.Context, raw string) (Principal, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(raw) > 4096 {
		return Principal{}, ErrUnauthorized
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Principal{}, ErrUnauthorized
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	var claims passwordClaims
	if json.Unmarshal(data, &claims) != nil || claims.Subject == "" || claims.Expires <= time.Now().Unix() {
		return Principal{}, ErrUnauthorized
	}
	return Principal{Issuer: "local-password", Subject: claims.Subject}, nil
}

func NewDevelopmentVerifier(token string) *DevelopmentVerifier {
	return &DevelopmentVerifier{token: token, tokens: make(map[string]Principal)}
}

func (d *DevelopmentVerifier) Issue(u domain.User) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	d.mu.Lock()
	d.tokens[token] = Principal{Issuer: "development", Subject: u.Subject}
	d.mu.Unlock()
	return token, nil
}

func (d *DevelopmentVerifier) Verify(_ context.Context, token string) (Principal, error) {
	if subtle.ConstantTimeCompare([]byte(token), []byte(d.token)) == 1 {
		return Principal{Issuer: "development", Subject: "local-admin"}, nil
	}
	d.mu.RLock()
	p, ok := d.tokens[token]
	d.mu.RUnlock()
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	return p, nil
}

type oidcVerifier struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
}

func (v oidcVerifier) Verify(ctx context.Context, raw string) (Principal, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, err
	}
	if token.Subject == "" {
		return Principal{}, ErrUnauthorized
	}
	return Principal{Issuer: v.issuer, Subject: token.Subject}, nil
}

// NewVerifier never accepts roles, email domains, or identity supplied in custom headers.
// OIDC identities must also be explicitly provisioned and active in the database.
func NewVerifier(ctx context.Context, c config.Config) (Verifier, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.AuthMode == "development" {
		return NewDevelopmentVerifier(c.DevToken), nil
	}
	if c.AuthMode == "password" {
		return NewPasswordVerifier(c.AuthSecret), nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	return oidcVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: c.Audience}), issuer: c.Issuer}, nil
}
