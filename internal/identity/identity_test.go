package identity

import (
	"context"
	"errors"
	"testing"

	"company-community/backend/internal/domain"
)

type fixedVerifier struct {
	principal Principal
	err       error
}

func (v fixedVerifier) Verify(context.Context, string) (Principal, error) { return v.principal, v.err }

type fixedUsers struct {
	user            domain.User
	err             error
	issuer, subject string
}

type countingLocalCredentials struct{ calls int }

func (c *countingLocalCredentials) LocalCredential(context.Context, string) (domain.LocalCredential, error) {
	c.calls++
	return domain.LocalCredential{}, ErrUnauthorized
}

func TestLocalPasswordLengthExceptionIsLimitedToPlatformDevAccount(t *testing.T) {
	credentials := &countingLocalCredentials{}
	login := LocalPasswordLogin{Credentials: credentials}
	_, _ = login.Authenticate(context.Background(), "platform-admin@commonroom.local", "Admin@123")
	if credentials.calls != 1 {
		t.Fatal("platform account's 9-character development password should reach credential verification")
	}
	_, _ = login.Authenticate(context.Background(), "employee@mindorigin.in", "Admin@123")
	if credentials.calls != 1 {
		t.Fatal("short password for a different account should be rejected before credential lookup")
	}
}

func (u *fixedUsers) FindIdentity(_ context.Context, issuer, subject string) (domain.User, error) {
	u.issuer = issuer
	u.subject = subject
	return u.user, u.err
}
func TestOnlyProvisionedActiveIdentitiesAuthenticate(t *testing.T) {
	tests := []struct {
		name                 string
		active               bool
		lookupErr, verifyErr error
		want                 error
	}{
		{name: "active", active: true},
		{name: "offboarded", active: false, want: ErrUnauthorized},
		{name: "not provisioned", lookupErr: domain.ErrNotFound, want: ErrUnauthorized},
		{name: "invalid token", verifyErr: errors.New("invalid signature"), want: ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &fixedUsers{user: domain.User{ID: "user", Active: tt.active}, err: tt.lookupErr}
			auth := Authenticator{Verifier: fixedVerifier{principal: Principal{Issuer: "trusted", Subject: "subject"}, err: tt.verifyErr}, Users: users}
			_, err := auth.Authenticate(context.Background(), "token")
			if !errors.Is(err, tt.want) {
				t.Fatalf("Authenticate()=%v, want %v", err, tt.want)
			}
			if tt.verifyErr == nil && (users.issuer != "trusted" || users.subject != "subject") {
				t.Fatal("identity lookup did not use verified issuer and subject")
			}
		})
	}
}
func TestDevelopmentVerifierRequiresExactSecret(t *testing.T) {
	v := NewDevelopmentVerifier("a-long-random-development-token")
	for _, token := range []string{"", "wrong", "a-long-random-development-token-extra"} {
		if _, err := v.Verify(context.Background(), token); err == nil {
			t.Fatal("accepted invalid token")
		}
	}
	if p, err := v.Verify(context.Background(), "a-long-random-development-token"); err != nil || p.Subject != "local-admin" {
		t.Fatalf("valid development token failed: %v", err)
	}
}

func TestDevelopmentLoginTokensResolveToTheirOwnIdentity(t *testing.T) {
	v := NewDevelopmentVerifier("a-long-random-development-token")
	first, err := v.Issue(domain.User{Subject: "mindorigin-user"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := v.Issue(domain.User{Subject: "firstock-user"})
	if err != nil {
		t.Fatal(err)
	}
	p1, err := v.Verify(context.Background(), first)
	if err != nil || p1.Subject != "mindorigin-user" {
		t.Fatalf("first token resolved to %#v: %v", p1, err)
	}
	p2, err := v.Verify(context.Background(), second)
	if err != nil || p2.Subject != "firstock-user" {
		t.Fatalf("second token resolved to %#v: %v", p2, err)
	}
	if first == second {
		t.Fatal("separate account logins received the same token")
	}
}
