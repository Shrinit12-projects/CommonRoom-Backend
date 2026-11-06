package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"company-community/backend/internal/content"
	"company-community/backend/internal/domain"
	"company-community/backend/internal/identity"
	"company-community/backend/internal/product"
	"company-community/backend/internal/search"
	"company-community/backend/internal/tags"
)

//go:embed web/index.html web/assets/*
var webFiles embed.FS

type Authenticator interface {
	Authenticate(context.Context, string) (domain.User, error)
}
type Administration interface {
	UpdateUser(context.Context, domain.User, string, string, bool) (domain.User, error)
	DeleteUser(context.Context, domain.User, string) error
	Audit(context.Context, domain.User, int, int) ([]map[string]any, error)
}
type OrganizationAdministration interface {
	Organizations(context.Context, domain.User) ([]domain.Organization, error)
	OrganizationUsers(context.Context, domain.User, string, string, int, int) ([]domain.User, error)
	CreateOrganization(context.Context, domain.User, domain.OrganizationAdminInput) (domain.Organization, domain.User, error)
	CreateOrganizationUser(context.Context, domain.User, domain.OrganizationUserInput) (domain.User, error)
}
type LocalLogin interface {
	Authenticate(context.Context, string, string) (domain.User, error)
}
type LocalTokenIssuer interface {
	Issue(domain.User) (string, error)
}
type API struct {
	Auth              Authenticator
	Content           *content.Service
	Tags              *tags.Service
	Search            *search.Service
	Admin             Administration
	OrganizationAdmin OrganizationAdministration
	Product           *product.Service
	Ping              func(context.Context) error
	Logger            *slog.Logger
	LocalLogin        LocalLogin
	DevToken          string
	LocalTokenIssuer  LocalTokenIssuer
	DevelopmentAuth   bool
	LocalAccountAuth  bool
	TrustedIssuer     string
}
type contextKey int

const userKey contextKey = 1
const requestKey contextKey = 2
const limitsKey contextKey = 3

type requestLimits struct{ ip, user, login *limiter }

func user(r *http.Request) domain.User { return r.Context().Value(userKey).(domain.User) }

type endpoint func(*http.Request) (any, int, error)

