package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"company-community/backend/internal/domain"
	"company-community/backend/internal/identity"
)

type deniedAuth struct{}
type platformAdminAuth struct{}
type employeeAuth struct{}

type acceptingLocalLogin struct{ username, password string }

func (l *acceptingLocalLogin) Authenticate(_ context.Context, username, password string) (domain.User, error) {
	l.username, l.password = username, password
	return domain.User{ID: "admin-id", DisplayName: "Local admin", Role: "admin", Active: true}, nil
}

func (deniedAuth) Authenticate(context.Context, string) (domain.User, error) {
	return domain.User{}, identity.ErrUnauthorized
}
func (platformAdminAuth) Authenticate(context.Context, string) (domain.User, error) {
	return domain.User{ID: "platform-id", OrganizationID: "mindorigin-id", Role: "platform_admin", Active: true}, nil
}
func (employeeAuth) Authenticate(context.Context, string) (domain.User, error) {
	return domain.User{ID: "employee-id", OrganizationID: "mindorigin-id", Role: "employee", Active: true}, nil
}
func TestProtectedRoutesRequireAuthentication(t *testing.T) {
	api := &API{Auth: deniedAuth{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := api.Handler()
	for _, path := range []string{"/v1/me", "/v1/posts", "/v1/search?q=private", "/v1/admin/audit-logs", "/v1/tags"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Request-ID") == "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing privacy or request tracing headers")
		}
	}
}

func TestLocalLoginReturnsDevelopmentBearerToken(t *testing.T) {
	localLogin := &acceptingLocalLogin{}
	api := &API{LocalLogin: localLogin, DevToken: "development-token", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"email":"admin@mindorigin.in","password":"a-valid-development-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"token":"development-token"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if localLogin.username != "admin@mindorigin.in" || localLogin.password != "a-valid-development-password" {
		t.Fatalf("login received username=%q password length=%d", localLogin.username, len(localLogin.password))
	}
}

func TestPlatformAdminCannotUseTenantCommunityRoutes(t *testing.T) {
	api := &API{Auth: platformAdminAuth{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest(http.MethodGet, "/v1/posts", nil)
	req.Header.Set("Authorization", "Bearer platform-token")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("tenant content route status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestOrganizationEndpointsRejectEmployees(t *testing.T) {
	api := &API{Auth: employeeAuth{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/v1/admin/organizations", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer employee-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s organization route status=%d body=%s", method, w.Code, w.Body.String())
		}
	}
}

func TestProvisionIdentityRequiresModeAppropriateCredentials(t *testing.T) {
	dev := &API{DevelopmentAuth: true}
	issuer, subject, salt, hash, iterations, err := dev.provisionIdentity("person@example.com", "", "a-long-local-password")
	if err != nil || issuer != "development" || subject == "" || len(salt) != 16 || len(hash) != 32 || iterations < 200000 {
		t.Fatalf("development provisioning failed: issuer=%q subject=%q err=%v", issuer, subject, err)
	}
	prod := &API{TrustedIssuer: "https://id.example.com"}
	issuer, subject, _, _, _, err = prod.provisionIdentity("person@example.com", "trusted-subject", "")
	if err != nil || issuer != "https://id.example.com" || subject != "trusted-subject" {
		t.Fatalf("OIDC provisioning failed: issuer=%q subject=%q err=%v", issuer, subject, err)
	}
	if _, _, _, _, _, err = prod.provisionIdentity("person@example.com", "", "password-not-used"); err == nil {
		t.Fatal("OIDC provisioning accepted an unverified identity or a local password")
	}
}
func TestStrictJSONInput(t *testing.T) {
	for _, body := range []string{`{"title":"value","role":"admin"}`, `{"title":"one"} {"title":"two"}`, `null`, ``} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		var in struct {
			Title string `json:"title"`
		}
		err := decode(req, &in)
		if err == nil {
			t.Fatalf("accepted invalid JSON: %s", body)
		}
	}
}
func TestRequestFiltersRejectInvalidLimitsAndReferences(t *testing.T) {
	for _, query := range []string{"limit=101", "offset=-1", "author_id=not-a-uuid", "tag_id=bad", "sort=arbitrary", "type=people", "status=unknown"} {
		if _, err := filters(httptest.NewRequest(http.MethodGet, "/?"+query, nil)); err == nil {
			t.Fatalf("accepted filter %s", query)
		}
	}
}
func TestRateLimitWindow(t *testing.T) {
	l := &limiter{}
	now := time.Now()
	for i := 0; i < 120; i++ {
		if !l.allow("client", now) {
			t.Fatal("limited too early")
		}
	}
	if l.allow("client", now) {
		t.Fatal("did not enforce limit")
	}
	if !l.allow("client", now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
}

func TestRateLimitSeparatesAccountsAndHonorsConfiguredCeiling(t *testing.T) {
	l := &limiter{limit: 2}
	now := time.Now()
	for i := 0; i < 2; i++ {
		if !l.allow("account-a", now) || !l.allow("account-b", now) {
			t.Fatal("one account consumed another account's quota")
		}
	}
	if l.allow("account-a", now) {
		t.Fatal("configured per-account ceiling was not enforced")
	}
}