func (a *API) Handler() http.Handler {
	if a.Logger == nil {
		a.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			a.writeError(w, r, domain.ErrNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFileFS(w, r, webFiles, "web/index.html")
	})
	assetFiles, _ := fs.Sub(webFiles, "web/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetFiles))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if a.Ping == nil || a.Ping(ctx) != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	register := func(pattern string, fn endpoint) { mux.Handle(pattern, a.authenticate(a.endpoint(fn))) }
	register("GET /v1/me", func(r *http.Request) (any, int, error) { return user(r), http.StatusOK, nil })
	mux.Handle("POST /v1/auth/login", a.endpoint(a.login))
	register("GET /v1/posts", a.listPosts)
	register("POST /v1/blogs", a.createPost("blog"))
	register("POST /v1/questions", a.createPost("question"))
	register("GET /v1/posts/{id}", a.getPost)
	register("DELETE /v1/posts/{id}", func(r *http.Request) (any, int, error) {
		if err := a.Content.Delete(r.Context(), user(r), r.PathValue("id")); err != nil {
			return nil, 0, err
		}
		return map[string]bool{"deleted": true}, http.StatusOK, nil
	})
	register("PUT /v1/posts/{id}", a.updatePost)
	register("POST /v1/posts/{id}/publish", a.transition("publish"))
	register("POST /v1/posts/{id}/unpublish", a.transition("unpublish"))
	register("POST /v1/posts/{id}/close", a.transition("close"))
	register("POST /v1/posts/{id}/reopen", a.transition("reopen"))
	register("GET /v1/posts/{id}/revisions", a.revisions)
	register("GET /v1/questions/{id}/answers", a.answers)
	register("POST /v1/questions/{id}/answers", a.answer)
	register("PUT /v1/questions/{id}/accepted-answer", a.accept)
	register("GET /v1/tags", a.listTags)
	register("POST /v1/tags", a.createTag)
	register("PATCH /v1/tags/{id}", a.tagStatus)
	register("GET /v1/tag-proposals", a.proposals)
	register("POST /v1/tag-proposals", a.proposeTag)
	register("PUT /v1/tag-proposals/{id}/decision", a.reviewProposal)
	register("GET /v1/search", a.search)
	register("POST /v1/preview", func(r *http.Request) (any, int, error) {
		var in struct {
			Body string `json:"body_markdown"`
		}
		if e := decode(r, &in); e != nil {
			return nil, 0, e
		}
		if len(in.Body) > 200000 {
			return nil, 0, domain.Invalidf("body_markdown is too long")
		}
		html, e := a.Content.Render(in.Body)
		return map[string]string{"body_html": html}, 200, e
	})
	register("PUT /v1/moderation/posts/{id}", a.moderate)
	register("PATCH /v1/admin/users/{id}", a.updateUser)
	register("DELETE /v1/admin/users/{id}", func(r *http.Request) (any, int, error) {
		if a.Admin == nil {
			return nil, 0, domain.ErrNotFound
		}
		return map[string]bool{"deleted": true}, http.StatusOK, a.Admin.DeleteUser(r.Context(), user(r), r.PathValue("id"))
	})
	register("POST /v1/admin/users", a.createOrganizationUser)
	register("GET /v1/admin/organizations/{id}/users", a.listOrganizationUsers)
	register("GET /v1/admin/organizations", a.listOrganizations)
	register("POST /v1/admin/organizations", a.createOrganization)
	register("GET /v1/admin/audit-logs", a.audit)
	if a.Product != nil {
		a.productRoutes(mux, register)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { a.writeError(w, r, domain.ErrNotFound) })
	return a.middleware(mux)
}
func (a *API) endpoint(fn endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.PathValue("id"); id != "" && !domain.ValidID(id) {
			a.writeError(w, r, domain.Invalidf("id must be a UUID"))
			return
		}
		data, status, err := fn(r)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeJSON(w, status, map[string]any{"data": data})
	})
}
func (a *API) login(r *http.Request) (any, int, error) {
	if a.LocalLogin == nil || (a.DevToken == "" && a.LocalTokenIssuer == nil) {
		return nil, 0, identity.ErrUnauthorized
	}
	var in struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 0, err
	}
	login := strings.TrimSpace(in.Email)
	if login == "" {
		login = strings.TrimSpace(in.Username) // Keep compatibility with older local clients.
	}
	u, err := a.LocalLogin.Authenticate(r.Context(), login, in.Password)
	if err != nil {
		return nil, 0, err
	}
	token := a.DevToken
	if a.LocalTokenIssuer != nil {
		var err error
		token, err = a.LocalTokenIssuer.Issue(u)
		if err != nil {
			return nil, 0, err
		}
	}
	return map[string]any{"token": token, "user": u}, http.StatusOK, nil
}
func writeRateLimit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "60")
	requestID, _ := r.Context().Value(requestKey).(string)
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]string{"code": "rate_limited", "message": "request limit exceeded; retry after 60 seconds", "request_id": requestID}})
}
func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 16384 {
			if limitFailedAuth(w, r) {
				return
			}
			a.writeError(w, r, identity.ErrUnauthorized)
			return
		}
		u, err := a.Auth.Authenticate(r.Context(), parts[1])
		if err != nil {
			if errors.Is(err, identity.ErrUnauthorized) && limitFailedAuth(w, r) {
				return
			}
			a.writeError(w, r, err)
			return
		}
		if u.Role == "platform_admin" {
			p := r.URL.Path
			allowed := p == "/v1/me" || p == "/v1/admin/organizations" || strings.HasPrefix(p, "/v1/admin/organizations/") || p == "/v1/admin/users" || ((r.Method == http.MethodPatch || r.Method == http.MethodDelete) && strings.HasPrefix(p, "/v1/admin/users/"))
			if !allowed {
				a.writeError(w, r, domain.ErrForbidden)
				return
			}
		}
		if limits, ok := r.Context().Value(limitsKey).(*requestLimits); ok && !limits.user.allow(u.ID, time.Now()) {
			writeRateLimit(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}
func limitFailedAuth(w http.ResponseWriter, r *http.Request) bool {
	limits, ok := r.Context().Value(limitsKey).(*requestLimits)
	if !ok || limits.ip.allow(clientIP(r), time.Now()) {
		return false
	}
	writeRateLimit(w, r)
	return true
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func decode(r *http.Request, out any) error {
	if media := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); media != "application/json" {
		return domain.Invalidf("Content-Type must be application/json")
	}
	dec := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return domain.Invalidf("invalid JSON body or unsupported fields")
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return domain.Invalidf("request body must be a JSON object")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return domain.Invalidf("request must contain one JSON value")
	}
	fields := json.NewDecoder(bytes.NewReader(raw))
	fields.DisallowUnknownFields()
	if err := fields.Decode(out); err != nil {
		return domain.Invalidf("invalid JSON body or unsupported fields")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	code, message, status := "internal_error", "an unexpected error occurred", http.StatusInternalServerError
	var invalid domain.Invalid
	switch {
	case errors.Is(err, identity.ErrUnauthorized):
		code, message, status = "unauthorized", "valid authentication is required", http.StatusUnauthorized
		w.Header().Set("WWW-Authenticate", `Bearer realm="community"`)
	case errors.Is(err, domain.ErrNotFound):
		code, message, status = "not_found", "resource not found", http.StatusNotFound
	case errors.Is(err, domain.ErrForbidden):
		code, message, status = "forbidden", "permission denied", http.StatusForbidden
	case errors.Is(err, domain.ErrConflict):
		code, message, status = "conflict", "resource conflicts with current state; refresh before retrying", http.StatusConflict
	case errors.As(err, &invalid):
		code, message, status = "validation_error", invalid.Message, http.StatusBadRequest
	case errors.Is(err, context.DeadlineExceeded):
		code, message, status = "timeout", "request timed out", http.StatusGatewayTimeout
	}
	requestID, _ := r.Context().Value(requestKey).(string)
	if status >= 500 {
		a.Logger.Error("request failed", "request_id", requestID, "error", err)
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID}})
}

type response struct {
	http.ResponseWriter
	status int
}

func (w *response) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *response) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

type window struct {
	start time.Time
	count int
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]window
	limit   int
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]window{}
	}
	item, exists := l.entries[key]
	if !exists && len(l.entries) >= 10000 {
		for k, v := range l.entries {
			if now.Sub(v.start) >= time.Minute {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= 10000 {
			return false
		}
	}
	if item.start.IsZero() || now.Sub(item.start) >= time.Minute {
		item = window{start: now}
	}
	item.count++
	l.entries[key] = item
	limit := l.limit
	if limit <= 0 {
		limit = 120
	}
	return item.count <= limit
}
func (a *API) middleware(next http.Handler) http.Handler {
	limits := &requestLimits{ip: &limiter{limit: 120}, user: &limiter{limit: 600}, login: &limiter{limit: 20}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			http.Error(w, "request unavailable", http.StatusServiceUnavailable)
			return
		}
		id := hex.EncodeToString(b[:])
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), requestKey, id), 15*time.Second)
		ctx = context.WithValue(ctx, limitsKey, limits)
		defer cancel()
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		recorded := &response{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				a.Logger.Error("request panic", "request_id", id)
				if recorded.status == 0 {
					a.writeError(recorded, r, errors.New("request panic"))
				}
			}
			a.Logger.Info("http_request", "request_id", id, "method", r.Method, "status", recorded.status, "duration_ms", time.Since(start).Milliseconds())
		}()
		if r.URL.Path == "/v1/auth/login" && !limits.login.allow(clientIP(r), start) {
			writeRateLimit(recorded, r)
			return
		}
		r.Body = http.MaxBytesReader(recorded, r.Body, 6<<20)
		next.ServeHTTP(recorded, r)
	})
}
func filters(r *http.Request) (domain.Filter, error) {
	q := r.URL.Query()
	f := domain.Filter{Kind: q.Get("type"), Query: q.Get("q"), TagID: q.Get("tag_id"), CategoryID: q.Get("category_id"), AuthorID: q.Get("author_id"), Status: q.Get("status"), Sort: q.Get("sort"), Limit: 20, From: q.Get("from"), To: q.Get("to")}
	if len(f.Query) > 200 {
		return f, domain.Invalidf("q must not exceed 200 bytes")
	}
	if f.Kind != "" && f.Kind != "blog" && f.Kind != "question" && f.Kind != "answer" {
		return f, domain.Invalidf("type must be blog, question, or answer")
	}
	if f.Sort != "" && f.Sort != "relevance" && f.Sort != "newest" && f.Sort != "updated" && f.Sort != "helpful" && f.Sort != "unanswered" {
		return f, domain.Invalidf("sort must be relevance, newest, updated, helpful, or unanswered")
	}
	switch f.Status {
	case "", "draft", "published", "open", "solved", "closed", "duplicate":
	default:
		return f, domain.Invalidf("unsupported status")
	}
	if f.TagID != "" && !domain.ValidID(f.TagID) || f.CategoryID != "" && !domain.ValidID(f.CategoryID) || f.AuthorID != "" && !domain.ValidID(f.AuthorID) {
		return f, domain.Invalidf("tag_id, category_id and author_id must be UUIDs")
	}
	for label, value := range map[string]string{"from": f.From, "to": f.To} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return f, domain.Invalidf("%s must use YYYY-MM-DD", label)
			}
		}
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return f, domain.Invalidf("from must be on or before to")
	}
	for key, target := range map[string]*int{"limit": &f.Limit, "offset": &f.Offset} {
		if raw := q.Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return f, domain.Invalidf("%s must be an integer", key)
			}
			*target = n
		}
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 10000 {
		return f, domain.Invalidf("limit must be 1 to 100; offset must be 0 to 10000")
	}
	return f, nil
}
func page(data any, f domain.Filter) any {
	return map[string]any{"items": data, "limit": f.Limit, "offset": f.Offset}
}
